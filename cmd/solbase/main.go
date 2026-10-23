// solbase は REST API サーバーを起動する。
package main

import (
	"context"
	"log"
	"net/http"
	"os"

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
	mux := http.NewServeMux()
	mux.Handle("/rest/v1/", h)
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
