#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$ROOT_DIR/.build/matched"
RELEASE_DIR="$ROOT_DIR/.build/release"
RELEASE_TAG="${RELEASE_TAG:-bifrost-v2.0.0-r1}"
PLATFORM="linux-amd64-glibc"
PLUGIN_ASSET="agent-capability-router-bifrost-v2.0.0-$PLATFORM.so"
HOST_ASSET="bifrost-http-bifrost-v2.0.0-$PLATFORM"
BUNDLE="bifrost-capability-plugin-$RELEASE_TAG-$PLATFORM.tar.gz"

[[ "$RELEASE_TAG" =~ ^bifrost-v2\.0\.0-r[1-9][0-9]*$ ]] || {
  echo "Unsupported release tag: $RELEASE_TAG" >&2
  exit 1
}

required=(
  bifrost-http
  agent-capability-router.so
  official-llm-only.so
  abi-probe.so
  bifrost-http.buildinfo
  agent-capability-router.buildinfo
  official-llm-only.buildinfo
  abi-probe.buildinfo
  compatible-runtime.json
  isolated-test-result.json
  SHA256SUMS
)
for file in "${required[@]}"; do
  [[ -f "$BUILD_DIR/$file" ]] || { echo "Missing tested release artifact: $BUILD_DIR/$file" >&2; exit 1; }
done

stage_parent="$(mktemp -d)"
trap 'rm -rf "$stage_parent"' EXIT
stage="$stage_parent/bifrost-capability-plugin-$RELEASE_TAG-$PLATFORM"
mkdir -p "$stage" "$stage/.build/matched" "$RELEASE_DIR"
rm -f "$RELEASE_DIR"/*

git -C "$ROOT_DIR" ls-files --cached --others --exclude-standard -z |
  tar --null -C "$ROOT_DIR" -T - -cf - |
  tar -C "$stage" -xf -

for file in "${required[@]}"; do
  cp -p "$BUILD_DIR/$file" "$stage/.build/matched/$file"
done

cp -p "$BUILD_DIR/agent-capability-router.so" "$RELEASE_DIR/$PLUGIN_ASSET"
cp -p "$BUILD_DIR/bifrost-http" "$RELEASE_DIR/$HOST_ASSET"
tar -C "$stage_parent" -czf "$RELEASE_DIR/$BUNDLE" "$(basename "$stage")"

(
  cd "$RELEASE_DIR"
  sha256sum "$PLUGIN_ASSET" "$HOST_ASSET" "$BUNDLE" >SHA256SUMS
  sha256sum -c SHA256SUMS
)

tar -tzf "$RELEASE_DIR/$BUNDLE" >"$stage_parent/archive-files.txt"
grep -Fq "/.build/matched/bifrost-http" "$stage_parent/archive-files.txt"
grep -Fq "/.build/matched/agent-capability-router.so" "$stage_parent/archive-files.txt"
echo "Release assets created under: $RELEASE_DIR"
