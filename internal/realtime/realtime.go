// Package realtime は Supabase Realtime（postgres_changes）相当の最小版。
// テーブルのトリガーが pg_notify で変更を流し、サーバーが LISTEN で受けて、WebSocket の購読者に配る。
// 配る前に、購読者のロールと JWT で「その行が見えるか」を Postgres に聞く（RLS をそのまま使う）。
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel は pg_notify のチャンネル名。
const Channel = "solbase_realtime"

// Migrate は realtime スキーマ・トリガー関数・realtime.enable() を作る。何度流しても同じ。
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242002)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS realtime;

		-- 行が変わるたびに、種類・テーブル・新しい行・古い行を JSON で通知する
		CREATE OR REPLACE FUNCTION realtime.notify_change() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE payload text;
		BEGIN
			payload := json_build_object(
				'type', TG_OP, 'table', TG_TABLE_NAME,
				'record', CASE WHEN TG_OP = 'DELETE' THEN NULL ELSE to_jsonb(NEW) END,
				'old_record', CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE to_jsonb(OLD) END)::text;
			-- NOTIFY は 8000 バイトまで。超えたら中身なしで知らせる（サーバー側で捨てる）
			IF octet_length(payload) > 7900 THEN
				payload := json_build_object('type', TG_OP, 'table', TG_TABLE_NAME, 'too_large', true)::text;
			END IF;
			PERFORM pg_notify('`+Channel+`', payload);
			RETURN NULL;
		END $$;

		-- SELECT realtime.enable('todos') で、そのテーブルの変更を流すようにする
		-- （本物の ALTER PUBLICATION supabase_realtime ADD TABLE の代わり）
		CREATE OR REPLACE FUNCTION realtime.enable(tbl regclass) RETURNS void LANGUAGE plpgsql AS $$
		BEGIN
			EXECUTE format('DROP TRIGGER IF EXISTS solbase_realtime ON %s', tbl);
			EXECUTE format('CREATE TRIGGER solbase_realtime AFTER INSERT OR UPDATE OR DELETE ON %s
				FOR EACH ROW EXECUTE FUNCTION realtime.notify_change()', tbl);
		END $$;
		REVOKE ALL ON FUNCTION realtime.enable(regclass) FROM PUBLIC;

		-- REST から anon / authenticated で書いたときもトリガーが動けるように
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'anon') THEN CREATE ROLE anon NOLOGIN; END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN CREATE ROLE authenticated NOLOGIN; END IF;
		END $$;
		GRANT USAGE ON SCHEMA realtime TO anon, authenticated;
		GRANT EXECUTE ON FUNCTION realtime.notify_change() TO anon, authenticated;`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Event はトリガーが送ってくる1件の変更。
type Event struct {
	Type      string          `json:"type"`
	Table     string          `json:"table"`
	Record    json.RawMessage `json:"record,omitempty"`
	OldRecord json.RawMessage `json:"old_record,omitempty"`
	TooLarge  bool            `json:"too_large,omitempty"`
}

type client struct {
	role, claims string
	mu           sync.Mutex
	tables       map[string]bool
	send         chan []byte
}

func (c *client) subscribed(t string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tables[t]
}

// Server は /realtime/v1 の WebSocket を受け、変更を配る。
type Server struct {
	DB *pgxpool.Pool
	// TokenRole はトークンから Postgres のロールと claims の JSON を返す（auth.Handler.TokenRole）。
	TokenRole func(token string) (role, claims string, err error)

	mu      sync.Mutex
	clients map[*client]struct{}
}

// New は Server を作る。Run を別の goroutine で回すこと。
func New(db *pgxpool.Pool, tokenRole func(string) (string, string, error)) *Server {
	return &Server{DB: db, TokenRole: tokenRole, clients: map[*client]struct{}{}}
}

// Clients は今つながっている数（テストでリークを見る用）。
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Run は専用の接続で LISTEN し、通知が来るたびに配る。ctx が終わるまで戻らない。
// 接続が切れたら1秒おいてつなぎ直す（その間の変更は取りこぼす。NOTIFY の弱点）。
func (s *Server) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.listen(ctx); err != nil && ctx.Err() == nil {
			log.Printf("realtime: listen: %v (retrying)", err)
			time.Sleep(time.Second)
		}
	}
}

func (s *Server) listen(ctx context.Context) error {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			// プールに戻す前に LISTEN を外す（戻した接続に通知が溜まらないように）
			conn.Exec(context.Background(), "UNLISTEN *")
			return err
		}
		var ev Event
		if json.Unmarshal([]byte(n.Payload), &ev) != nil || ev.TooLarge {
			continue
		}
		s.dispatch(ctx, ev)
	}
}

func (s *Server) dispatch(ctx context.Context, ev Event) {
	s.mu.Lock()
	var targets []*client
	for c := range s.clients {
		if c.subscribed(ev.Table) {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	msg, _ := json.Marshal(ev)
	for _, c := range targets {
		ok, err := s.visible(ctx, c, ev)
		if err != nil {
			log.Printf("realtime: visibility check %s: %v", ev.Table, err)
		}
		if !ok {
			continue
		}
		select {
		case c.send <- msg:
		default: // 受け取りが遅すぎる接続には送らない（サーバーを止めないため）
		}
	}
}

// visible は「購読者のロールと JWT で、この変更の行が見えるか」を Postgres に聞く。
// INSERT・UPDATE は今の行を、そのロールで主キーで探す。DELETE は行がもう無いので、
// トランザクションの中で古い行を入れ直してから探し、最後に必ずロールバックする（外からは見えない）。
func (s *Server) visible(ctx context.Context, c *client, ev Event) (bool, error) {
	row := ev.Record
	if ev.Type == "DELETE" {
		row = ev.OldRecord
	}
	if len(row) == 0 {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	table := pgx.Identifier{"public", ev.Table}.Sanitize()
	pk, err := primaryKey(ctx, tx, ev.Table)
	if err != nil {
		return false, err
	}
	if len(pk) == 0 {
		return false, errors.New("table has no primary key")
	}
	if ev.Type == "DELETE" {
		if _, err := tx.Exec(ctx, "INSERT INTO "+table+" SELECT * FROM jsonb_populate_record(NULL::"+table+", $1)", string(row)); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('role', $1, true), set_config('request.jwt.claims', $2, true)`, c.role, c.claims); err != nil {
		return false, err
	}
	var conds []string
	for _, col := range pk {
		conds = append(conds, fmt.Sprintf("%s::text = ($1::jsonb ->> %s)", pgx.Identifier{col}.Sanitize(), quote(col)))
	}
	var ok bool
	err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" WHERE "+strings.Join(conds, " AND ")+")", string(row)).Scan(&ok)
	return ok, err
}

