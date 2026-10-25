package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// MinSecretLen は JWT_SECRET の最短の長さ（Supabase も 32 文字以上を求める）。
const MinSecretLen = 32

// Handler は /auth/v1/ を受ける。
type Handler struct {
	DB     *pgxpool.Pool
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

// New は秘密鍵の長さを確かめて Handler を作る。
func New(db *pgxpool.Pool, secret string) (*Handler, error) {
	if len(secret) < MinSecretLen {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}
	return &Handler{DB: db, Secret: []byte(secret), TTL: time.Hour, Now: time.Now}, nil
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// User は返すときのユーザー。パスワードのハッシュは入れない。
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type session struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	ExpiresAt   int64  `json:"expires_at"`
	User        User   `json:"user"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/signup":
		h.signup(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/token":
		if r.URL.Query().Get("grant_type") != "password" {
			writeErr(w, 400, "unsupported_grant_type", "only grant_type=password is supported")
			return
		}
		h.token(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/auth/v1/user":
		h.Require(http.HandlerFunc(h.user)).ServeHTTP(w, r)
	default:
		writeErr(w, 404, "not_found", "no such endpoint")
	}
}

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "server_error", "could not hash password")
		return
	}
	var u User
	err = h.DB.QueryRow(r.Context(),
		`INSERT INTO auth.users (email, encrypted_password) VALUES ($1, $2) RETURNING id::text, email`,
		c.Email, string(hash)).Scan(&u.ID, &u.Email)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		writeErr(w, 422, "user_already_exists", "user already registered")
		return
	}
	if err != nil {
		writeErr(w, 500, "server_error", "could not create user")
		return
	}
	h.writeSession(w, u)
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	var u User
	var hash string
	err := h.DB.QueryRow(r.Context(),
		`SELECT id::text, email, encrypted_password FROM auth.users WHERE lower(email) = lower($1)`,
		c.Email).Scan(&u.ID, &u.Email, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		// 無いユーザーでも bcrypt を1回回し、時間の差でメールの有無が分からないようにする
		bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password))
		writeErr(w, 400, "invalid_grant", "invalid login credentials")
		return
	}
	if err != nil {
		writeErr(w, 500, "server_error", "could not read user")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil {
		writeErr(w, 400, "invalid_grant", "invalid login credentials")
		return
	}
	h.writeSession(w, u)
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("solbase-dummy-password"), bcrypt.DefaultCost)

func (h *Handler) user(w http.ResponseWriter, r *http.Request) {
	c, _ := ClaimsFrom(r.Context())
	writeJSON(w, 200, User{ID: c.Sub, Email: c.Email, Role: c.Role})
}

func (h *Handler) writeSession(w http.ResponseWriter, u User) {
	now := h.Now()
	u.Role = "authenticated"
	exp := now.Add(h.TTL).Unix()
	tok, err := Sign(Claims{Sub: u.ID, Role: u.Role, Email: u.Email, Iat: now.Unix(), Exp: exp}, h.Secret)
	if err != nil {
		writeErr(w, 500, "server_error", "could not sign token")
		return
	}
	writeJSON(w, 200, session{AccessToken: tok, TokenType: "bearer", ExpiresIn: int(h.TTL.Seconds()), ExpiresAt: exp, User: u})
}

type ctxKey struct{}

// Require は Authorization: Bearer <token> を確かめ、通ったら claims を context に入れて次へ渡す。
func (h *Handler) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			writeErr(w, 401, "no_authorization", "missing bearer token")
			return
		}
		c, err := Verify(tok, h.Secret, h.Now())
		if err != nil {
			writeErr(w, 401, "bad_jwt", err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
	})
}

// RequestRole は REST 用に、リクエストの JWT から Postgres のロール名と claims の JSON を返す。
// トークンが無ければ anon（claims は空）。トークンがあって不正なら error（401 にする）。
// ロールは anon / authenticated だけ通す（JWT の role をそのまま SET ROLE に使わない）。
func (h *Handler) RequestRole(r *http.Request) (role, claims string, err error) {
	hdr := r.Header.Get("Authorization")
	if hdr == "" {
		return "anon", "", nil
	}
	tok, ok := strings.CutPrefix(hdr, "Bearer ")
	if !ok || tok == "" {
		return "", "", errors.New("authorization header must be Bearer <token>")
	}
	c, err := Verify(tok, h.Secret, h.Now())
	if err != nil {
		return "", "", err
	}
	if c.Role != "authenticated" && c.Role != "anon" {
		return "", "", errors.New("role is not allowed")
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", "", err
	}
	return c.Role, string(b), nil
}

// ClaimsFrom は Require が入れた claims を取り出す。
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

func readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var c credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&c); err != nil {
		writeErr(w, 400, "bad_json", "body must be JSON with email and password")
		return c, false
	}
	c.Email = strings.TrimSpace(c.Email)
	if a, err := mail.ParseAddress(c.Email); err != nil || a.Address != c.Email {
		writeErr(w, 400, "validation_failed", "email is invalid")
		return c, false
	}
	// bcrypt は 72 バイトより後ろを読まないので、長すぎるものは断る
	if len(c.Password) < 6 || len(c.Password) > 72 {
		writeErr(w, 400, "weak_password", "password must be 6 to 72 bytes")
		return c, false
	}
	return c, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": msg})
}
