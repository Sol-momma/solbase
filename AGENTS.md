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

## 各パートで必ず書くもの
各パートのPRに `docs/how-supabase-does-it/<part>.md` を追加する。本物のSupabaseがどうしているかを短く説明し、本物のソース（GitHubのコードや公式ドキュメント）へのリンクを付ける。
- rest.md：PostgREST（https://github.com/PostgREST/postgrest）
- auth.md：GoTrue / Supabase Auth（https://github.com/supabase/auth）
- rls.md：RLS＋JWT（https://supabase.com/docs/guides/database/postgres/row-level-security）
- realtime.md：Realtime（https://github.com/supabase/realtime）
