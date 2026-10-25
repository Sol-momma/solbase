# solbase

Supabase の仕組みを学ぶために Go で作っている小さな Supabase です（[日本語](README.ja.md)）。

A mini [Supabase](https://supabase.com/) written in Go to learn how Supabase works inside. It is for study, not for production use. The first part is a PostgREST-style REST API over a local Postgres.

![REST API demo](docs/media/rest-demo.gif)

## Features

- REST API compatible in spirit with PostgREST: `GET`, `POST`, `PATCH`, `DELETE` on `/rest/v1/<table>`
- `select`, `order`, `limit` and filters (`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like` with `*` as wildcard)
- `PATCH` and `DELETE` require a filter, so a whole table cannot be changed by mistake
- `solctl`, a small CLI to run SQL, reload the table list and call the API
- Notes comparing each part with the real Supabase: [docs/how-supabase-does-it](docs/how-supabase-does-it/)

Planned parts: Auth, row level security with JWT, and Realtime.

## Usage

Requires Go and Docker.

```sh
git clone https://github.com/Sol-momma/solbase.git
cd solbase
docker compose up -d                       # local Postgres
cp .env.example .env && set -a && . ./.env && set +a
go run ./cmd/solbase                       # REST API on :8080
```

Try it with `solctl`:

```sh
go run ./cmd/solctl sql "CREATE TABLE todos (id serial primary key, title text)"
go run ./cmd/solctl reload                 # make the server re-read tables
go run ./cmd/solctl post todos '{"title":"a"}'
go run ./cmd/solctl get "todos?id=eq.1"
```

Or with curl: `curl "localhost:8080/rest/v1/todos?select=id,title&order=id.desc&limit=10"`

## Tests

Tests run against a real Postgres, not mocks.

```sh
DATABASE_URL=postgres://solbase:solbase@localhost:5432/solbase go test ./...
./scripts/demo.sh                          # end-to-end curl run against the server
```

## Project structure

```text
cmd/solbase/   # API server
cmd/solctl/    # CLI
internal/rest/ # REST layer (query parsing, SQL building)
docs/          # how the real Supabase does each part
```

## Tech stack

- Go, PostgreSQL 17 (Docker Compose)

## License

[MIT](LICENSE)
