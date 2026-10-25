package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-secret-test-secret-test-secret!")

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok, err := Sign(Claims{Sub: "u1", Role: "authenticated", Email: "a@example.com", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(tok, secret, now)
	if err != nil || c.Sub != "u1" || c.Role != "authenticated" || c.Email != "a@example.com" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	// 期限ちょうどから先は拒否
	if _, err := Verify(tok, secret, now.Add(time.Hour)); err != ErrExpired {
		t.Fatalf("expired: %v", err)
	}
	// 別の鍵では拒否
	if _, err := Verify(tok, []byte("other-secret-other-secret-other!!"), now); err != ErrSignature {
		t.Fatalf("other key: %v", err)
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tok, _ := Sign(Claims{Sub: "u1", Role: "authenticated", Exp: now.Add(time.Hour).Unix()}, secret)
	parts := strings.Split(tok, ".")
	// role を service_role に書き換えた payload（署名はそのまま）
	evil := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u1","role":"service_role","exp":` + "1800003600" + `}`))
	if _, err := Verify(parts[0]+"."+evil+"."+parts[2], secret, now); err != ErrSignature {
		t.Fatalf("tampered payload: %v", err)
	}
	// alg=none で署名なし
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	if _, err := Verify(none+"."+parts[1]+".", secret, now); err != ErrAlg {
		t.Fatalf("alg none: %v", err)
	}
	for _, bad := range []string{"", "a.b", "a.b.c.d", "!!.!!.!!"} {
		if _, err := Verify(bad, secret, now); err != ErrMalformed {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	// exp が無いトークンは期限切れ扱い
	noExp, _ := Sign(Claims{Sub: "u1"}, secret)
	if _, err := Verify(noExp, secret, now); err != ErrExpired {
		t.Fatalf("no exp: %v", err)
	}
}
