# Lumiqot Linux distribution

Build the Linux CLI and the bundled Worker from explicit Git commits. The Worker
and Allino inputs can come from private repositories. The release contains
binaries and dependency licenses.

```sh
go test ./...
scripts/package.sh v1.2.3 /path/to/worker WORKER_COMMIT /path/to/allino ALLINO_COMMIT
```

Build on Linux with Go 1.25 or newer, Python 3.11 or newer, Git, tar, and patch.
The script archives both commits, builds for Linux amd64 and arm64, and
generates two tarballs and a shared SHA256SUMS asset in `dist/`. Publish all
three assets together in the stable GitHub release for that exact tag. Use a new
tag for the first format 1 release. Manager installs the archive and owns
the active symlink.

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
session. The Worker is **disabled by default**, with or without an existing
configuration. Manager can install a fresh release before the Worker is
configured. Save a valid configuration and credentials, then explicitly
enable the Worker.

| Command | Default deadline | Effect |
| --- | --- | --- |
| `lumiqot service enable worker` | 60 seconds | Save the enabled preference, start and verify the Worker, and resume admission. |
| `lumiqot service disable worker` | 120 seconds | Save the disabled preference, drain in-flight work, and stop the Worker. |
| `lumiqot service status worker --json` | 8 seconds | Report Worker intent, actual state, and health. |
| `lumiqot lifecycle drain` | 900 seconds | Close admission and wait for active work. |
| `lumiqot lifecycle stop` | 120 seconds | Drain and temporarily stop managed services. |
| `lumiqot lifecycle start` | 60 seconds | Start and resume enabled services and converge disabled services to stopped. |
| `lumiqot lifecycle restart` | 960 seconds | Temporarily stop services, then converge to their saved preferences. If stopping fails, attempt convergence in a separate 60-second recovery window. |
| `lumiqot lifecycle status --json` | 8 seconds | Report aggregate and per-service status. |

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

### JSON status

`service status worker --json` returns one service object. The same object
appears under `services["lumiqot-worker.service"]` in lifecycle status.

- `desired` is `enabled`, `disabled`, or `unknown` when the preference cannot be read.
- `actual` is `running`, `stopped`, `draining`, `failed`, or `partial` during a systemd transition.
- `health` is `healthy`, `unhealthy`, `not_applicable` when stopped, or `unknown` when health cannot be determined. Running health checks local readiness and the executable paired with the active CLI.
- `converged` compares intent with actual state independently of process health.
- `load`, `systemd`, and `boot_enabled` describe the unit. `ready`, `draining`, and `active` report Worker admission and the active job count. `error` is included when a probe fails.

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
when packaging either architecture. Publish a Manager with protocol 2
support before using the new package with WinApp. Use matching releases on a
fresh WSL installation. The package manifest still uses archive format 1.

Run `go test ./...` for contract tests. Set `LUMIQOT_TEST_SYSTEMD=1` in a
running Linux user systemd session to also exercise the bundled startup
condition on a disposable unit.
