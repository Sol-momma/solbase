// solbase は REST API と認証（/auth/v1）のサーバーを起動する。
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/Sol-momma/solbase/internal/auth"
	"github.com/Sol-momma/solbase/internal/rest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	db, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://solbase:solbase@localhost:5432/solbase"))
	if err != nil {
		log.Fatal(err)
	}
	h, err := rest.New(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	if err := auth.Migrate(ctx, db); err != nil {
		log.Fatal(err)
	}
	// 秘密鍵は環境変数だけから読む（既定値は置かない）
	a, err := auth.New(db, os.Getenv("JWT_SECRET"))
	if err != nil {
		log.Fatal(err)
	}
	h.Role = a.RequestRole // REST はリクエストごとに JWT のロールで動く（RLS が効く）
	mux := http.NewServeMux()
	mux.Handle("/rest/v1/", h)
	mux.Handle("/auth/v1/", a)
	addr := env("ADDR", ":8080")
	log.Printf("solbase listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
