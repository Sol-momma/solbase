// Package rest は PostgreSQL のテーブル定義から REST API を自動で作る（PostgREST 相当の最小版）。
package rest

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Catalog は public スキーマのテーブル名 → 列名 → 型名（udt_name）。
type Catalog map[string]map[string]string

// LoadCatalog は information_schema から public のテーブルと列を読む。
func LoadCatalog(ctx context.Context, db *pgxpool.Pool) (Catalog, error) {
	rows, err := db.Query(ctx, `SELECT c.table_name, c.column_name, c.udt_name
		FROM information_schema.columns c
		JOIN information_schema.tables t USING (table_schema, table_name)
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cat := Catalog{}
	for rows.Next() {
		var t, c, typ string
		if err := rows.Scan(&t, &c, &typ); err != nil {
			return nil, err
		}
		if cat[t] == nil {
			cat[t] = map[string]string{}
		}
		cat[t][c] = typ
	}
	return cat, rows.Err()
}
