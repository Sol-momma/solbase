# 認証（本物は GoTrue / Supabase Auth）

## 本物の Supabase
- `/auth/v1/` は [Supabase Auth](https://github.com/supabase/auth)（GoTrue を元にした Go のサーバー）が受けている。
- ユーザーは `auth.users` テーブルに入り、パスワードは bcrypt でハッシュにして `encrypted_password` 列に置く（[models/user.go](https://github.com/supabase/auth/blob/master/internal/models/user.go)）。
- `POST /signup` と `POST /token?grant_type=password` が入り口（[api/signup.go](https://github.com/supabase/auth/blob/master/internal/api/signup.go)、[api/token.go](https://github.com/supabase/auth/blob/master/internal/api/token.go)）。間違ったパスワードは 400 `invalid_grant`、登録済みのメールは 422 `user_already_exists`。
- access token は JWT。claim は `sub`（ユーザー id）・`role`（ふつうは `authenticated`）・`email`・`exp` など（[JWT のドキュメント](https://supabase.com/docs/guides/auth/jwts)）。従来は `JWT_SECRET` を共有する HS256 で署名し、今は非対称鍵（ES256 / RS256）も選べる。
- このほか、メール確認・パスワードリセット・OAuth・マジックリンク・リフレッシュトークン・MFA がある。

## solbase（この版）
- `internal/auth/schema.go`：`auth.users`（id uuid・email・encrypted_password）。メールは大文字小文字を区別せず一意。`auth` スキーマなので `/rest/v1`（`public` だけ読む）からは見えない。
- `internal/auth/handler.go`：`POST /auth/v1/signup`、`POST /auth/v1/token?grant_type=password`、`GET /auth/v1/user`（トークンの確かめ用）。パスワードは bcrypt（6〜72バイト）。無いメールでも bcrypt を1回回し、応答時間からメールの有無が分からないようにした。
- `internal/auth/jwt.go`：HS256 を標準ライブラリだけで書いた。ヘッダーの `alg` は HS256 以外を断る（`alg: none` で署名を飛ばされないように）。署名の比較は `hmac.Equal`。`exp` が無い・過ぎたものは断る。
- `Require` ミドルウェア：`Authorization: Bearer <token>` を確かめて、claims を context に入れる。③（RLS）で `/rest/v1` の前に置く予定。
- 秘密鍵は環境変数 `JWT_SECRET`（32文字以上）からだけ読み、無いとサーバーが起動しない。
- 本物との違い：メール確認・リフレッシュトークン・ログアウト・OAuth・MFA は無い。signup は確認メールなしで、すぐトークンを返す（本物の「メール確認オフ」と同じ動き）。
