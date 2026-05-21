#!/usr/bin/env bash
# hydrate-winget.sh — populate the winget manifest template with real
# sha256 sums and the release version, ready for a winget-pkgs PR.
#
# v1.0-1 leaves the published manifest as a TEMPLATE with sha256
# placeholders. At release time, this script reads the per-arch
# sha256 sidecars and the release tag, writes a hydrated copy to
# stdout (or to --out), and leaves the committed template untouched.
#
# Usage:
#   scripts/win/hydrate-winget.sh --version 1.0.0 --dist dist/ \
#       [--out dist/winget/aish.yaml]
#
# The hydrated manifest is what wingetcreate submit consumes; the
# committed template under data/install/windows/winget/aish.yaml is
# the source of truth for the manifest shape.

set -euo pipefail

VERSION=""
DIST="dist"
OUT="/dev/stdout"
TEMPLATE="data/install/windows/winget/aish.yaml"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) VERSION="$2"; shift 2 ;;
        --dist)    DIST="$2";    shift 2 ;;
        --out)     OUT="$2";     shift 2 ;;
        --template) TEMPLATE="$2"; shift 2 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

if [[ -z "$VERSION" ]]; then
    echo "usage: $0 --version <semver> [--dist dist/] [--out file] [--template path]" >&2
    exit 2
fi

if [[ ! -f "$TEMPLATE" ]]; then
    echo "missing template: $TEMPLATE" >&2
    exit 1
fi

read_sha() {
    local file="$1"
    if [[ ! -f "$file" ]]; then
        echo "missing sha256 sidecar: $file" >&2
        exit 1
    fi
    awk '{print toupper($1)}' "$file"
}

amd64_sha=$(read_sha "$DIST/aish-windows-amd64.exe.sha256")
arm64_sha=$(read_sha "$DIST/aish-windows-arm64.exe.sha256")

# Naive template substitution — keeps us out of yq/python dependencies.
sed -e "s|@@VERSION@@|$VERSION|g" \
    -e "s|@@AMD64_SHA256@@|$amd64_sha|g" \
    -e "s|@@ARM64_SHA256@@|$arm64_sha|g" \
    "$TEMPLATE" > "$OUT"
