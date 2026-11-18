#!/bin/sh
set -eu

if [ "$#" -gt 1 ]; then
    echo "Usage: ./install.sh [PREFIX]" >&2
    exit 2
fi
prefix=${1:-"$HOME/.local"}
case "$prefix" in
    /*) ;;
    *) echo "PREFIX must be absolute" >&2; exit 2 ;;
esac
source_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ ! -x "$source_dir/bin/lumiqot" ] || [ ! -x "$source_dir/libexec/lumiqot/lumiqo-worker" ]; then
    echo "Run this installer from a complete release archive" >&2
    exit 1
fi
install -d -- "$prefix/bin" "$prefix/libexec/lumiqot"
install -m 0755 -- "$source_dir/libexec/lumiqot/lumiqo-worker" "$prefix/libexec/lumiqot/lumiqo-worker"
install -m 0755 -- "$source_dir/bin/lumiqot" "$prefix/bin/lumiqot"
