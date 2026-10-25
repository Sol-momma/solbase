# RLS と JWT（本物は PostgREST ＋ Postgres の Row Level Security）

## 本物の Supabase
- アクセスの判断は Postgres がする。API サーバー（PostgREST）は「誰が来たか」を DB に伝えるだけ（[RLS のドキュメント](https://supabase.com/docs/guides/database/postgres/row-level-security)）。
- PostgREST は `authenticator` ロールで接続し、リクエストごとに1つのトランザクションを開いて、JWT の `role` claim のロール（無ければ `anon`）に `SET LOCAL ROLE` で切り替える。claims は `set_config('request.jwt.claims', <JSON>, true)` で入れる（[PostgREST の認証](https://docs.postgrest.org/en/stable/references/auth.html)、[トランザクションの中身](https://docs.postgrest.org/en/stable/references/transactions.html)）。どちらもトランザクションの中だけ効くので、接続を使い回しても次のリクエストに残らない。
- `auth.uid()` は `request.jwt.claim.sub` を uuid にして返す SQL 関数（[supabase/postgres の auth スキーマ](https://github.com/supabase/postgres/blob/develop/migrations/db/init-scripts/00000000000001-auth-schema.sql)）。ポリシーは `USING (user_id = auth.uid())` のように書く。
- `anon` と `authenticated` には public のテーブルの権限が最初から付いていて、行を絞るのは RLS のポリシー。`service_role` は RLS を飛ばせる（`BYPASSRLS`）。

## solbase（この版）
- `internal/auth/schema.go`：`anon`・`authenticated`（どちらも NOLOGIN）と `auth.uid()` を作る。両ロールに public の権限を渡し、`auth.users` は読めないようにする。
- `internal/auth/handler.go` の `RequestRole`：トークンが無ければ `anon`、あれば検証して JWT の `role`（`anon` か `authenticated` だけ許す）。壊れた・期限切れのトークンは anon に落とさず 401。
- `internal/rest/handler.go` の `query`：1リクエスト＝1トランザクション。先頭で `set_config('role', …, true)`（= `SET LOCAL ROLE`）と `set_config('request.jwt.claims', …, true)`。終わると Postgres が元に戻す。
- テスト `internal/rls`：接続を1本にして、A のリクエストの直後に同じ接続でロールと claims が空なことを確かめる。`true` を `false` にするとこのテストが落ちることも確かめた。
- 本物との違い：接続するユーザーは `authenticator` ではなく、そのままのユーザー（スーパーユーザー）。`SET ROLE` した後は RLS が効くが、`service_role` と細かいロール設計は作っていない。

## 例：自分の行だけ見える todos
```sql
CREATE TABLE todos (id serial PRIMARY KEY, user_id uuid NOT NULL DEFAULT auth.uid(), title text NOT NULL);
ALTER TABLE todos ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_rows ON todos FOR ALL TO authenticated
  USING (user_id = auth.uid()) WITH CHECK (user_id = auth.uid());
```
