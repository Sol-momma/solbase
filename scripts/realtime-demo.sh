#!/usr/bin/env bash
# A・B が todos を購読し、A の INSERT・UPDATE・DELETE が A にだけ届くことを確かめる。
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://solbase:solbase@localhost:5432/solbase}
export ADDR=${ADDR:-:8080}
export JWT_SECRET=${JWT_SECRET:-$(openssl rand -base64 48)} # その場で作る使い捨ての鍵（表示しない）
URL=http://localhost${ADDR}
export SOLBASE_URL=$URL
go build -o /tmp/solbase ./cmd/solbase && go build -o /tmp/solctl ./cmd/solctl
/tmp/solbase & PID=$!; trap 'kill $PID 2>/dev/null' EXIT; sleep 1
/tmp/solctl sql "DELETE FROM auth.users WHERE email IN ('a@example.com','b@example.com');
  DROP TABLE IF EXISTS todos;
  CREATE TABLE todos (id serial PRIMARY KEY, user_id uuid NOT NULL DEFAULT auth.uid(), title text NOT NULL);
  ALTER TABLE todos ENABLE ROW LEVEL SECURITY;
  CREATE POLICY own_rows ON todos FOR ALL TO authenticated USING (user_id = auth.uid()) WITH CHECK (user_id = auth.uid());
  SELECT realtime.enable('todos');" >/dev/null
/tmp/solctl reload >/dev/null
tok() { curl -s -X POST -d "{\"email\":\"$1\",\"password\":\"pass1234\"}" $URL/auth/v1/signup | sed -E 's/.*"access_token":"([^"]+)".*/\1/'; }
A=$(tok a@example.com); B=$(tok b@example.com)
OUT=$(mktemp -d)
SOLBASE_TOKEN=$A /tmp/solctl listen todos 4 >"$OUT/a" & LA=$!
SOLBASE_TOKEN=$B /tmp/solctl listen todos 4 >"$OUT/b" & LB=$!
sleep 0.5
H=(-H "Authorization: Bearer $A")
echo '$ [A] POST / PATCH / DELETE /rest/v1/todos'
curl -s -o /dev/null "${H[@]}" -X POST -d '{"title":"A のメモ"}' $URL/rest/v1/todos
curl -s -o /dev/null "${H[@]}" -X PATCH -d '{"title":"A が書き換え"}' "$URL/rest/v1/todos?id=eq.1"
curl -s -o /dev/null "${H[@]}" -X DELETE "$URL/rest/v1/todos?id=eq.1"
wait $LA $LB
echo; echo '--- A が受け取ったもの'; cat "$OUT/a"
echo; echo '--- B が受け取ったもの（subscribed だけのはず）'; cat "$OUT/b"
echo; echo '$ 壊れたトークンで接続'; SOLBASE_TOKEN=garbage /tmp/solctl listen todos 1 || true
rm -rf "$OUT"
