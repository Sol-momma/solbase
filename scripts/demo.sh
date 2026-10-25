#!/usr/bin/env bash
# 実際の Postgres と solbase サーバーに対して、solctl と curl で一通り試す。
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://solbase:solbase@localhost:5432/solbase}
export JWT_SECRET=${JWT_SECRET:-$(openssl rand -base64 48)} # 起動に要る。その場で作る使い捨ての鍵（表示しない）
go build -o /tmp/solbase ./cmd/solbase && go build -o /tmp/solctl ./cmd/solctl
run() { echo "\$ $*"; "$@"; echo; sleep 0.5; }
run /tmp/solctl sql "DROP TABLE IF EXISTS todos; CREATE TABLE todos (id serial primary key, title text not null, done boolean default false); INSERT INTO todos (title) VALUES ('buy milk'), ('learn postgrest');"
/tmp/solbase & PID=$!; trap 'kill $PID' EXIT; sleep 1
run /tmp/solctl tables
run curl -s localhost:8080/rest/v1/todos
run curl -s "localhost:8080/rest/v1/todos?id=eq.1"
run curl -s -X POST -H 'Content-Type: application/json' -d '{"title":"a"}' localhost:8080/rest/v1/todos
run curl -s -X PATCH -d '{"done":true}' "localhost:8080/rest/v1/todos?id=eq.1"
run curl -s "localhost:8080/rest/v1/todos?done=eq.false&order=id.desc&select=id,title"
run curl -s -X DELETE "localhost:8080/rest/v1/todos?id=eq.2"
run curl -s -w ' %{http_code}\n' localhost:8080/rest/v1/nope
run curl -s -w ' %{http_code}\n' "localhost:8080/rest/v1/todos?id=eq.1%3Bdrop%20table%20todos"
run curl -s localhost:8080/rest/v1/todos
