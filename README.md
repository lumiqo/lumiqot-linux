# Lumiqot Linux distribution

Build the Linux CLI with the bundled Worker, Librarian, Remote, and Gateway. Worker, Allino,
Gateway, Librarian, and Remote inputs come from explicit Git commits and can use private repositories.
The release contains binaries and dependency licenses.

```sh
go test ./...
scripts/package.sh v1.2.3 /path/to/worker WORKER_COMMIT /path/to/allino ALLINO_COMMIT \
    /path/to/gateway GATEWAY_COMMIT /path/to/librarian LIBRARIAN_COMMIT \
    /path/to/remote REMOTE_COMMIT
```

Build on Linux with Go 1.26 or newer, Python 3.11 or newer, Git, tar, and patch.
The script archives the five input commits, builds for Linux amd64 and arm64,
and generates two tarballs and a shared SHA256SUMS asset in `dist/`. Publish all
three assets together in the stable GitHub release for that exact tag. Manager installs the archive and owns
the active symlink. The installed Gateway is available at
`/opt/lumiqo/distribution/current/bin/lumiqo-gateway`. Gateway configuration is prepared separately from package installation.

## Settings overview

Run `lumiqot status --json` as the managed Linux user to refresh the settings
overview. The command writes `~/.cache/lumiqot/status.json` with the latest
`components`, the snapshot before the last detected change in `previous`,
and the component names that changed in `changedComponents`. Identical
collections update the collection time and retain the last change. The report
includes Gateway connectivity, SSH public keys, selected Worker settings, and
the active GitHub CLI login and account type. Account types are stored per
GitHub login in `~/.config/lumiqot/github-cli.json` and default to personal.
Use `lumiqot github-cli set-account-type LOGIN personal|work|ai` to save a
classification and refresh the status cache.

Package activation installs `~/.config/lumiqot/AGENTS-template.md` and
`~/.codex/AGENTS-Lumiqot-README.md` if they are missing. A status refresh
renders the template to `~/.codex/AGENTS.md`. Lumiqot replaces templates
that begin with the `LUMIQOT-AUTO-UPDATE` comment with the bundled version.
Remove that line before editing the template. Templates can use
`.GitHubSignedIn`, `.GitHubCLI` (with `.Login` and `.AccountType`), and
`.Gateway` (with `.LocalPort` and `.Service`).
Template errors are reported in `agentsError` and leave the previous
`AGENTS.md` in place.

## Worker maintenance

Run the bundled Worker maintenance commands as the Worker user.

```sh
lumiqot worker fetch-all
lumiqot worker push-all --dry-run
lumiqot worker push-all
```

`fetch-all` updates the GitHub tracking refs of managed repositories.
`push-all` publishes unpublished `main` commits recorded by Worker, and
`--dry-run` previews the candidates. Both commands print per-repository
results as JSON. They use `~/.config/lumiqot/worker.json` by default.
Use `--config FILE` for a separately configured Worker.

## Managed service endpoints

Run `lumiqot service manifest` to inspect the bundled `services.json`. Socket
paths are relative to the user manager's runtime directory. Use
`endpoint: worker.github-webhook` with
`auth: github-webhook`, a webhook secret file, and the event, repository ID, and
installation ID allowlists to forward verified deliveries to Worker. Gateway
returns HTTP 503 if Worker cannot accept a delivery. Worker reconciles with
GitHub during periodic polling.

## Package format 1

Assets are named `lumiqot-linux-v1.2.3-linux-amd64.tar.gz`,
`lumiqot-linux-v1.2.3-linux-arm64.tar.gz`, and
`lumiqot-linux-v1.2.3-SHA256SUMS`. The checksum file uses standard
`sha256sum` syntax. GitHub's HTTPS release channel is the trust root for the
archive and checksum assets.

The first entry in the tar.gz is a regular `manifest.json` with mode 0644.
All subsequent entries are the declared regular files at archive root-relative
paths, with no enclosing package directory. For example, the manifest structure
is:

```json
{
  "format": 1,
  "package": "lumiqot-linux",
  "version": "v1.2.3",
  "os": "linux",
  "arch": "amd64",
  "cli": "bin/lumiqot",
  "lifecycle": 2,
  "files": [
    {"path": "bin/lumiqot", "mode": 493, "sha256": "<64 lowercase hex characters>"}
  ]
}
```

