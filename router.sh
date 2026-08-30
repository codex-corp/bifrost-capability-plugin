#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIFROST_URL="${BIFROST_URL:-http://127.0.0.1:10020}"
BIFROST_APP_DIR="${BIFROST_APP_DIR:-$HOME/.config/bifrost}"
PLUGIN_DIR="${BIFROST_PLUGIN_DIR:-$HOME/.local/lib/bifrost/plugins}"
PLUGIN_NAME="agent-capability-router"
PLUGIN_SO="$ROOT_DIR/.build/matched/$PLUGIN_NAME.so"
MATCHED_HOST="$ROOT_DIR/.build/matched/bifrost-http"
COMPATIBILITY_MARKER="$ROOT_DIR/.build/matched/compatible-runtime.json"
RUNTIME_BIN="${BIFROST_RUNTIME_BIN:-$HOME/.local/lib/bifrost/v2.0.0-matched/bifrost-http}"
INSTALLED_SO="$PLUGIN_DIR/$PLUGIN_NAME.so"
BACKUP_ROOT="$ROOT_DIR/backups"
INSTALL_CONFIG="$ROOT_DIR/.local/install.json"
GO_IMAGE="golang:1.27.0"
EXPECTED_TRANSPORT='"v2.0.0"'

api() {
  local method="$1" path="$2"
  shift 2
  local auth=()
  if [[ -n "${BIFROST_ADMIN_BEARER_TOKEN:-}" ]]; then
    auth=(-H "Authorization: Bearer $BIFROST_ADMIN_BEARER_TOKEN")
  elif [[ -n "${BIFROST_ADMIN_BASIC:-}" ]]; then
    auth=(-u "$BIFROST_ADMIN_BASIC")
  fi
  curl --fail-with-body --silent --show-error --max-time 30 \
    -X "$method" "${BIFROST_URL}${path}" "${auth[@]}" "$@"
}

