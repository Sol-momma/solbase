# REST API（本物は PostgREST）

## 本物の Supabase
- Supabase の `/rest/v1/` は [PostgREST](https://github.com/PostgREST/postgrest)（Haskell）がそのまま受けている。
- 起動時に `pg_catalog` からテーブル・列・外部キー・関数を読み、「スキーマキャッシュ」を作る（[ドキュメント](https://docs.postgrest.org/en/stable/references/schema_cache.html)）。スキーマを変えたら `NOTIFY pgrst, 'reload schema'` で読み直す。
- `?col=eq.val` などの[横方向のフィルタ](https://docs.postgrest.org/en/stable/references/api/tables_views.html)、`select=`、`order=`、`limit=` を1本の SQL に変換する。値はプレースホルダで渡す。
- 外部キーを使った埋め込み（`select=*,author(*)`）、OpenAPI 生成、`Prefer: return=representation` などのヘッダ、JWT の role 切り替えもある。

## solbase（この版）
- `internal/rest/catalog.go`：`information_schema` から `public` のテーブル・列・型を読む（スキーマキャッシュの代わり）。再読込は再起動か `POST /rest/v1/_reload`。
- `internal/rest/handler.go`：`GET/POST/PATCH/DELETE /rest/v1/<table>`。フィルタは `eq neq gt gte lt lte like`（`*` は `%`）。
- テーブル名・列名はカタログにあるものだけ通し、`pgx.Identifier` で引用する。値はすべて `$n` で渡し、`$n::text::<列の型>` で型を合わせる。
- 本物との違い：埋め込み・OpenAPI・ヘッダ・`or=`・認証は無い（認証は次の段階）。POST は常に作った行を返す（本物は `Prefer` ヘッダ次第）。
