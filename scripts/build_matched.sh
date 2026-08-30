#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="${BIFROST_SOURCE_DIR:-/tmp/bifrost-v2.0.0}"
OUTPUT_DIR="$ROOT_DIR/.build/matched"
SNAPSHOT_DIR="$ROOT_DIR/.build/source-v2.0.0"
CACHE_DIR="$ROOT_DIR/.cache/matched"
GO_IMAGE="golang:1.27.0"
NODE_IMAGE="node:22.12.0"
EXPECTED_REVISION="e4a30d6041c0446603aea615bc5da340dac001b1"

[[ -d "$SOURCE_DIR/.git" ]] || { echo "Missing Bifrost checkout: $SOURCE_DIR" >&2; exit 1; }
[[ "$(git -C "$SOURCE_DIR" rev-parse HEAD)" == "$EXPECTED_REVISION" ]] || {
  echo "Bifrost checkout is not revision $EXPECTED_REVISION" >&2
  exit 1
}

mkdir -p "$OUTPUT_DIR" "$CACHE_DIR/build" "$CACHE_DIR/mod"
rm -f "$OUTPUT_DIR/compatible-runtime.json" "$OUTPUT_DIR/isolated-test-result.json"
rm -rf "$SNAPSHOT_DIR"
mkdir -p "$SNAPSHOT_DIR"
git -C "$SOURCE_DIR" archive HEAD | tar -x -C "$SNAPSHOT_DIR"

docker run --rm --user "$(id -u):$(id -g)" \
  -e npm_config_cache=/tmp/npm-cache \
  -v "$SNAPSHOT_DIR:/src" -w /src/ui "$NODE_IMAGE" sh -ec '
    npm ci
    npm run build
  '

WORK_DIR="$SNAPSHOT_DIR/transports/.agent-router-build"
mkdir -p "$WORK_DIR/router" "$WORK_DIR/abi-probe" "$WORK_DIR/official-llm-only"
cp "$ROOT_DIR"/{main.go,config.go,classifier.go,extractor.go} "$WORK_DIR/router/"
cp "$ROOT_DIR/abi-probe/main.go" "$WORK_DIR/abi-probe/main.go"
cp "$SNAPSHOT_DIR/examples/plugins/llm-only/main.go" "$WORK_DIR/official-llm-only/main.go"

docker run --rm --user "$(id -u):$(id -g)" \
  -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod \
  -v "$SNAPSHOT_DIR:/src" -v "$OUTPUT_DIR:/out" -v "$CACHE_DIR:/cache" \
  -w /src/transports "$GO_IMAGE" sh -ec '
    BUILD_TAGS=netgo,osusergo,sqlite_static
    go test -tags="$BUILD_TAGS" /src/transports/.agent-router-build/router
    CGO_ENABLED=1 go build -tags="$BUILD_TAGS" -trimpath \
      -ldflags="-w -s -X main.Version=v2.0.0" \
      -o /out/bifrost-http ./bifrost-http
    CGO_ENABLED=1 go build -tags="$BUILD_TAGS" -trimpath -buildmode=plugin -o /out/official-llm-only.so ./.agent-router-build/official-llm-only
    CGO_ENABLED=1 go build -tags="$BUILD_TAGS" -trimpath -buildmode=plugin -o /out/abi-probe.so ./.agent-router-build/abi-probe
    CGO_ENABLED=1 go build -tags="$BUILD_TAGS" -trimpath -buildmode=plugin -o /out/agent-capability-router.so ./.agent-router-build/router
    go version -m /out/bifrost-http > /out/bifrost-http.buildinfo
    go version -m /out/official-llm-only.so > /out/official-llm-only.buildinfo
    go version -m /out/abi-probe.so > /out/abi-probe.buildinfo
    go version -m /out/agent-capability-router.so > /out/agent-capability-router.buildinfo
  '

(
  cd "$OUTPUT_DIR"
  sha256sum bifrost-http *.so >SHA256SUMS
)
echo "Matched candidate built under: $OUTPUT_DIR"
