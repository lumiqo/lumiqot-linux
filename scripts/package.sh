#!/usr/bin/env bash
set -euo pipefail
export GOWORK=off

if [[ $# != 7 || ! $1 =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "Usage: scripts/package.sh VERSION WORKER_REPO WORKER_COMMIT ALLINO_REPO ALLINO_COMMIT GATEWAY_REPO GATEWAY_COMMIT" >&2
    exit 2
fi
version=$1
worker_repo=$(cd -- "$2" && pwd)
worker_commit=$(git -C "$worker_repo" rev-parse --verify "$3^{commit}")
allino_repo=$(cd -- "$4" && pwd)
allino_commit=$(git -C "$allino_repo" rev-parse --verify "$5^{commit}")
gateway_repo=$(cd -- "$6" && pwd)
gateway_commit=$(git -C "$gateway_repo" rev-parse --verify "$7^{commit}")
cd -- "$(dirname -- "$0")/.."
repo=$PWD
mkdir -p dist
build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT
mkdir -p "$build_dir/worker/.deps/allino" "$build_dir/gateway"
git -C "$worker_repo" archive "$worker_commit" | tar -x -C "$build_dir/worker"
git -C "$allino_repo" archive "$allino_commit" | tar -x -C "$build_dir/worker/.deps/allino"
git -C "$gateway_repo" archive "$gateway_commit" | tar -x -C "$build_dir/gateway"
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
if [[ $worker_help != *"-control-socket "* || $worker_help != *"-drain-marker "* ]]; then
    echo "Worker commit $worker_commit does not support the managed control socket and drain marker" >&2
    exit 1
fi

if ! (
    cd "$build_dir/gateway"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOHOSTARCH)" go run . drain
); then
    echo "Gateway commit $gateway_commit does not support the managed drain command" >&2
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
    mkdir -p "$package/licenses/go"
    go_root=$(go env GOROOT)
    if [[ -f "$go_root/LICENSE" ]]; then
        cp -- "$go_root/LICENSE" "$go_root/PATENTS" "$package/licenses/go/"
    else
        cp -- "/usr/share/doc/golang-${go_root##*/go-}-go/copyright" "$package/licenses/go/"
    fi
    python3 scripts/archive.py "$package" "$version" "$arch" "$repo/dist/$name.tar.gz"
done

cd "$repo/dist"
sha256sum "lumiqot-linux-${version}-linux-amd64.tar.gz" \
    "lumiqot-linux-${version}-linux-arm64.tar.gz" > "lumiqot-linux-${version}-SHA256SUMS"