The manifest tag and architecture must match the selected asset and host.
Paths use ASCII letters, digits, slash, dot, underscore, plus, at sign, and
hyphen. They must be canonical relative paths, at most 512 bytes, with distinct
names and no dot or parent traversal segments. File modes are JSON integers
420 (0644) or 493 (0755). `bin/lumiqot` is mandatory and executable, and its `version` command must
print the manifest tag. Manager
limits compressed downloads to 128 MiB, decompressed tar input to 512 MiB,
and the manifest to 1 MiB and fewer than 4096 listed files. It rejects
unlisted, missing, duplicate, or digest-mismatched entries, directories and
special entries such as symlinks, hard links and devices. Manager creates
parent directories and assigns permissions itself. Package owners, timestamps,
and extended attributes are ignored. Lifecycle protocol 2 is required by the matching Manager. The format has no install hooks.

## Service management and lifecycle protocol 2

Run these commands as the service user (`lumiqo`) in its systemd user
session. Librarian and Remote start automatically on first install. The defaults
apply whenever a service has no saved preference. Explicit preferences survive
updates.
Remote's MCP socket is private to the service user. Bash execution requires
systemd with cgroup v2 and passwordless sudo for that user. PDF extraction uses
`pdftotext` from `poppler-utils`. Stopping or updating Remote stops active jobs.

Worker and Gateway are disabled by default. Configure their credentials before
enabling them. The managed Gateway reads
`$HOME/.config/lumiqo-gateway/gateway.yaml`, which you can create using the
bundled Gateway's `init` command.

| Command | Default deadline | Effect |
| --- | --- | --- |
| `lumiqot service enable worker` | 60 seconds | Save the enabled preference, start and verify the Worker, and resume admission. |
| `lumiqot service disable worker` | 120 seconds | Save the disabled preference, drain in-flight work, and stop the Worker. |
| `lumiqot service status worker --json` | 8 seconds | Report Worker intent, actual state, and health. |
| `lumiqot service enable librarian` | 60 seconds | Save the enabled preference and wait for the Librarian process. |
| `lumiqot service disable librarian` | 120 seconds | Save the disabled preference and stop the Librarian. |
| `lumiqot service status librarian --json` | 8 seconds | Report Librarian intent, actual state, and health. |
| `lumiqot service enable remote` | 60 seconds | Save the enabled preference and wait for the Remote MCP socket. |
| `lumiqot service disable remote` | 120 seconds | Save the disabled preference and stop Remote and its jobs. |
| `lumiqot service status remote --json` | 8 seconds | Report Remote intent, actual state, and health. |
| `lumiqot service enable gateway` | 60 seconds | Save the enabled preference and wait for Gateway readiness. |
| `lumiqot service disable gateway` | 120 seconds | Save the disabled preference and stop the Gateway. |
| `lumiqot service status gateway --json` | 8 seconds | Report Gateway intent, actual state, and health. |
| `lumiqot lifecycle drain` | 900 seconds | Close Worker admission and wait for active jobs. Gateway drain returns immediately and leaves traffic flowing until stop. |
| `lumiqot lifecycle stop` | 120 seconds | Drain and temporarily stop managed services. |
| `lumiqot lifecycle start` | 60 seconds | Start and resume enabled services and converge disabled services to stopped. |
| `lumiqot lifecycle restart` | 960 seconds | Temporarily stop services, then converge to their saved preferences. If stopping fails, attempt convergence in a separate 60-second recovery window. |
| `lumiqot lifecycle status --json` | 8 seconds | Report aggregate and per-service status. |

Gateway discovers `remote.mcp` through the package manifest.
To publish Remote, create a bearer-authenticated route targeting `remote.mcp`.
The route grants clients the service user's shell and filesystem access.

Manage public routes with the Gateway CLI wrapper. Commands return JSON.
The secret file must already exist, contain the key, and be private to the
Gateway user.

```sh
lumiqot gateway route list
lumiqot gateway route add --path /mcp --auth bearer --endpoint remote.mcp --secret-file /absolute/private/mcp-key
lumiqot gateway route remove /mcp
```

For a local HTTP backend, use `--upstream http://127.0.0.1:9000` in place of
`--endpoint`. A GitHub webhook route uses `--auth github-webhook` and requires
`--event` and `--installation`. Repeat either flag to allow multiple values.
The route covers every repository in each allowed installation. Bearer routes can repeat `--secret-file` for key rotation.

The list includes saved and active routes and indicates whether they agree
when Gateway is running. Add and remove apply to a running Gateway or save
the validated configuration for the next start. Removal leaves secret files
in place. The wrapper uses the same configuration file as the Gateway user
service.

