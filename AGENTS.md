# AGENTS.md

## 前提
- 言語：Go
- DB：Dockerで動かすローカルのPostgres（本物のPostgresで確かめる。モック禁止）
- 有料サービスは使わない。秘密（鍵・パスワード・トークン）はリポジトリに入れない（`.env` は `.gitignore`、例は `.env.example`）。

## 進め方（pstack）
1. 要件：Issue（`.github/ISSUE_TEMPLATE/dev-task.md` の形）を読み、合格条件を確かめる。
2. 計画は小さく：1つのPRに目的は1つ。
3. 自分で確かめる：テストとcurlをローカルのPostgresに対して実行する。
4. PR本文は `## Goal` / `## Verification` / `## Remaining` の3つ。
5. レビューを2回受けて直す。
6. worklog（作業記録）を残す。

## AIの確かめ方

新しいアプリや機能は、設計の段階で「AIが自分で動かして確かめられる作りか」を必ず決める。確かめ方は次のとおり。
- Webアプリ：CDP か Playwright
- iOSアプリ：シミュレーター
- どちらも使えないとき：操作用のコマンドやサイドカー（外から操作するための小さなプログラム）を作る

Issue の設計欄と合格条件には「AIの確かめ方」を必ず書く。

## 各パートで必ず書くもの
各パートのPRに `docs/how-supabase-does-it/<part>.md` を追加する。本物のSupabaseがどうしているかを短く説明し、本物のソース（GitHubのコードや公式ドキュメント）へのリンクを付ける。
- rest.md：PostgREST（https://github.com/PostgREST/postgrest）
- auth.md：GoTrue / Supabase Auth（https://github.com/supabase/auth）
- rls.md：RLS＋JWT（https://supabase.com/docs/guides/database/postgres/row-level-security）
- realtime.md：Realtime（https://github.com/supabase/realtime）

## PRの画面録画

- 画面がある変更には、実際に最初から最後まで操作して確かめた画面録画を必ず付ける（無理ならスクリーンショット）。画面がない変更（CLIなど）は、ターミナルの出力で代わりにする。必要なら図（mermaid）も付ける。
- 録画のやり方：
  1. Playwrightで録画をオンにして開く（`record_video_dir`、1280×720）
  2. 人と同じ速さでクリックして移る。見えないリンクはURLを直接開き、そう書いておく
  3. ffmpegでmp4にする。比べるときは左右に並べる（`hstack`）
  4. 表示用にGIFも作る（gh CLIで付けた動画はコメントの中で再生されないため）
  5. コマを何枚か書き出し、秘密が映っていないか目で確かめる
  6. `gh release create <tag> --prerelease` で上げ、PRにGIFとmp4のリンクを書く
- 見本：https://github.com/Sol-momma/ChiriTsumomma/pull/2#issuecomment-6370639466
- 公開リポジトリでは、録画に個人情報やログイン後の画面が映る場合は上げずに、壮宇さんに確認する。
