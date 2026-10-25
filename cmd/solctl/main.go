// solctl は solbase を外から操作・確認するためのコマンド（AI が自分で確かめる用）。
//
//	solctl sql "<SQL>"           DB に直接 SQL を流す
//	solctl tables                public のテーブルと列を表示
//	solctl reload                サーバーにカタログを読み直させる
//	solctl get|post|patch|delete <path> [json]   API を呼ぶ（例: get "todos?id=eq.1"）
//	solctl listen <table> [seconds]  Realtime で変更を受けて1行ずつ表示（SOLBASE_TOKEN があればそのユーザーで）
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sol-momma/solbase/internal/rest"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: solctl sql|tables|reload|get|post|patch|delete ...")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func run(cmd string, args []string) error {
	ctx := context.Background()
	switch cmd {
	case "sql", "tables":
		db, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://solbase:solbase@localhost:5432/solbase"))
		if err != nil {
			return err
		}
		defer db.Close()
		if cmd == "tables" {
			cat, err := rest.LoadCatalog(ctx, db)
			if err != nil {
				return err
			}
			for t, cols := range cat {
				var cs []string
				for c := range cols {
					cs = append(cs, c)
				}
				fmt.Printf("%s: %s\n", t, strings.Join(cs, ", "))
			}
			return nil
		}
		if len(args) != 1 {
			return fmt.Errorf("usage: solctl sql \"<SQL>\"")
		}
		_, err = db.Exec(ctx, args[0])
		if err == nil {
			fmt.Println("ok")
		}
		return err
	case "listen":
		if len(args) < 1 {
			return fmt.Errorf("usage: solctl listen <table> [seconds]")
		}
		secs := 10
		if len(args) > 1 {
			secs, _ = strconv.Atoi(args[1])
		}
		return listen(ctx, args[0], time.Duration(secs)*time.Second)
	case "reload":
		return call("POST", "_reload", "")
	case "get", "post", "patch", "delete":
		if len(args) < 1 {
			return fmt.Errorf("usage: solctl %s <path> [json]", cmd)
		}
		b := ""
		if len(args) > 1 {
			b = args[1]
		}
		return call(strings.ToUpper(cmd), args[0], b)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func call(method, path, body string) error {
	req, err := http.NewRequest(method, env("SOLBASE_URL", "http://localhost:8080")+"/rest/v1/"+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	fmt.Printf("%d %s", res.StatusCode, out)
	if res.StatusCode >= 400 {
		os.Exit(1)
	}
	return nil
}

func listen(ctx context.Context, table string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	u := "ws" + strings.TrimPrefix(env("SOLBASE_URL", "http://localhost:8080"), "http") + "/realtime/v1"
	if t := os.Getenv("SOLBASE_TOKEN"); t != "" {
		u += "?token=" + url.QueryEscape(t)
	}
	ws, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"subscribe","table":"`+table+`"}`)); err != nil {
		return err
	}
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			return nil // 時間切れで終わる
		}
		fmt.Println(string(b))
	}
}
