package auth

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate は auth.users を作る。何度流しても同じ結果になる。
// auth スキーマは REST のカタログ（public だけ）に入らないので、/rest/v1 からは見えない。
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// 同時に起動したとき（テストの並列実行など）に CREATE ROLE がぶつからないよう1つずつ流す
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242001)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS auth;
		CREATE TABLE IF NOT EXISTS auth.users (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			email text NOT NULL,
			encrypted_password text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX IF NOT EXISTS users_email_key ON auth.users (lower(email));

		-- ③ RLS 用。PostgREST と同じく、リクエストごとに SET LOCAL ROLE で切り替える先のロール（ログインはさせない）
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon') THEN CREATE ROLE anon NOLOGIN; END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN CREATE ROLE authenticated NOLOGIN; END IF;
		END $$;

		-- auth.uid()：そのリクエストの JWT の sub（無ければ NULL）
		CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT nullif(nullif(current_setting('request.jwt.claims', true), '')::jsonb ->> 'sub', '')::uuid
		$$;

		-- auth.users は REST のロールには見せない。auth スキーマは uid() を呼べるだけにする
		REVOKE ALL ON ALL TABLES IN SCHEMA auth FROM anon, authenticated;
		GRANT USAGE ON SCHEMA auth TO anon, authenticated;
		GRANT EXECUTE ON FUNCTION auth.uid() TO anon, authenticated;

		-- public は Supabase と同じく両ロールに権限を渡し、行の出し分けは RLS のポリシーで決める
		GRANT USAGE ON SCHEMA public TO anon, authenticated;
		GRANT ALL ON ALL TABLES IN SCHEMA public TO anon, authenticated;
		GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO anon, authenticated;
		ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO anon, authenticated;
		ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO anon, authenticated;`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