To issue named API keys for a new Bearer route, enable managed keys when
adding it. For an existing Bearer route, set `managed_api_keys: true` in
the Gateway YAML and reload before issuing keys.

```sh
lumiqot gateway route add --path /mcp --auth bearer --endpoint remote.mcp --managed-api-keys
lumiqot gateway api-key issue --name my-client --path /mcp
lumiqot gateway api-key issue --name global-client
lumiqot gateway api-key list
lumiqot gateway api-key delete --name my-client
```

Omit `--path` to issue a key for every Bearer route in this Gateway
instance, including routes that use secret files. Global keys have
an empty `path` in the list. GitHub webhook routes still require webhook
signatures.

Use `api_key.token` from the issue response as an `Authorization: Bearer`
credential. The token is displayed only at issuance. The list shows the
name, route, and UTC creation time. Deleting a key revokes it immediately
when Gateway is running.

Each service has a private preference file at
`$HOME/.local/state/lumiqot/services/<service>`. The CLI writes it atomically
as the service user. A missing file selects that service's documented default.
A read error or invalid value prevents automatic startup and appears in status.
The registered user unit checks this preference through the active CLI before
each start, including at boot and on automatic restart. Manager enables
lingering. The Worker control socket and temporary admission marker use the
systemd RuntimeDirectory, surviving automatic service restarts within a
session while clearing at explicit shutdown or reboot. Worker configuration
and job data live outside the distribution.

Mutations use the same `.update.lock` as Manager. Interactive commands wait
up to 20 minutes for the lock, then apply their `--timeout SECONDS` deadline.
If activation happened while a command waited, it dispatches through the new
CLI under the lock. Manager temporarily disarms unit startup during package
switches, then converges the active or recovered version. Preference changes
queued behind a transaction apply to the version that wins activation or
rollback. If a user operation acquires the lock first, a competing Manager
transaction reports the lock conflict and can be retried. If convergence fails or a drain times out after a preference is
saved, the preference remains in effect and a later start or reconciliation
retries convergence. The CLI never force-kills in-flight jobs. Status reads
are point-in-time observations during a transaction.

### Forward a hostname through Gateway

```sh
lumiqot gateway domain add --host app.example.net --upstream http://127.0.0.1:3000 --auth public
lumiqot gateway domain list
lumiqot gateway domain remove --host app.example.net
```

The first registration uses the hostname from Gateway's `public_url` for
its existing path routes. If that URL is unset, pass `--route-host HOST` with
the hostname or IP your path clients already use. Existing path routes
continue to answer on that host, including after the last domain is removed.
The forwarding rule is public. Any application login is handled by the
upstream service.

Package with a Gateway commit that provides `domain add/list/remove`.
Use `local` or `home-relay` connectivity. Configure
the public DNS, TLS, and ingress mapping separately. The ingress must preserve
the request's Host header.

### JSON status

`service status <service> --json` returns one service object. Lifecycle status
indexes the same objects by service ID.

- `desired` is `enabled`, `disabled`, or `unknown` when the preference cannot be read.
- `actual` is `running`, `stopped`, `draining`, `failed`, or `partial` during a systemd transition.
- `health` is `healthy`, `unhealthy`, `not_applicable` when stopped, or `unknown` when health cannot be determined. Running health checks local readiness and the executable paired with the active CLI.
- `converged` compares intent with actual state independently of process health.
- `load`, `systemd`, and `boot_enabled` describe the unit. `ready` reports service readiness. For Worker, `draining` and `active` report admission state and the active job count. `error` is included when a probe fails.

Lifecycle status adds an aggregate `state` of `healthy`, `disabled`,
`draining`, `stopped`, `failed`, or `partial`. Its `converged` means all
services match their preferences. Aggregate `ready` means all enabled
services are healthy and set up for boot and all disabled services are stopped.
An entirely disabled installation has `state: "disabled"` and `ready: true`.
Exit codes are 0 for success (including status reports containing probe
errors), 1 for operational failure, 2 for invalid arguments, and 124 for a
mutation deadline. See the Worker journal with
`journalctl --user -u lumiqot-worker.service`.

### Release and verification

Use a Worker commit that implements `-control-socket` and `-drain-marker`
and a Gateway commit that implements `drain` when packaging either architecture. Publish a Manager with protocol 2
support before using the new package with WinApp. Use matching releases on a
fresh WSL installation. The package manifest still uses archive format 1.

Run `go test ./...` for contract tests. Set `LUMIQOT_TEST_SYSTEMD=1` in a
running Linux user systemd session to also exercise the bundled startup
condition on a disposable unit.
