package auth

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate は auth.users を作る。何度流しても同じ結果になる。
// auth スキーマは REST のカタログ（public だけ）に入らないので、/rest/v1 からは見えない。
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS auth;
		CREATE TABLE IF NOT EXISTS auth.users (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			email text NOT NULL,
			encrypted_password text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX IF NOT EXISTS users_email_key ON auth.users (lower(email));`)
	return err
}
