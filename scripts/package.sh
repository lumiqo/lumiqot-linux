#!/usr/bin/env bash
set -euo pipefail
export GOWORK=off

if [[ $# != 11 || ! $1 =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "Usage: scripts/package.sh VERSION WORKER_REPO WORKER_COMMIT ALLINO_REPO ALLINO_COMMIT GATEWAY_REPO GATEWAY_COMMIT LIBRARIAN_REPO LIBRARIAN_COMMIT REMOTE_REPO REMOTE_COMMIT" >&2
    exit 2
fi
version=$1
worker_repo=$(cd -- "$2" && pwd)
worker_ref=$3
allino_repo=$(cd -- "$4" && pwd)
allino_commit=$(git -C "$allino_repo" rev-parse --verify "$5^{commit}")
gateway_repo=$(cd -- "$6" && pwd)
gateway_ref=$7
librarian_repo=$(cd -- "$8" && pwd)
librarian_ref=$9
remote_repo=$(cd -- "${10}" && pwd)
remote_ref=${11}
cd -- "$(dirname -- "$0")/.."
output=${LUMIQOT_PACKAGE_OUTPUT:-"$PWD/dist"}
mkdir -p -- "$output"
output=$(realpath -- "$output")
build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT
mkdir -p "$build_dir/worker/.deps/allino" "$build_dir/gateway" "$build_dir/librarian" "$build_dir/remote"
copy_source() {
    local repo=$1 ref=$2 destination=$3 commit
    if [[ $ref == WORKTREE ]]; then
        git -C "$repo" ls-files -z --cached --others --exclude-standard --deduplicate |
            (cd -- "$repo" && while IFS= read -r -d '' path; do
                if [[ -f $path || -L $path ]]; then printf '%s\0' "$path"; fi
            done) |
            tar -cf - -C "$repo" --null --no-recursion -T - | tar -xf - -C "$destination"
    else
        commit=$(git -C "$repo" rev-parse --verify "$ref^{commit}")
        git -C "$repo" archive "$commit" | tar -xf - -C "$destination"
    fi
}
copy_source "$worker_repo" "$worker_ref" "$build_dir/worker"
git -C "$allino_repo" archive "$allino_commit" | tar -x -C "$build_dir/worker/.deps/allino"
copy_source "$gateway_repo" "$gateway_ref" "$build_dir/gateway"
copy_source "$librarian_repo" "$librarian_ref" "$build_dir/librarian"
copy_source "$remote_repo" "$remote_ref" "$build_dir/remote"
if [[ ! -f "$build_dir/worker/.deps/allino/job_queue.go" ]]; then
    (cd "$build_dir/worker/.deps/allino" && patch -p1 < "$build_dir/worker/patches/allino-sql-queue.patch")
fi

# A disabled installation does not execute Worker during activation. Verify
# that the selected source supports managed operation before creating releases.
if ! worker_help=$(
    cd "$build_dir/worker"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOHOSTARCH)" go run ./cmd/lumiqo-worker -help 2>&1
); then
    printf '%s\n' "$worker_help" >&2
    exit 1
fi
if [[ $worker_help != *"-control-socket "* || $worker_help != *"-drain-marker "* || $worker_help != *"-webhook-socket "* ]]; then
    echo "Worker source $worker_ref does not support the managed control socket, webhook socket and drain marker" >&2
    exit 1
fi
dry_run_flag='(^|[[:space:]])-dry-run([[:space:]]|$)'
if [[ $worker_help != *fetch-all* || $worker_help != *push-all* || ! $worker_help =~ $dry_run_flag ]]; then
    echo "Worker source $worker_ref does not support fetch-all and push-all with dry-run" >&2
    exit 1
fi

if ! librarian_help=$(
    cd "$build_dir/librarian"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOHOSTARCH)" go run . serve -help 2>&1
); then
    printf '%s\n' "$librarian_help" >&2
    exit 1
fi
if [[ $librarian_help != *"-unix-socket "* ]]; then
    echo "Librarian source $librarian_ref does not support the managed socket" >&2
    exit 1
fi

if ! remote_help=$(
    cd "$build_dir/remote"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOHOSTARCH)" go run ./cmd/lumiqo-remote -help 2>&1
); then
    printf '%s\n' "$remote_help" >&2
    exit 1
fi
if [[ $remote_help != *"-unix-socket "* ]]; then
    echo "Remote source $remote_ref does not support the managed socket" >&2
    exit 1
