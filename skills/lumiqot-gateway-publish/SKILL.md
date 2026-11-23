---
name: lumiqot-gateway-publish
description: Build and expose an HTTP backend through the Lumiqot Linux Gateway over public HTTPS. Use for authenticated APIs, MCP servers, and GitHub App webhooks when the user wants to use the Lumiqot Gateway or its existing public ingress.
---

# Gateway 経由でサービスを公開する

1. 公開ホスト名・パス、利用クライアント、認証要件を確認する。既存の DNS・TLS・リレー経路は配置先の運用資料で確認する。この環境の構成は Notion のネットワーク・接続資料を参照する。既存の公開経路が使える場合は、その経路から Gateway の HTTP リスナーに転送する。既存の home relay は `home-relay`、Gateway が Cloudflare named tunnel を起動する構成は `managed-tunnel`。別管理のリバースプロキシで TLS を終端する場合は `local` と公開 HTTPS URL も使える。`public_url` の設定だけでは DNS・TLS・リレーは構成されない。
2. 新規バックエンドは Gateway と同じ Linux/WSL 環境の数値ループバックアドレスで待ち受け、再起動後もサービスマネージャーで起動できるようにする。公開するパスをバックエンドに実装する。Worker・Librarian の同梱エンドポイントは `lumiqot service manifest` で確認し、TCP upstream の代わりに `endpoint` を指定する。
3. 認証と公開パスを設定する。通常の API・MCP には `bearer` を使い、鍵は信頼できるクライアントに渡す。Gateway が転送前に Bearer ヘッダーを削除するので、バックエンドはその値に依存せず、ツールやリソースの権限も必要に応じて管理する。GitHub App には `github-webhook` を使い、共有した署名鍵とイベント・数値のリポジトリ ID・インストール ID の許可リストを用意する。公開経路でブラウザーに共有鍵を配る必要がある場合は、先にクライアント側の認証設計を見直す。現行 Gateway のルートは匿名公開やセッション認証に対応していない。
4. localhost でバックエンドと Gateway を検証してから公開経路を適用する。外部から HTTPS で対象ルートを確認し、認証なしは `401`、未登録のパスは `404`、認証済みリクエストはアプリの期待する応答になることを確認する。MCP は初期化と必要なストリーミング、Webhook は許可対象の実配送で確認する。Gateway の readiness と DNS・TLS・外部疎通は個別に確認する。

## 設定例と操作

管理対象の Gateway ユーザーと同じアカウントで操作する。既定設定はそのユーザーの `~/.config/lumiqo-gateway/gateway.yaml`。Gateway の実行ファイルは `/opt/lumiqo/distribution/current/bin/lumiqo-gateway`。既存設定がある場合、ルートだけを追加し、他のルートと接続設定を保持する。

次は `/api/status` だけを公開する設定例。既存の YAML では `routes` に項目だけ追加し、待ち受けと公開経路は現在の設定に合わせる。鍵は絶対パスの所有者専用ファイルに置き、リポジトリやログに含めない。

```yaml
version: 1
listen: 127.0.0.1:8080
connectivity:
  provider: local
routes:
  - path: /api/status
    upstream: http://127.0.0.1:9000
    auth: bearer
    secret_files:
      - /absolute/private/path/api-key
```

`/api/*` を使うと配下を公開できる。`/api` 自体も必要なら完全一致のルートを別途追加する。同梱エンドポイントには完全一致の公開パスとマニフェスト指定の認証が必要。`managed-tunnel` の場合は `connectivity` に `public_url`、`tunnel_id`、絶対パスの `credentials_file` を設定し、Gateway のサービスから `cloudflared` を起動できるようにする。

- 設定を適用する前に、現在の YAML と参照中の鍵を復旧できるようにしておく。Gateway 実行ファイルの `validate` で設定を確認し、稼働中のルート変更を `reload` で反映する。失敗したら以前の YAML を戻し、検証からやり直す。待ち受けや接続方式の変更には再起動が必要。
- 初回の起動には `lumiqot service enable gateway` を使う。状態は `lumiqot service status gateway --json`、詳細は Gateway 実行ファイルの `doctor` と `journalctl --user -u lumiqot-gateway.service` で確認する。
- Gateway ルートと外部の公開経路は別々に反映する。DNS、TLS、リレーの切り替えは配置先の手順に沿い、公開経路に問題があれば以前のマッピングに戻す。
