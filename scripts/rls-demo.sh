#!/usr/bin/env bash
# A・B の2ユーザーで todos を触り、RLS で自分の行だけ見える・触れることを curl で確かめる。
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://solbase:solbase@localhost:5432/solbase}
export ADDR=${ADDR:-:8080}
export JWT_SECRET=${JWT_SECRET:-$(openssl rand -base64 48)} # その場で作る使い捨ての鍵（表示しない）
URL=http://localhost${ADDR}
export SOLBASE_URL=$URL
PSQL=${PSQL:-docker compose exec -T db psql -U solbase -d solbase}
go build -o /tmp/solbase ./cmd/solbase && go build -o /tmp/solctl ./cmd/solctl
/tmp/solbase & PID=$!; trap 'kill $PID' EXIT; sleep 1
/tmp/solctl sql "DELETE FROM auth.users WHERE email IN ('a@example.com','b@example.com');
  DROP TABLE IF EXISTS todos;
  CREATE TABLE todos (id serial PRIMARY KEY, user_id uuid NOT NULL DEFAULT auth.uid(), title text NOT NULL);
  ALTER TABLE todos ENABLE ROW LEVEL SECURITY;
  CREATE POLICY own_rows ON todos FOR ALL TO authenticated USING (user_id = auth.uid()) WITH CHECK (user_id = auth.uid());" >/dev/null
/tmp/solctl reload >/dev/null
tok() { curl -s -X POST -d "{\"email\":\"$1\",\"password\":\"pass1234\"}" $URL/auth/v1/signup | sed -E 's/.*"access_token":"([^"]+)".*/\1/'; }
A=$(tok a@example.com); B=$(tok b@example.com)
show() { echo "\$ [$1] curl -X $2 $URL$3 ${4:-}"; local t=""; [ "$1" = A ] && t=$A; [ "$1" = B ] && t=$B
  curl -s -w ' %{http_code}' -X $2 ${t:+-H "Authorization: Bearer $t"} ${4:+-d "$4"} "$URL$3"; echo; echo; sleep 0.3; }
show A POST /rest/v1/todos '{"title":"A のメモ"}'
show B POST /rest/v1/todos '{"title":"B のメモ"}'
show A GET "/rest/v1/todos?select=id,title"
show B GET "/rest/v1/todos?select=id,title"
show A PATCH "/rest/v1/todos?id=eq.2" '{"title":"A が書き換え"}'
show A DELETE "/rest/v1/todos?id=eq.2"
show anon GET /rest/v1/todos
echo '$ psql: DB の中身（RLS を飛ばすスーパーユーザーで見る）と、別の接続での request.jwt.claims'
$PSQL -c "SELECT id, title FROM todos ORDER BY id" -c "SELECT coalesce(current_setting('request.jwt.claims', true), '') = '' AS claims_empty"