func primaryKey(ctx context.Context, tx pgx.Tx, table string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT a.attname FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		WHERE i.indrelid = to_regclass($1) AND i.indisprimary`, pgx.Identifier{"public", table}.Sanitize())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

type inMsg struct {
	Type  string `json:"type"`
	Table string `json:"table"`
}

// ServeHTTP は GET /realtime/v1?token=<jwt> を WebSocket にする。トークンが無ければ anon、不正なら 401。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	role, claims := "anon", ""
	if tok := r.URL.Query().Get("token"); tok != "" {
		var err error
		if role, claims, err = s.TokenRole(tok); err != nil {
			http.Error(w, `{"error":"bad_jwt"}`, http.StatusUnauthorized)
			return
		}
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c := &client{role: role, claims: claims, tables: map[string]bool{}, send: make(chan []byte, 64)}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		ws.CloseNow()
	}()

	// 書く係：send に来たものを順に送る
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-c.send:
				wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
				err := ws.Write(wctx, websocket.MessageText, m)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// 読む係：購読の申し込みを受ける。切れたら抜けて後片付け
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m inMsg
		if json.Unmarshal(data, &m) != nil || m.Type != "subscribe" || m.Table == "" {
			c.reply(`{"type":"error","message":"send {\"type\":\"subscribe\",\"table\":\"<name>\"}"}`)
			continue
		}
		var exists bool
		s.DB.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, pgx.Identifier{"public", m.Table}.Sanitize()).Scan(&exists)
		if !exists {
			b, _ := json.Marshal(map[string]string{"type": "error", "message": "table not found: " + m.Table})
			c.reply(string(b))
			continue
		}
		c.mu.Lock()
		c.tables[m.Table] = true
		c.mu.Unlock()
		b, _ := json.Marshal(map[string]string{"type": "subscribed", "table": m.Table})
		c.reply(string(b))
	}
}

func (c *client) reply(s string) {
	select {
	case c.send <- []byte(s):
	default:
	}
}
