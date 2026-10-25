#!/usr/bin/env bash
# 実際の Postgres と solbase サーバーに対して、登録・ログイン・トークン確認を curl で一通り試す。
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://solbase:solbase@localhost:5432/solbase}
export ADDR=${ADDR:-:8080}
# デモ用にその場で作る使い捨ての鍵（表示しない）
export JWT_SECRET=${JWT_SECRET:-$(openssl rand -base64 48)}
URL=http://localhost${ADDR}
go build -o /tmp/solbase ./cmd/solbase && go build -o /tmp/solctl ./cmd/solctl
run() { echo "\$ $*"; "$@"; echo; sleep 0.5; }
/tmp/solbase & PID=$!; trap 'kill $PID' EXIT; sleep 1
/tmp/solctl sql "DELETE FROM auth.users WHERE email = 'demo@example.com'" >/dev/null
CRED='{"email":"demo@example.com","password":"pass1234"}'
run curl -s -X POST -d "$CRED" $URL/auth/v1/signup -o /dev/null -w 'signup %{http_code}\n'
run curl -s -X POST -d "$CRED" $URL/auth/v1/signup
TOKEN=$(curl -s -X POST -d "$CRED" "$URL/auth/v1/token?grant_type=password" | sed -E 's/.*"access_token":"([^"]+)".*/\1/')
echo "\$ ログイン → access_token の中身（payload）"
echo "$TOKEN" | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null; echo; echo
echo '$ curl -s -H "Authorization: Bearer <access_token>" '$URL/auth/v1/user
curl -s -H "Authorization: Bearer $TOKEN" $URL/auth/v1/user; echo
run curl -s -w ' %{http_code}\n' -X POST -d '{"email":"demo@example.com","password":"wrong999"}' "$URL/auth/v1/token?grant_type=password"
echo '$ curl -s -H "Authorization: Bearer <access_token の最後を書き換えたもの>" '$URL/auth/v1/user
curl -s -w ' %{http_code}\n' -H "Authorization: Bearer ${TOKEN%??}xx" $URL/auth/v1/user; echo
# DB を直接見て、パスワードが平文でなく bcrypt のハッシュ（$2a$…）で入っているか確かめる
PSQL=${PSQL:-docker compose exec -T db psql -U solbase -d solbase}
run $PSQL -c "SELECT email, left(encrypted_password, 7) || '…' AS encrypted_password, encrypted_password = 'pass1234' AS is_plaintext FROM auth.users WHERE email = 'demo@example.com'"