require_commands() {
  local missing=()
  for command in curl jq docker python3 sha256sum flock; do
    command -v "$command" >/dev/null 2>&1 || missing+=("$command")
  done
  if ((${#missing[@]})); then
    echo "Missing required commands: ${missing[*]}" >&2
    exit 1
  fi
}

status() {
  require_commands
  echo "Bifrost health: $(api GET /health | jq -r '.status')"
  echo "Bifrost version: $(api GET /api/version | jq -r '.')"
  echo "Runtime target: linux/amd64, Go 1.27.0, core v1.8.3, framework v1.6.0"
  if [[ -f "$COMPATIBILITY_MARKER" && -f "$RUNTIME_BIN" ]]; then
    local expected_host current_host
    expected_host="$(jq -r '.host_sha256' "$COMPATIBILITY_MARKER")"
    current_host="$(sha256sum "$RUNTIME_BIN" | cut -d' ' -f1)"
    [[ "$expected_host" == "$current_host" ]] && echo "Dynamic plugin ABI: isolated candidate matches runtime" || echo "Dynamic plugin ABI: current runtime is NOT the isolated matched candidate"
  else
    echo "Dynamic plugin ABI: not proven against current runtime"
  fi
  echo "Plugin:"
  if api GET "/api/plugins/$PLUGIN_NAME" >/tmp/agent-router-plugin-status.json 2>/dev/null; then
    jq '{name:.name,enabled:.enabled,path:.path,placement:.placement,order:.order,status:.status.status}' /tmp/agent-router-plugin-status.json
  else
    echo "  not installed"
  fi
  echo "Capability rules:"
  api GET /api/routing/rules | jq -r '.rules[] | select(.name | startswith("Agent CR ")) | "  \(.priority) \(.name) enabled=\(.enabled)"'
}

validate() {
  require_commands
  "$ROOT_DIR/install.sh" check
  jq -e . "$ROOT_DIR/config/plugin.json" "$ROOT_DIR/config/models.json" "$ROOT_DIR/config/lanes.json" "$ROOT_DIR/config/routing-rules.json" >/dev/null
  [[ "$(api GET /api/version)" == "$EXPECTED_TRANSPORT" ]] || {
    echo "Expected Bifrost v2.0.0; found $(api GET /api/version)" >&2
    exit 1
  }
  [[ "$(uname -s)/$(uname -m)" == "Linux/x86_64" ]] || {
    echo "This package targets Linux/x86_64; found $(uname -s)/$(uname -m)" >&2
    exit 1
  }
  api GET /api/routing/complexity-analyzer-config | jq -e \
    '.tier_boundaries.simple_medium != null and .tier_boundaries.medium_complex != null and .tier_boundaries.complex_reasoning != null' >/dev/null || {
    echo "Bifrost Complexity Router configuration is unavailable." >&2
    exit 1
  }
  python3 "$ROOT_DIR/scripts/inspect_db.py" validate-models \
    "$BIFROST_APP_DIR/config.db" "$ROOT_DIR/config/models.json"
  jq -e '
    length > 0 and
    (all(.[]; (.name | startswith("Agent CR ")) and
      (.cel_expression | contains("agent-")) and
      ((.cel_expression | contains("codex-")) | not) and
      .scope == "virtual_key" and
      (.targets | length > 0)))
  ' "$ROOT_DIR/config/routing-rules.json" >/dev/null
  echo "Validation passed: runtime, installation, model inventory, and agent-only rule isolation."
}

build() {
  require_commands
  mkdir -p "$ROOT_DIR/.build" "$ROOT_DIR/.cache/go-build" "$ROOT_DIR/.cache/go-mod"
  docker run --rm --user "$(id -u):$(id -g)" \
    -e PATH=/usr/local/go/bin:/usr/bin:/bin \
    -e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/go-mod \
    -v "$ROOT_DIR:/src" -w /src "$GO_IMAGE" sh -ec '
      go mod tidy
      gofmt -w *.go
      go test ./...
      go vet ./...
    '
  "$ROOT_DIR/scripts/build_matched.sh"
  echo "Built matched host and plugins under: $ROOT_DIR/.build/matched"
}

test_candidate() {
  python3 "$ROOT_DIR/scripts/test_candidate.py"
}

require_compatible_runtime() {
  [[ -f "$PLUGIN_SO" && -f "$MATCHED_HOST" && -f "$COMPATIBILITY_MARKER" ]] || {
    echo "No successful isolated matched-runtime test. Run: ./router.sh build && ./router.sh test-candidate" >&2
    exit 1
  }
  [[ -f "$RUNTIME_BIN" ]] || { echo "Bifrost runtime not found: $RUNTIME_BIN" >&2; exit 1; }
  local expected_host expected_plugin current_host current_plugin
  expected_host="$(jq -r '.host_sha256' "$COMPATIBILITY_MARKER")"
  expected_plugin="$(jq -r '.plugin_sha256' "$COMPATIBILITY_MARKER")"
  current_host="$(sha256sum "$RUNTIME_BIN" | cut -d' ' -f1)"
  current_plugin="$(sha256sum "$PLUGIN_SO" | cut -d' ' -f1)"
  [[ "$current_plugin" == "$expected_plugin" ]] || { echo "Plugin changed after isolated test; test again." >&2; exit 1; }
  [[ "$current_host" == "$expected_host" ]] || {
    echo "Refusing apply: the running-service executable is not the isolated ABI-matched Bifrost candidate." >&2
    echo "Current: $RUNTIME_BIN ($current_host)" >&2
    echo "Tested:  $MATCHED_HOST ($expected_host)" >&2
    exit 1
  }
}

backup_live() {
  local backup_dir="$1"
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"
  for file in config.json config.db config.db-wal config.db-shm logs.db logs.db-wal logs.db-shm; do
    [[ -f "$BIFROST_APP_DIR/$file" ]] && cp -p "$BIFROST_APP_DIR/$file" "$backup_dir/$file"
  done
  api GET /api/plugins >"$backup_dir/plugins.json"
  api GET /api/routing/rules >"$backup_dir/routing-rules.json"
  api GET /api/routing/complexity-analyzer-config >"$backup_dir/complexity-router.json"
}

upsert_plugin() {
  local body current desired_state current_state
  body="$(jq --arg path "$INSTALLED_SO" --argjson shadow "$(jq '.shadow_mode' "$INSTALL_CONFIG")" \
    '. + {path:$path} | .config.shadow_mode=$shadow' "$ROOT_DIR/config/plugin.json")"
  if current="$(api GET "/api/plugins/$PLUGIN_NAME" 2>/dev/null)"; then
    desired_state="$(jq -Sc '{name,enabled,path,placement,order,config}' <<<"$body")"
    current_state="$(jq -Sc '{name,enabled,path,placement,order,config}' <<<"$current")"
    if [[ "$desired_state" == "$current_state" ]]; then
      echo "Plugin configuration already matches; skipping protected v2 update."
      return
    fi
    api PUT "/api/plugins/$PLUGIN_NAME" -H 'Content-Type: application/json' --data "$body" >/dev/null
  else
    api POST /api/plugins -H 'Content-Type: application/json' --data "$body" >/dev/null
  fi
}

upsert_rules() {
  local existing rule name id
  existing="$(api GET /api/routing/rules)"
  while IFS= read -r encoded; do
    rule="$(printf '%s' "$encoded" | base64 --decode)"
    name="$(jq -r '.name' <<<"$rule")"
    id="$(jq -r --arg name "$name" '.rules[] | select(.name == $name) | .id' <<<"$existing" | head -n1)"
    if [[ -n "$id" ]]; then
      api PUT "/api/routing/rules/$id" -H 'Content-Type: application/json' --data "$rule" >/dev/null
    else
      api POST /api/routing/rules -H 'Content-Type: application/json' --data "$rule" >/dev/null
    fi
  done < <(jq --arg scope_id "$(jq -r '.virtual_key.id' "$INSTALL_CONFIG")" \
    'map(.scope_id=$scope_id) | .[] | @base64' "$ROOT_DIR/config/routing-rules.json" -r)
}

apply() {
  require_commands
  validate
  require_compatible_runtime
  exec 9>"$ROOT_DIR/.apply.lock"
  flock -n 9 || { echo "Another router operation is running." >&2; exit 1; }
  local stamp backup_dir
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="$BACKUP_ROOT/$stamp"
  backup_live "$backup_dir"
  mkdir -p "$PLUGIN_DIR"
  install -m 0755 "$PLUGIN_SO" "$INSTALLED_SO"
  upsert_plugin
  upsert_rules
  printf '%s\n' "$backup_dir" >"$BACKUP_ROOT/latest"
  echo "Applied with shadow_mode=$(jq -r '.shadow_mode' "$INSTALL_CONFIG"). Backup: $backup_dir"
}

apply_rules() {
  require_commands
  validate
  exec 9>"$ROOT_DIR/.apply.lock"
  flock -n 9 || { echo "Another router operation is running." >&2; exit 1; }
  local stamp backup_dir
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_dir="$BACKUP_ROOT/$stamp"
  backup_live "$backup_dir"
  upsert_rules
  printf '%s\n' "$backup_dir" >"$BACKUP_ROOT/latest"
  echo "Applied Agent CR rules only. Dashboard-managed plugin configuration was not changed. Backup: $backup_dir"
}

rollback() {
  require_commands
  exec 9>"$ROOT_DIR/.apply.lock"
  flock -n 9 || { echo "Another router operation is running." >&2; exit 1; }
  local existing id stamp
  existing="$(api GET /api/routing/rules)"
  while IFS= read -r id; do
    [[ -n "$id" ]] && api DELETE "/api/routing/rules/$id" >/dev/null
  done < <(jq -r '.rules[] | select(.name | startswith("Agent CR ")) | .id' <<<"$existing")
  api DELETE "/api/plugins/$PLUGIN_NAME" >/dev/null 2>&1 || true
  if [[ -f "$INSTALLED_SO" ]]; then
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    mv "$INSTALLED_SO" "$INSTALLED_SO.disabled-$stamp"
  fi
  echo "Rolled back additive capability-router plugin and rules. OC v2 rules were not touched."
}

logs() {
  journalctl --user-unit=bifrost.service --since '30 minutes ago' --no-pager 2>/dev/null | \
    grep -E 'agent-capability-router|Agent CR|RoutingEngine' || true
}

usage() {
  echo "Usage: $0 {status|validate|build|test-candidate|apply|apply-rules|rollback|logs}" >&2
  exit 2
}

case "${1:-}" in
  status) status ;;
  validate) validate ;;
  build) build ;;
  test-candidate) test_candidate ;;
  apply) apply ;;
  apply-rules) apply_rules ;;
  rollback) rollback ;;
  logs) logs ;;
  *) usage ;;
esac
