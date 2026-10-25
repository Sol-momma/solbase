// Package auth は GoTrue（Supabase Auth）相当の最小版：メール＋パスワードのユーザー登録・ログインと、HS256 の JWT。
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Claims は access token の中身。名前は Supabase の JWT と同じにしてある。
type Claims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	Email string `json:"email"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

var (
	ErrMalformed = errors.New("token is malformed")
	ErrAlg       = errors.New("token alg must be HS256")
	ErrSignature = errors.New("token signature is invalid")
	ErrExpired   = errors.New("token is expired")
)

var b64 = base64.RawURLEncoding

// header は固定。alg を外から選ばせない（"none" などで署名を飛ばされないように）。
var header = b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// Sign は claims を HS256 で署名して "header.payload.signature" を返す。
func Sign(c Claims, secret []byte) (string, error) {
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	unsigned := header + "." + b64.EncodeToString(p)
	return unsigned + "." + b64.EncodeToString(mac(unsigned, secret)), nil
}

// Verify は署名と期限を確かめて claims を返す。now は期限の判定に使う時刻。
func Verify(token string, secret []byte, now time.Time) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, ErrMalformed
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return c, ErrMalformed
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hb, &h) != nil {
		return c, ErrMalformed
	}
	if h.Alg != "HS256" {
		return c, ErrAlg
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return c, ErrMalformed
	}
	// 比べるときは時間が一定になる hmac.Equal を使う
	if !hmac.Equal(sig, mac(parts[0]+"."+parts[1], secret)) {
		return c, ErrSignature
	}
	pb, err := b64.DecodeString(parts[1])
	if err != nil || json.Unmarshal(pb, &c) != nil {
		return c, ErrMalformed
	}
	if c.Exp == 0 || now.Unix() >= c.Exp {
		return c, ErrExpired
	}
	return c, nil
}

func mac(s string, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(s))
	return m.Sum(nil)
}
