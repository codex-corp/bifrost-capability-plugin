#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIFROST_APP_DIR="${BIFROST_APP_DIR:-$HOME/.config/bifrost}"
INSTALL_DIR="$ROOT_DIR/.local"
INSTALL_CONFIG="$INSTALL_DIR/install.json"

usage() {
  cat >&2 <<'EOF'
Usage:
  ./install.sh configure [--virtual-key-id <id>] [--virtual-key-name <name>] [--live]
  ./install.sh check

configure writes machine-specific settings to ignored .local/install.json.
Without --virtual-key-id it opens an interactive setup wizard. By default the
plugin is installed in shadow mode; --live enables routing.
EOF
  exit 2
}

configure() {
  local id="" name="" shadow=true live_set=false
  while (($#)); do
    case "$1" in
      --virtual-key-id) id="${2:-}"; shift 2 ;;
      --virtual-key-name) name="${2:-}"; shift 2 ;;
      --live) shadow=false; live_set=true; shift ;;
      *) usage ;;
    esac
  done
  if [[ -z "$id" ]]; then
    [[ -t 0 ]] || { echo "--virtual-key-id is required in non-interactive mode." >&2; exit 1; }
    echo "Bifrost Capability Router setup (Bedrock provider)"
    echo "Virtual Key documentation: https://docs.getbifrost.ai/features/governance/virtual-keys#configuration"
    echo
    echo "Available Virtual Keys (ID, name, status):"
    mapfile -t virtual_keys < <(python3 "$ROOT_DIR/scripts/inspect_db.py" list-virtual-keys "$BIFROST_APP_DIR/config.db")
    ((${#virtual_keys[@]})) || { echo "No Virtual Keys found in Bifrost." >&2; exit 1; }
    local index=1 row
    for row in "${virtual_keys[@]}"; do
      printf '  %d) %s\n' "$index" "${row//$'\t'/ | }"
      ((index += 1))
    done
    while :; do
      read -r -p "Select an active Virtual Key [1-${#virtual_keys[@]}]: " index
      [[ "$index" =~ ^[0-9]+$ ]] && ((index >= 1 && index <= ${#virtual_keys[@]})) || { echo "Choose a listed number." >&2; continue; }
      row="${virtual_keys[index-1]}"
      id="${row%%$'\t'*}"
      [[ "$(awk -F '\t' '{print $3}' <<<"$row")" == active ]] || { echo "Choose an active Virtual Key." >&2; continue; }
      [[ -n "$name" ]] || name="$(awk -F '\t' '{print $2}' <<<"$row")"
      break
    done
    if [[ "$live_set" == false ]]; then
      read -r -p "Enable live routing now? [y/N]: " answer
      [[ "$answer" =~ ^[Yy]$ ]] && shadow=false
    fi
  fi
  [[ "$id" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]] || {
    echo "A valid Virtual Key UUID is required." >&2
    exit 1
  }
  python3 "$ROOT_DIR/scripts/inspect_db.py" validate-models "$BIFROST_APP_DIR/config.db" "$ROOT_DIR/config/models.json"
  python3 "$ROOT_DIR/scripts/inspect_db.py" validate-virtual-key "$BIFROST_APP_DIR/config.db" "$ROOT_DIR/config/models.json" "$id"
  mkdir -p "$INSTALL_DIR"
  chmod 700 "$INSTALL_DIR"
  local temporary="$INSTALL_CONFIG.tmp"
  jq -n --arg id "$id" --arg name "$name" --argjson shadow "$shadow" \
    '{provider:"bedrock",virtual_key:{id:$id,name:$name},shadow_mode:$shadow}' >"$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$INSTALL_CONFIG"
  echo "Configuration saved to $INSTALL_CONFIG"
  echo "Next: ./router.sh validate"
}

check() {
  [[ -f "$INSTALL_CONFIG" ]] || { echo "Run ./install.sh configure first." >&2; exit 1; }
  jq -e '.provider == "bedrock" and (.virtual_key.id | length > 0) and (.shadow_mode | type == "boolean")' "$INSTALL_CONFIG" >/dev/null
  python3 "$ROOT_DIR/scripts/inspect_db.py" validate-models "$BIFROST_APP_DIR/config.db" "$ROOT_DIR/config/models.json"
  python3 "$ROOT_DIR/scripts/inspect_db.py" validate-virtual-key "$BIFROST_APP_DIR/config.db" "$ROOT_DIR/config/models.json" "$(jq -r '.virtual_key.id' "$INSTALL_CONFIG")"
  echo "Installation configuration is valid."
}

case "${1:-}" in
  configure) shift; configure "$@" ;;
  check) check ;;
  *) usage ;;
esac
