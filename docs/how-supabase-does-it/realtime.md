# Realtime（本物は supabase/realtime の Postgres Changes）

## 本物の Supabase
- `supabase.channel(...).on('postgres_changes', { event: '*', table: 'todos' }, cb)` で、テーブルの INSERT・UPDATE・DELETE が WebSocket で届く（[Postgres Changes のドキュメント](https://supabase.com/docs/guides/realtime/postgres-changes)）。流すテーブルは `ALTER PUBLICATION supabase_realtime ADD TABLE todos` で選ぶ。
- サーバーは Elixir 製の [supabase/realtime](https://github.com/supabase/realtime)。Postgres の論理レプリケーション（WAL）を読み、変更を拾う。
- 誰に届けるかは [walrus](https://github.com/supabase/walrus)（Write Ahead Log Realtime Unified Security）が決める。購読者ごとにロールと JWT の claims を入れて、その行が RLS で見えるかを Postgres に聞き、見える人にだけ送る。
- DELETE は行がもう無いので RLS で確かめられない。本物は主キー（replica identity）だけを送る。

## solbase（この版）
- `internal/realtime/realtime.go` の `Migrate`：`realtime.notify_change()`（トリガー関数）と `realtime.enable('todos')` を作る。トリガーは変更を JSON にして `pg_notify('solbase_realtime', …)` する（[NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)）。WAL の代わり。
- `Server.Run`：プールから1本だけ取って `LISTEN` し、通知を待つ。
- `Server.visible`：walrus の代わり。購読者ごとに1トランザクションを開き、`set_config('role', …, true)` と `set_config('request.jwt.claims', …, true)` をしてから、主キーで行を探す。見つかれば送る。
  - DELETE は、トランザクションの中で古い行を入れ直してから同じように探し、必ずロールバックする。だから本物と違って、見える人には古い行の中身も送れる。
- `Server.ServeHTTP`：`GET /realtime/v1?token=<JWT>`。トークンが無ければ anon。壊れた・期限切れなら WebSocket にせず 401。`{"type":"subscribe","table":"todos"}` で購読する。切れたら購読者の一覧から消す。
- `internal/auth/handler.go` の `TokenRole`：REST と同じ判定で、トークンからロールと claims を出す。
- テスト `internal/realtime/realtime_test.go`：
  - A の INSERT が1秒以内に A に届く。UPDATE・DELETE も種類つきで届く。
  - B と anon には届かない。
  - 購読していないテーブルは届かない。壊れたトークンは 401。
  - 5本つないで切ると、購読者の数が0に戻る。
  - 見える判定を外すとテストが落ちることも確かめた。

## 本物との違い（わざと削ったところ）
- NOTIFY は8000バイトまで。超える行の変更は捨てる（本物は WAL なので大きさの制限がない）。
- LISTEN の接続が切れている間の変更は取りこぼす（1秒後につなぎ直す）。
- 購読者 × 変更の数だけ DB に聞くので、人数が多いと重い。本物も同じ問題があり、ドキュメントに注意が書いてある。
- つないだ後にトークンの期限が切れても切断しない。フィルタ（`filter: 'id=eq.1'`）、Broadcast、Presence は作っていない。

## 例
```sql
SELECT realtime.enable('todos');
```
```sh
SOLBASE_TOKEN=<A のトークン> solctl listen todos 30
./scripts/realtime-demo.sh
```
