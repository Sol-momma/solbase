// Package rls は ③ の結合テスト：auth の JWT → REST → Postgres の RLS が通しで効くかを本物の Postgres で確かめる。
package rls

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Sol-momma/solbase/internal/auth"
	"github.com/Sol-momma/solbase/internal/rest"
	"github.com/jackc/pgx/v5/pgxpool"
)

const secret = "test-secret-test-secret-test-secret!"

func setup(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set (run: docker compose up -d)")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	// 接続を1本にして、どのリクエストも同じ接続を使い回す（設定が残れば必ず見える）
	cfg.MaxConns = 1
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := auth.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		DELETE FROM auth.users WHERE email LIKE '%@rls.test';
		DROP TABLE IF EXISTS rls_todos;
		CREATE TABLE rls_todos (id serial PRIMARY KEY, user_id uuid NOT NULL DEFAULT auth.uid(), title text NOT NULL);
		ALTER TABLE rls_todos ENABLE ROW LEVEL SECURITY;
		CREATE POLICY own_rows ON rls_todos FOR ALL TO authenticated
			USING (user_id = auth.uid()) WITH CHECK (user_id = auth.uid());`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP TABLE IF EXISTS rls_todos; DELETE FROM auth.users WHERE email LIKE '%@rls.test'`)
	})
	a, err := auth.New(db, secret)
	if err != nil {
		t.Fatal(err)
	}
	h, err := rest.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	h.Role = a.RequestRole
	mux := http.NewServeMux()
	mux.Handle("/rest/v1/", h)
	mux.Handle("/auth/v1/", a)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, db
}

func call(t *testing.T, srv *httptest.Server, method, path, body, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b json.RawMessage
	json.NewDecoder(res.Body).Decode(&b)
	return res.StatusCode, b
}

func rows(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var r []map[string]any
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not rows: %s", b)
	}
	return r
}

func login(t *testing.T, srv *httptest.Server, email string) (token, id string) {
	t.Helper()
	code, b := call(t, srv, "POST", "/auth/v1/signup", `{"email":"`+email+`","password":"pass1234"}`, "")
	var s struct {
		AccessToken string              `json:"access_token"`
		User        struct{ ID string } `json:"user"`
	}
	json.Unmarshal(b, &s)
	if code != 200 || s.AccessToken == "" {
		t.Fatalf("signup %s: %d %s", email, code, b)
	}
	return s.AccessToken, s.User.ID
}

func TestOwnRowsOnly(t *testing.T) {
	srv, db := setup(t)
	ta, ida := login(t, srv, "a@rls.test")
	tb, idb := login(t, srv, "b@rls.test")

	// user_id は auth.uid() の既定値で入る（JSON で他人の id を入れようとしても WITH CHECK で断られる）
	for _, x := range []struct{ tok, title string }{{ta, "a1"}, {ta, "a2"}, {tb, "b1"}} {
		if code, b := call(t, srv, "POST", "/rest/v1/rls_todos", `{"title":"`+x.title+`"}`, x.tok); code != 201 {
			t.Fatalf("insert %s: %d %s", x.title, code, b)
		}
	}
	if code, _ := call(t, srv, "POST", "/rest/v1/rls_todos", `{"title":"spoof","user_id":"`+idb+`"}`, ta); code == 201 {
		t.Fatal("A could insert a row owned by B")
	}

	// A には A の行だけ、B には B の行だけ
	_, b := call(t, srv, "GET", "/rest/v1/rls_todos?order=id", "", ta)
	ra := rows(t, b)
	if len(ra) != 2 || ra[0]["user_id"] != ida || ra[1]["user_id"] != ida {
		t.Fatalf("A sees %v", ra)
	}
	noLeak(t, db) // A のリクエストの直後、同じ接続にロールも claims も残っていない
	_, b = call(t, srv, "GET", "/rest/v1/rls_todos", "", tb)
	if rb := rows(t, b); len(rb) != 1 || rb[0]["title"] != "b1" {
		t.Fatalf("B sees %v", rb)
	}

	// A が B の行を PATCH / DELETE しても変わらない（0行が返る）
	var bID int
	db.QueryRow(context.Background(), `SELECT id FROM rls_todos WHERE title = 'b1'`).Scan(&bID)
	idq := "id=eq." + itoa(bID)
	if _, b := call(t, srv, "PATCH", "/rest/v1/rls_todos?"+idq, `{"title":"hacked"}`, ta); len(rows(t, b)) != 0 {
		t.Fatalf("A patched B: %s", b)
	}
	if _, b := call(t, srv, "DELETE", "/rest/v1/rls_todos?"+idq, "", ta); len(rows(t, b)) != 0 {
		t.Fatalf("A deleted B: %s", b)
	}
	var title string
	db.QueryRow(context.Background(), `SELECT title FROM rls_todos WHERE id = $1`, bID).Scan(&title)
	if title != "b1" {
		t.Fatalf("B's row changed to %q", title)
	}

	// トークンなしは anon：ポリシーが無いので 0 行
	if code, b := call(t, srv, "GET", "/rest/v1/rls_todos", "", ""); code != 200 || len(rows(t, b)) != 0 {
		t.Fatalf("anon: %d %s", code, b)
	}
	// 壊れたトークンは 401（anon に落とさない）
	if code, _ := call(t, srv, "GET", "/rest/v1/rls_todos", "", ta[:len(ta)-2]+"xx"); code != 401 {
		t.Fatalf("bad token: %d", code)
	}

	noLeak(t, db)
}

// noLeak は、同じ接続（MaxConns=1）でリクエストの後にロールも claims も残っていないことを確かめる。
func noLeak(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	var who, claims string
	db.QueryRow(context.Background(), `SELECT current_user, coalesce(current_setting('request.jwt.claims', true), '')`).Scan(&who, &claims)
	if who == "authenticated" || who == "anon" || claims != "" {
		t.Fatalf("leaked: role=%s claims=%q", who, claims)
	}
}

func TestAuthUsersHiddenFromRoles(t *testing.T) {
	_, db := setup(t)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	tx.Exec(ctx, `SET LOCAL ROLE authenticated`)
	if _, err := tx.Exec(ctx, `SELECT encrypted_password FROM auth.users`); err == nil {
		t.Fatal("authenticated can read auth.users")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
