package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler は /rest/v1/<table> を受ける。
type Handler struct {
	DB  *pgxpool.Pool
	mu  sync.RWMutex
	cat Catalog
}

// New はカタログを読んで Handler を作る。
func New(ctx context.Context, db *pgxpool.Pool) (*Handler, error) {
	h := &Handler{DB: db}
	return h, h.Reload(ctx)
}

// Reload はカタログを読み直す（PostgREST の NOTIFY pgrst の代わり）。
func (h *Handler) Reload(ctx context.Context) error {
	cat, err := LoadCatalog(ctx, h.DB)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.cat = cat
	h.mu.Unlock()
	return nil
}

type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func bad(status int, f string, a ...any) error { return &apiError{status, fmt.Sprintf(f, a...)} }

var ops = map[string]string{"eq": "=", "neq": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=", "like": "LIKE"}

func ident(s string) string { return pgx.Identifier{s}.Sanitize() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/rest/v1/_reload" {
		if err := h.Reload(r.Context()); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 解釈できないクエリ（生の ; など）は黙って捨てずに 400 にする
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		writeErr(w, bad(400, "bad query: %v", err))
		return
	}
	table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
	h.mu.RLock()
	cols, ok := h.cat[table]
	h.mu.RUnlock()
	if !ok || table == "" || strings.Contains(table, "/") {
		writeErr(w, bad(404, "table %q not found", table))
		return
	}
	var (
		rows []map[string]any
		err  error
	)
	switch r.Method {
	case http.MethodGet:
		rows, err = h.list(r, table, cols)
	case http.MethodPost:
		rows, err = h.insert(r, table, cols)
	case http.MethodPatch:
		rows, err = h.update(r, table, cols)
	case http.MethodDelete:
		rows, err = h.delete(r, table, cols)
	default:
		err = bad(405, "method not allowed")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		w.WriteHeader(http.StatusCreated)
	}
	json.NewEncoder(w).Encode(rows)
}

func writeErr(w http.ResponseWriter, err error) {
	status, msg := 500, err.Error()
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.status
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// where は ?col=op.val を WHERE 句にする。値は必ずプレースホルダ。
func where(r *http.Request, cols map[string]string, args *[]any) (string, error) {
	var conds []string
	q := r.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "select" || k == "order" || k == "limit" || k == "offset" {
			continue
		}
		if !has(cols, k) {
			return "", bad(400, "column %q not found", k)
		}
		for _, v := range q[k] {
			op, val, ok := strings.Cut(v, ".")
			sqlOp, known := ops[op]
			if !ok || !known {
				return "", bad(400, "bad filter %s=%s (use eq|neq|gt|gte|lt|lte|like)", k, v)
			}
			if op == "like" {
				*args = append(*args, strings.ReplaceAll(val, "*", "%"))
				conds = append(conds, fmt.Sprintf("%s::text LIKE $%d", ident(k), len(*args)))
				continue
			}
			// 値は文字列で渡し、列の型に変換して比べる（数値の大小が正しくなる）
			*args = append(*args, val)
			conds = append(conds, fmt.Sprintf("%s %s $%d::text::%s", ident(k), sqlOp, len(*args), ident(cols[k])))
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

func selectList(r *http.Request, cols map[string]string) (string, error) {
	s := r.URL.Query().Get("select")
	if s == "" || s == "*" {
		return "*", nil
	}
	var out []string
	for _, c := range strings.Split(s, ",") {
		if !has(cols, c) {
			return "", bad(400, "column %q not found", c)
		}
		out = append(out, ident(c))
	}
	return strings.Join(out, ", "), nil
}

func (h *Handler) list(r *http.Request, table string, cols map[string]string) ([]map[string]any, error) {
	var args []any
	sel, err := selectList(r, cols)
	if err != nil {
		return nil, err
	}
	w, err := where(r, cols, &args)
	if err != nil {
		return nil, err
	}
	sql := "SELECT " + sel + " FROM " + ident(table) + w
	q := r.URL.Query()
	if o := q.Get("order"); o != "" {
		var parts []string
		for _, p := range strings.Split(o, ",") {
			c, dir, _ := strings.Cut(p, ".")
			if !has(cols, c) {
				return nil, bad(400, "column %q not found", c)
			}
			d := "ASC"
			switch dir {
			case "", "asc":
			case "desc":
				d = "DESC"
			default:
				return nil, bad(400, "bad order %q", p)
			}
			parts = append(parts, ident(c)+" "+d)
		}
		sql += " ORDER BY " + strings.Join(parts, ", ")
	}
	for _, k := range []string{"limit", "offset"} {
		if v := q.Get(k); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, bad(400, "bad %s %q", k, v)
			}
			sql += fmt.Sprintf(" %s %d", strings.ToUpper(k), n)
		}
	}
	return h.query(r.Context(), sql, args)
}

func body(r *http.Request, cols map[string]string) (map[string]any, []string, error) {
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil || len(m) == 0 {
		return nil, nil, bad(400, "body must be a non-empty JSON object")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !has(cols, k) {
			return nil, nil, bad(400, "column %q not found", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return m, keys, nil
}

func (h *Handler) insert(r *http.Request, table string, cols map[string]string) ([]map[string]any, error) {
	m, keys, err := body(r, cols)
	if err != nil {
		return nil, err
	}
	var names, ph []string
	var args []any
	for i, k := range keys {
		names = append(names, ident(k))
		ph = append(ph, fmt.Sprintf("$%d", i+1))
		args = append(args, m[k])
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", ident(table), strings.Join(names, ", "), strings.Join(ph, ", "))
	return h.query(r.Context(), sql, args)
}

func (h *Handler) update(r *http.Request, table string, cols map[string]string) ([]map[string]any, error) {
	m, keys, err := body(r, cols)
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for _, k := range keys {
		args = append(args, m[k])
		sets = append(sets, fmt.Sprintf("%s = $%d", ident(k), len(args)))
	}
	w, err := where(r, cols, &args)
	if err != nil {
		return nil, err
	}
	if w == "" {
		return nil, bad(400, "PATCH needs a filter (e.g. ?id=eq.1)")
	}
	return h.query(r.Context(), "UPDATE "+ident(table)+" SET "+strings.Join(sets, ", ")+w+" RETURNING *", args)
}

func (h *Handler) delete(r *http.Request, table string, cols map[string]string) ([]map[string]any, error) {
	var args []any
	w, err := where(r, cols, &args)
	if err != nil {
		return nil, err
	}
	if w == "" {
		return nil, bad(400, "DELETE needs a filter (e.g. ?id=eq.1)")
	}
	return h.query(r.Context(), "DELETE FROM "+ident(table)+w+" RETURNING *", args)
}

func (h *Handler) query(ctx context.Context, sql string, args []any) ([]map[string]any, error) {
	rows, err := h.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, bad(400, "%v", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, bad(400, "%v", err)
	}
	return out, nil
}

func has(cols map[string]string, c string) bool { _, ok := cols[c]; return ok }
