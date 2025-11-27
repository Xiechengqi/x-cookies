#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

GOOS=${GOOS:-}
GOARCH=${GOARCH:-}

if [[ -n "$GOOS" && -n "$GOARCH" ]]; then
  echo "Building twitter-scraper for $GOOS/$GOARCH"
else
  echo "Building twitter-scraper for host platform"
fi

go build -o twitter-scraper

echo "Binary generated at $SCRIPT_DIR/twitter-scraper"
