package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 本物の Postgres に対して動かす（モックなし）。DATABASE_URL が無ければ飛ばす。
func setup(t *testing.T) *httptest.Server {
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
	_, err = db.Exec(ctx, `DROP TABLE IF EXISTS test_todos;
		CREATE TABLE test_todos (id serial PRIMARY KEY, title text NOT NULL, done boolean DEFAULT false, prio int DEFAULT 0);
		INSERT INTO test_todos (title, prio) VALUES ('a', 9), ('b', 10), ('c', 1);`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(context.Background(), "DROP TABLE IF EXISTS test_todos") })
	h, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+"/rest/v1/"+path, strings.NewReader(body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var rows []map[string]any
	json.NewDecoder(res.Body).Decode(&rows)
	return res.StatusCode, rows
}

func TestCRUD(t *testing.T) {
	srv := setup(t)
	if c, r := do(t, srv, "GET", "test_todos", ""); c != 200 || len(r) != 3 {
		t.Fatalf("list: %d %v", c, r)
	}
	if c, r := do(t, srv, "GET", "test_todos?id=eq.1", ""); c != 200 || len(r) != 1 || r[0]["title"] != "a" {
		t.Fatalf("get: %d %v", c, r)
	}
	// 数値として比べる（文字列比較なら '10' < '9' になってしまう）
	if c, r := do(t, srv, "GET", "test_todos?prio=gt.5&order=prio.desc&select=title", ""); c != 200 || len(r) != 2 || r[0]["title"] != "b" || len(r[0]) != 1 {
		t.Fatalf("filter: %d %v", c, r)
	}
	if c, r := do(t, srv, "GET", "test_todos?title=like.*b*&limit=1", ""); c != 200 || len(r) != 1 {
		t.Fatalf("like: %d %v", c, r)
	}
	if c, r := do(t, srv, "POST", "test_todos", `{"title":"d","prio":3}`); c != 201 || r[0]["title"] != "d" || r[0]["id"] == nil {
		t.Fatalf("post: %d %v", c, r)
	}
	if c, r := do(t, srv, "PATCH", "test_todos?id=eq.1", `{"done":true}`); c != 200 || r[0]["done"] != true {
		t.Fatalf("patch: %d %v", c, r)
	}
	if c, r := do(t, srv, "DELETE", "test_todos?id=eq.2", ""); c != 200 || len(r) != 1 {
		t.Fatalf("delete: %d %v", c, r)
	}
	if _, r := do(t, srv, "GET", "test_todos", ""); len(r) != 3 {
		t.Fatalf("after: %v", r)
	}
}

func TestErrorsAndInjection(t *testing.T) {
	srv := setup(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"GET", "nope", "", 404},
		{"GET", "test_todos?nope=eq.1", "", 400},
		{"GET", "test_todos?id=eq.1;drop%20table%20test_todos", "", 400},
		{"GET", "test_todos?id=eq.1%3Bdrop%20table%20test_todos", "", 400},
		{"DELETE", "test_todos?id=eq.1%3Bdrop%20table%20test_todos", "", 400},
		{"GET", "test_todos?select=id%3Bdrop", "", 400},
		{"GET", "test_todos?title=eq.x%27%20or%201%3D1--", "", 200},
		{"GET", "test_todos?select=id;drop", "", 400},
		{"GET", "test_todos?order=id;drop", "", 400},
		{"POST", "test_todos", `{"nope":1}`, 400},
		{"DELETE", "test_todos", "", 400},
		{"PATCH", "test_todos", `{"done":true}`, 400},
	}
	for _, c := range cases {
		if got, _ := do(t, srv, c.method, c.path, c.body); got != c.want {
			t.Errorf("%s %s: got %d want %d", c.method, c.path, got, c.want)
		}
	}
	if _, r := do(t, srv, "GET", "test_todos", ""); len(r) != 3 {
		t.Fatalf("table damaged: %v", r)
	}
}
