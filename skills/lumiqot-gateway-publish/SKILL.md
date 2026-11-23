---
name: lumiqot-gateway-publish
description: Inspect and publish HTTP services through Lumiqot Gateway using the lumiqot CLI. Use for Bearer-authenticated API and MCP routes, GitHub App webhooks, and public hostname forwarding to local web services.
---

# Publish services through Lumiqot Gateway

Lumiqo Gateway runs as a Linux user service, including on WSL. It handles Bearer-authenticated API and MCP paths, signed GitHub webhook paths, and public forwarding by hostname. Run the CLI as the user who runs the Gateway. Its configuration is stored in `~/.config/lumiqo-gateway/gateway.yaml`.

## Check availability and publication

```sh
lumiqot service status gateway --json
lumiqot status --json
lumiqot gateway route list
lumiqot gateway domain list
```

- Use the service status to check whether the unit is loaded, running, and ready. Enable it with `lumiqot service enable gateway` when you intend to start it and enable it on boot.
- In the status snapshot, inspect `components.gateway.value` for the listener, connectivity provider, configured public URL, and any connectivity diagnostic. Compare the saved and active entries in the route and domain outputs. When running, `in_sync` reports whether they agree. The domain output also shows which host serves the path routes.
- For the external path, check the deployment's DNS, TLS, and relay configuration and verify the intended URL from outside the host. In the Lumiqo environment, use the Notion network and connectivity notes. `public_url` records the intended URL. DNS, certificates, and any separately managed ingress are configured independently.
- Use `local` with a separately managed HTTPS reverse proxy, `home-relay` with an existing home relay, or `managed-tunnel` for a Cloudflare named tunnel started by Gateway. Managed tunnel configuration requires a public URL, tunnel ID, and an absolute credentials file path readable by the Gateway service. Use `journalctl --user -u lumiqot-gateway.service` for service diagnostics.

## Publish an API by path and issue an API key

Run the backend on a numeric loopback address in the same Linux or WSL environment. Implement the public path in the backend and arrange for the backend to start after reboot. To publish a bundled Worker or Librarian endpoint, find its endpoint ID and authentication policy with `lumiqot service manifest` and use `--endpoint` in place of `--upstream`. Bundled endpoints require an exact public path.

An API or MCP client needs an API key. Create a Bearer route with managed API keys enabled and issue a key scoped to its configured path.

```sh
lumiqot gateway route add --path /api/status --auth bearer \
  --upstream http://127.0.0.1:9000 --managed-api-keys
lumiqot gateway api-key issue --name status-client --path /api/status
lumiqot gateway api-key list
```

- Clients send `Authorization: Bearer <token>`. Gateway authenticates the request and strips that header before forwarding it. Keep the token out of repositories, logs, and browser-delivered code. The issue command displays it once. The list shows names, paths, and UTC creation times.
- Existing routes need `managed_api_keys: true` to accept path-scoped managed keys. For a subtree such as `'/api/*'`, pass the same quoted pattern to both the route and key commands. Add a separate exact route if `/api` itself must be exposed. Existing file-backed Bearer credentials can be configured with `--secret-file` pointing to an absolute, owner-only file.
- Omitting `--path` from `api-key issue` grants access to every Bearer route. Delete a key by name with `lumiqot gateway api-key delete --name status-client`. Key changes take effect immediately on a running Gateway. Remove a route with `lumiqot gateway route remove /api/status`. Revoke its old keys before assigning that path to another service.
- Verify the backend locally, then check the public HTTPS endpoint with and without the token. Missing or invalid credentials should return `401`. For MCP, also verify initialization and streaming.

## Publish a GitHub App webhook

To register the bundled GitHub Issue Worker at `/hooks/github`, use its dedicated command. It permits the `issues` and `issue_comment` events for the selected installation and repository IDs. Repeat the repository flag as needed.

```sh
lumiqot gateway worker-webhook register --secret-file /absolute/private/github-webhook-secret \
  --installation 456 --repository 123
```

Use `lumiqot gateway route add` with an exact `--path`, `--auth github-webhook`, and an absolute, owner-only `--secret-file` containing the secret configured in GitHub. Supply the permitted `--event`, numeric `--repository`, and numeric `--installation` values. Those flags can be repeated. For delivery to a bundled endpoint, use its manifest ID with `--endpoint`. Gateway checks the `X-Hub-Signature-256` signature on the POST body and all three allowlists. Verify the route with a real delivery for an allowed event. Bearer API keys apply only to Bearer routes.

## Forward a public hostname

Hostname forwarding sends requests for the configured host to a numeric loopback HTTP address with a port. It supports `local` and `home-relay` connectivity. **Gateway applies no authentication to these requests.** Every path on the forwarded host is public unless the upstream application enforces access controls. Confirm that sensitive application routes are protected before publishing.

```sh
lumiqot gateway domain add --host app.example.net \
  --upstream http://127.0.0.1:3000 --auth public
lumiqot gateway domain list
```

- The first domain registration sets the host for existing authenticated path routes from the hostname in `public_url`. If the URL is unset, include `--route-host` with the existing path-route hostname or IP. Use separate hosts for public forwarding and authenticated path routes. Existing clients must use the configured path-route host, which remains set after the last forwarded domain is removed.
- Configure DNS and TLS for the new hostname and have the external ingress preserve the original HTTP `Host` header when forwarding to Gateway. Verify the new hostname over HTTPS and recheck the existing authenticated endpoints. Remove the forwarding with `lumiqot gateway domain remove --host app.example.net`. Domain changes apply on a running Gateway or at its next start.
- For direct YAML edits, keep a recoverable copy of the current configuration and referenced secret files. Use the standalone Gateway binary at `/opt/lumiqo/distribution/current/bin/lumiqo-gateway` to `validate` the configuration and `reload` live routing changes. Listener and connectivity changes require a restart. If an edit fails, restore the prior configuration and validate it before retrying.
