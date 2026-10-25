# solbase

勉強用のミニ Supabase（Go）。本番では使わない。

## 動かす
```sh
docker compose up -d            # Postgres
cp .env.example .env && set -a && . ./.env && set +a
go run ./cmd/solbase            # :8080 で REST API と認証（.env に JWT_SECRET が要る）
```

## 操作用コマンド（solctl）
```sh
go run ./cmd/solctl sql "CREATE TABLE todos (id serial primary key, title text)"
go run ./cmd/solctl reload      # サーバーにテーブルを読み直させる
go run ./cmd/solctl tables
go run ./cmd/solctl post todos '{"title":"a"}'
go run ./cmd/solctl get "todos?id=eq.1"
```

## REST API（PostgREST 相当）
| 操作 | 例 |
|---|---|
| 一覧 | `GET /rest/v1/todos?select=id,title&order=id.desc&limit=10` |
| 絞り込み | `?id=eq.1`（eq neq gt gte lt lte like、like は `*` がワイルドカード） |
| 追加 | `POST /rest/v1/todos` + JSON |
| 更新 | `PATCH /rest/v1/todos?id=eq.1` + JSON（絞り込み必須） |
| 削除 | `DELETE /rest/v1/todos?id=eq.1`（絞り込み必須） |

## 認証（GoTrue 相当）
| 操作 | 例 |
|---|---|
| 登録 | `POST /auth/v1/signup` + `{"email":"a@example.com","password":"pass1234"}` |
| ログイン | `POST /auth/v1/token?grant_type=password` + 同じ JSON → `access_token`（HS256、1時間） |
| 自分 | `GET /auth/v1/user` + `Authorization: Bearer <access_token>` |

`JWT_SECRET` は32文字以上。例：`openssl rand -base64 48` で作り、`.env` にだけ書く。

## RLS（行レベルセキュリティ）
`/rest/v1` は、トークンが無ければ `anon`、あれば JWT の `role`（`authenticated`）として Postgres に入る。ポリシーで `auth.uid()`（JWT の `sub`）を使うと「自分の行だけ」にできる。例は [rls.md](docs/how-supabase-does-it/rls.md)。

## Realtime（変更の通知）
`SELECT realtime.enable('todos');` したテーブルの INSERT・UPDATE・DELETE が、WebSocket `/realtime/v1?token=<JWT>` に届く。送るのは `{"type":"subscribe","table":"todos"}`。RLS で見えない行の変更は届かない。`solctl listen todos` で試せる。仕組みは [realtime.md](docs/how-supabase-does-it/realtime.md)。

## テスト
```sh
DATABASE_URL=postgres://solbase:solbase@localhost:5432/solbase go test ./...
./scripts/demo.sh               # 実サーバーに curl で一通り（REST）
./scripts/auth-demo.sh          # 登録→ログイン→トークン確認→DBのハッシュ確認
./scripts/rls-demo.sh           # A・B の2ユーザーで、自分の行だけ見える・触れるか
./scripts/realtime-demo.sh      # A・B が購読し、A の変更は A にだけ届くか
```

仕組みの説明：[docs/how-supabase-does-it](docs/how-supabase-does-it/)
