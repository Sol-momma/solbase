// 結合テスト：REST で書いた変更が、RLS で見える人の WebSocket にだけ届くかを本物の Postgres で確かめる。
package realtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sol-momma/solbase/internal/auth"
	"github.com/Sol-momma/solbase/internal/realtime"
	"github.com/Sol-momma/solbase/internal/rest"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

const secret = "test-secret-test-secret-test-secret!"

func setup(t *testing.T) (*httptest.Server, *realtime.Server) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set (run: docker compose up -d)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	t.Cleanup(cancel) // 先に LISTEN を止めないと db.Close が待ち続ける
	if err := auth.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := realtime.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `
		DELETE FROM auth.users WHERE email LIKE '%@rt.test';
		DROP TABLE IF EXISTS rt_todos, rt_other;
		CREATE TABLE rt_todos (id serial PRIMARY KEY, user_id uuid NOT NULL DEFAULT auth.uid(), title text NOT NULL);
		ALTER TABLE rt_todos ENABLE ROW LEVEL SECURITY;
		CREATE POLICY own_rows ON rt_todos FOR ALL TO authenticated
			USING (user_id = auth.uid()) WITH CHECK (user_id = auth.uid());
		CREATE TABLE rt_other (id serial PRIMARY KEY, title text);
		SELECT realtime.enable('rt_todos');
		SELECT realtime.enable('rt_other');`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP TABLE IF EXISTS rt_todos, rt_other; DELETE FROM auth.users WHERE email LIKE '%@rt.test'`)
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
	rt := realtime.New(db, a.TokenRole)
	go rt.Run(ctx)
	mux := http.NewServeMux()
	mux.Handle("/rest/v1/", h)
	mux.Handle("/auth/v1/", a)
	mux.Handle("/realtime/v1", rt)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rt
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

func login(t *testing.T, srv *httptest.Server, email string) string {
	t.Helper()
	code, b := call(t, srv, "POST", "/auth/v1/signup", `{"email":"`+email+`","password":"pass1234"}`, "")
	var s struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(b, &s)
	if code != 200 || s.AccessToken == "" {
		t.Fatalf("signup %s: %d %s", email, code, b)
	}
	return s.AccessToken
}

func dial(t *testing.T, srv *httptest.Server, token string, tables ...string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/realtime/v1?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	for _, tb := range tables {
		ws.Write(context.Background(), websocket.MessageText, []byte(`{"type":"subscribe","table":"`+tb+`"}`))
		if m := recv(t, ws, time.Second); m == nil || m["type"] != "subscribed" {
			t.Fatalf("subscribe %s: %v", tb, m)
		}
	}
	return ws
}

// recv は d 以内に1件受け取る。来なければ nil。
func recv(t *testing.T, ws *websocket.Conn, d time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, b, err := ws.Read(ctx)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func title(m map[string]any, key string) any {
	r, _ := m[key].(map[string]any)
	return r["title"]
}

func TestChangesReachOnlyRowOwners(t *testing.T) {
	srv, _ := setup(t)
	ta := login(t, srv, "a@rt.test")
	tb := login(t, srv, "b@rt.test")
	wa := dial(t, srv, ta, "rt_todos")
	wb := dial(t, srv, tb, "rt_todos")
	anon := dial(t, srv, "", "rt_todos")

	// INSERT：A に1秒以内に届く。B と anon には届かない
	start := time.Now()
	if code, b := call(t, srv, "POST", "/rest/v1/rt_todos", `{"title":"a1"}`, ta); code != 201 {
		t.Fatalf("insert: %d %s", code, b)
	}
	m := recv(t, wa, time.Second)
	if m == nil || m["type"] != "INSERT" || title(m, "record") != "a1" {
		t.Fatalf("A insert event: %v", m)
	}
	t.Logf("INSERT reached A in %v", time.Since(start))

	// UPDATE・DELETE も種類つきで A に届く
	call(t, srv, "PATCH", "/rest/v1/rt_todos?title=eq.a1", `{"title":"a1-edited"}`, ta)
	if m := recv(t, wa, time.Second); m == nil || m["type"] != "UPDATE" || title(m, "record") != "a1-edited" || title(m, "old_record") != "a1" {
		t.Fatalf("A update event: %v", m)
	}
	call(t, srv, "DELETE", "/rest/v1/rt_todos?title=eq.a1-edited", "", ta)
	if m := recv(t, wa, time.Second); m == nil || m["type"] != "DELETE" || title(m, "old_record") != "a1-edited" {
		t.Fatalf("A delete event: %v", m)
	}

	// B の変更は B にだけ届く
	call(t, srv, "POST", "/rest/v1/rt_todos", `{"title":"b1"}`, tb)
	if m := recv(t, wb, time.Second); m == nil || title(m, "record") != "b1" {
		t.Fatalf("B insert event: %v", m)
	}
	for name, ws := range map[string]*websocket.Conn{"B": wb, "A": wa, "anon": anon} {
		if m := recv(t, ws, 500*time.Millisecond); m != nil {
			t.Fatalf("%s got someone else's change: %v", name, m)
		}
	}
}

func TestOnlySubscribedTables(t *testing.T) {
	srv, _ := setup(t)
	ta := login(t, srv, "a@rt.test")
	wa := dial(t, srv, ta, "rt_todos")
	call(t, srv, "POST", "/rest/v1/rt_other", `{"title":"x"}`, ta)
	if m := recv(t, wa, 500*time.Millisecond); m != nil {
		t.Fatalf("got change of a table not subscribed: %v", m)
	}
	// RLS なしのテーブルは購読すれば誰にでも届く
	wo := dial(t, srv, "", "rt_other")
	call(t, srv, "POST", "/rest/v1/rt_other", `{"title":"y"}`, ta)
	if m := recv(t, wo, time.Second); m == nil || title(m, "record") != "y" {
		t.Fatalf("rt_other event: %v", m)
	}
	// 無いテーブルはエラーを返す
	wo.Write(context.Background(), websocket.MessageText, []byte(`{"type":"subscribe","table":"nope"}`))
	if m := recv(t, wo, time.Second); m == nil || m["type"] != "error" {
		t.Fatalf("unknown table: %v", m)
	}
}

func TestBadTokenRejected(t *testing.T) {
	srv, _ := setup(t)
	for _, tok := range []string{"garbage", login(t, srv, "a@rt.test") + "x"} {
		_, res, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/realtime/v1?token="+tok, nil)
		if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: want 401, got %v %v", tok, res, err)
		}
	}
}

func TestDisconnectCleansUp(t *testing.T) {
	srv, rt := setup(t)
	ta := login(t, srv, "a@rt.test")
	var conns []*websocket.Conn
	for range 5 {
		conns = append(conns, dial(t, srv, ta, "rt_todos"))
	}
	if n := rt.Clients(); n != 5 {
		t.Fatalf("clients = %d, want 5", n)
	}
	for _, c := range conns {
		c.CloseNow()
	}
	deadline := time.Now().Add(2 * time.Second)
	for rt.Clients() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := rt.Clients(); n != 0 {
		t.Fatalf("clients after close = %d, want 0", n)
	}
	// 切れた後に変更が来てもサーバーは落ちない
	if code, b := call(t, srv, "POST", "/rest/v1/rt_todos", `{"title":"after"}`, ta); code != 201 {
		t.Fatalf("insert after close: %d %s", code, b)
	}
	w := dial(t, srv, ta, "rt_todos")
	call(t, srv, "POST", "/rest/v1/rt_todos", `{"title":"again"}`, ta)
	if m := recv(t, w, time.Second); m == nil || title(m, "record") != "again" {
		t.Fatalf("new client after close: %v", m)
	}
}
