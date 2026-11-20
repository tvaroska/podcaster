#!/usr/bin/env bash
# Canonical verification gate for podcaster.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

GOCACHE="$(mktemp -d /tmp/go-build-podcaster-XXXXXX)"
export GOCACHE
trap 'rm -rf "${GOCACHE}"' EXIT

unformatted="$(gofmt -s -l .)"
if [[ -n "${unformatted}" ]]; then
  echo "ERROR: run 'gofmt -s -w' on:" >&2
  echo "${unformatted}" >&2
  exit 1
fi
go vet ./...
if [[ $# -eq 0 ]]; then
  go test ./...
else
  go test "$@"
fi
