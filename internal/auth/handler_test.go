package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 本物の Postgres に対して動かす（モックなし）。DATABASE_URL が無ければ飛ばす。
func setup(t *testing.T) (*httptest.Server, *pgxpool.Pool, *Handler) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set (run: docker compose up -d)")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil { // 2回流しても壊れない
		t.Fatal(err)
	}
	clean := func() { db.Exec(context.Background(), `DELETE FROM auth.users WHERE email LIKE '%@solbase.test'`) }
	clean()
	t.Cleanup(clean)
	h, err := New(db, string(secret))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, db, h
}

func call(t *testing.T, srv *httptest.Server, method, path, body, bearer string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func TestSignupLoginAndVerify(t *testing.T) {
	srv, db, h := setup(t)
	cred := `{"email":"a@solbase.test","password":"pass1234"}`

	code, m := call(t, srv, "POST", "/auth/v1/signup", cred, "")
	if code != 200 || m["access_token"] == nil {
		t.Fatalf("signup: %d %v", code, m)
	}
	// 平文で保存しない：bcrypt のハッシュ（$2a$ / $2b$）が入っている
	var stored string
	db.QueryRow(context.Background(), `SELECT encrypted_password FROM auth.users WHERE email = 'a@solbase.test'`).Scan(&stored)
	if stored == "pass1234" || !strings.HasPrefix(stored, "$2") {
		t.Fatalf("password stored as %q", stored)
	}
	if _, leaked := m["user"].(map[string]any)["encrypted_password"]; leaked {
		t.Fatal("hash leaked in response")
	}

	// 同じメール（大文字違いも）では2回目の signup が失敗する
	if code, m := call(t, srv, "POST", "/auth/v1/signup", `{"email":"A@solbase.test","password":"other123"}`, ""); code != 422 || m["error"] != "user_already_exists" {
		t.Fatalf("dup signup: %d %v", code, m)
	}

	// 正しいパスワードでログイン → JWT に sub・role・exp がある
	code, m = call(t, srv, "POST", "/auth/v1/token?grant_type=password", cred, "")
	tok, _ := m["access_token"].(string)
	if code != 200 || tok == "" {
		t.Fatalf("login: %d %v", code, m)
	}
	c, err := Verify(tok, secret, time.Now())
	if err != nil || c.Sub == "" || c.Role != "authenticated" || c.Exp <= time.Now().Unix() {
		t.Fatalf("claims: %v %+v", err, c)
	}
	if c.Sub != m["user"].(map[string]any)["id"] {
		t.Fatalf("sub %s != user id", c.Sub)
	}

	// トークンで /auth/v1/user が読める
	if code, m := call(t, srv, "GET", "/auth/v1/user", "", tok); code != 200 || m["email"] != "a@solbase.test" {
		t.Fatalf("user: %d %v", code, m)
	}

	// 間違ったパスワード・無いユーザーではトークンを返さない
	for _, body := range []string{`{"email":"a@solbase.test","password":"wrong999"}`, `{"email":"nobody@solbase.test","password":"pass1234"}`} {
		code, m := call(t, srv, "POST", "/auth/v1/token?grant_type=password", body, "")
		if code != 400 || m["access_token"] != nil || m["error"] != "invalid_grant" {
			t.Fatalf("bad login %s: %d %v", body, code, m)
		}
	}

	// 期限切れ・改ざん・トークン無しは /auth/v1/user で 401
	h.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if code, _ := call(t, srv, "GET", "/auth/v1/user", "", tok); code != 401 {
		t.Fatalf("expired: %d", code)
	}
	h.Now = time.Now
	if code, _ := call(t, srv, "GET", "/auth/v1/user", "", tok[:len(tok)-2]+"xx"); code != 401 {
		t.Fatalf("tampered: %d", code)
	}
	if code, _ := call(t, srv, "GET", "/auth/v1/user", "", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
}

func TestInputChecks(t *testing.T) {
	srv, _, _ := setup(t)
	cases := []struct{ path, body, want string }{
		{"/auth/v1/signup", `not json`, "bad_json"},
		{"/auth/v1/signup", `{"email":"no-at-mark","password":"pass1234"}`, "validation_failed"},
		{"/auth/v1/signup", `{"email":"b@solbase.test","password":"12345"}`, "weak_password"},
		{"/auth/v1/signup", `{"email":"b@solbase.test","password":"` + strings.Repeat("x", 73) + `"}`, "weak_password"},
		{"/auth/v1/token?grant_type=refresh_token", `{}`, "unsupported_grant_type"},
	}
	for _, c := range cases {
		if code, m := call(t, srv, "POST", c.path, c.body, ""); code != 400 || m["error"] != c.want {
			t.Fatalf("%s %s: %d %v", c.path, c.body, code, m)
		}
	}
}

func TestNewRejectsShortSecret(t *testing.T) {
	if _, err := New(nil, "short"); err == nil {
		t.Fatal("short secret accepted")
	}
}
