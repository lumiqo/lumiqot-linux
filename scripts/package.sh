#!/usr/bin/env bash
set -euo pipefail
export GOWORK=off

if [[ $# != 5 || ! $1 =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Usage: scripts/package.sh VERSION WORKER_REPO WORKER_COMMIT ALLINO_REPO ALLINO_COMMIT" >&2
    exit 2
fi
version=$1
worker_repo=$(cd -- "$2" && pwd)
worker_commit=$(git -C "$worker_repo" rev-parse --verify "$3^{commit}")
allino_repo=$(cd -- "$4" && pwd)
allino_commit=$(git -C "$allino_repo" rev-parse --verify "$5^{commit}")
cd -- "$(dirname -- "$0")/.."
repo=$PWD
mkdir -p dist
build_dir=$(mktemp -d)
trap 'rm -rf -- "$build_dir"' EXIT
mkdir "$build_dir/worker" "$build_dir/allino"
git -C "$worker_repo" archive "$worker_commit" | tar -x -C "$build_dir/worker"
git -C "$allino_repo" archive "$allino_commit" | tar -x -C "$build_dir/allino"

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
        go list -deps -f '{{with .Module}}{{if not .Main}}{{printf "%s\t%s" .Path .Dir}}{{end}}{{end}}' \
            ./cmd/lumiqo-worker | sort -u > "$build_dir/modules"
        shopt -s nullglob
        while IFS=$'\t' read -r module directory; do
            [[ -n "$module" ]] || continue
            licenses=("$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE* "$directory"/PATENTS*)
            if (( ${#licenses[@]} )); then
                destination="$package/licenses/$module"
                mkdir -p "$destination"
                cp -- "${licenses[@]}" "$destination/"
            fi
        done < "$build_dir/modules"
    )
    mkdir -p "$package/licenses/go"
    go_root=$(go env GOROOT)
    if [[ -f "$go_root/LICENSE" ]]; then
        cp -- "$go_root/LICENSE" "$go_root/PATENTS" "$package/licenses/go/"
    else
        cp -- "/usr/share/doc/golang-${go_root##*/go-}-go/copyright" "$package/licenses/go/"
    fi
    install -m 0755 install.sh "$package/install.sh"
    tar -czf "$repo/dist/$name.tar.gz" -C "$build_dir" "$name"
done

cd "$repo/dist"
sha256sum "lumiqot-linux-${version}-linux-amd64.tar.gz" \
    "lumiqot-linux-${version}-linux-arm64.tar.gz" > "lumiqot-linux-${version}-SHA256SUMS"