fi

# Check that the pinned Gateway can resolve routes from the packaged manifest.
check_dir="$build_dir/check"
mkdir -p "$check_dir/bin"
cp cmd/lumiqot/services.json "$check_dir/services.json"
printf '%s\n' 'test-key-for-local-package-validation-only' > "$check_dir/key"
chmod 0600 "$check_dir/key"
cat > "$check_dir/gateway.yaml" <<EOF
version: 1
routes:
  - path: /hooks/github
    endpoint: worker.github-webhook
    auth: github-webhook
    events: [issues, issue_comment]
    installations: [1]
    secret_files:
      - "$check_dir/key"
  - path: /librarian-mcp
    endpoint: librarian.mcp
    auth: bearer
    secret_files:
      - "$check_dir/key"
  - path: /remote-mcp
    endpoint: remote.mcp
    auth: bearer
    secret_files:
      - "$check_dir/key"
EOF
if ! (
    cd "$build_dir/gateway"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOHOSTARCH)" go build -o "$check_dir/bin/lumiqo-gateway" .
    "$check_dir/bin/lumiqo-gateway" drain &&
        XDG_RUNTIME_DIR=/run/user/1000 "$check_dir/bin/lumiqo-gateway" validate -config "$check_dir/gateway.yaml"
); then
    echo "Gateway source $gateway_ref does not support the managed drain command or service endpoints" >&2
    exit 1
fi

copy_module_licenses() (
    target=$1
    license_root=$2
    go list -deps -f '{{with .Module}}{{if not .Main}}{{printf "%s\t%s" .Path .Dir}}{{end}}{{end}}' \
        "$target" | sort -u > "$build_dir/modules"
    shopt -s nullglob
    while IFS=$'\t' read -r module directory; do
        [[ -n "$module" ]] || continue
        licenses=("$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE* "$directory"/PATENTS*)
        if (( ${#licenses[@]} )); then
            destination="$license_root/$module"
            mkdir -p "$destination"
            cp -- "${licenses[@]}" "$destination/"
        fi
    done < "$build_dir/modules"
)

for arch in amd64 arm64; do
    name="lumiqot-linux-${version}-linux-${arch}"
    package="$build_dir/$name"
    mkdir -p "$package/bin" "$package/libexec/lumiqot"
    export GOOS=linux GOARCH="$arch" CGO_ENABLED=0
    go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$version" \
        -o "$package/bin/lumiqot" ./cmd/lumiqot
    (
        cd "$build_dir/worker"
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
            -o "$package/libexec/lumiqot/lumiqo-worker" ./cmd/lumiqo-worker
        copy_module_licenses ./cmd/lumiqo-worker "$package/licenses"
    )
    (
        cd "$build_dir/gateway"
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
            -o "$package/bin/lumiqo-gateway" .
        copy_module_licenses . "$package/licenses/gateway"
    )
    (
        cd "$build_dir/librarian"
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
            -o "$package/libexec/lumiqot/lumiqo-librarian" .
        copy_module_licenses . "$package/licenses/librarian"
    )
    (
        cd "$build_dir/remote"
        go build -trimpath -buildvcs=false -ldflags="-s -w" \
            -o "$package/libexec/lumiqot/lumiqo-remote" ./cmd/lumiqo-remote
        copy_module_licenses ./cmd/lumiqo-remote "$package/licenses/remote"
    )
    cp cmd/lumiqot/services.json "$package/services.json"
    mkdir -p "$package/share/lumiqot"
    cp cmd/lumiqot/AGENTS-template.md cmd/lumiqot/AGENTS-Lumiqot-README.md "$package/share/lumiqot/"
    cp -a skills "$package/share/lumiqot/"
    mkdir -p "$package/licenses/go"
    go_root=$(go env GOROOT)
    if [[ -f "$go_root/LICENSE" ]]; then
        cp -- "$go_root/LICENSE" "$go_root/PATENTS" "$package/licenses/go/"
    else
        cp -- "/usr/share/doc/golang-${go_root##*/go-}-go/copyright" "$package/licenses/go/"
    fi
    python3 scripts/archive.py "$package" "$version" "$arch" "$output/$name.tar.gz"
done

cd "$output"
sha256sum "lumiqot-linux-${version}-linux-amd64.tar.gz" \
    "lumiqot-linux-${version}-linux-arm64.tar.gz" > "lumiqot-linux-${version}-SHA256SUMS"
