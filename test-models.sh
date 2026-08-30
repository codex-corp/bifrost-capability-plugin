#!/usr/bin/env bash
set -u

BIFROST_URL="${BIFROST_URL:-http://127.0.0.1:10020}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-60}"
MAX_RETRIES="${MAX_RETRIES:-3}"
RETRY_DELAY="${RETRY_DELAY:-2}"
RULE_PREFIX="${RULE_PREFIX:-Agent CR }"

: "${BIFROST_API_KEY:?Export BIFROST_API_KEY with a Bifrost Virtual Key before running this script}"

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

# Logging
log_info()    { echo -e "\\033[1;34m[INFO]\\033[0m $*" >&2; }
log_success() { echo -e "\\033[1;32m[SUCCESS]\\033[0m $*" >&2; }
log_warn()    { echo -e "\\033[1;33m[WARNING]\\033[0m $*" >&2; }
log_error()   { echo -e "\\033[1;31m[ERROR]\\033[0m $*" >&2; }

# Create temp files for the rule snapshot, primary models, and fallbacks.
rules_file="$(mktemp)"
main_models_file="$(mktemp)"
fallback_models_file="$(mktemp)"
trap 'rm -f "$rules_file" "$main_models_file" "$fallback_models_file"' EXIT

log_info "Fetching enabled '$RULE_PREFIX' routing rules from Bifrost..."

curl -fsS "$BIFROST_URL/api/routing/rules" > "$rules_file"

# Extract unique primary targets from the current Agent CR rule snapshot.
jq -r --arg prefix "$RULE_PREFIX" '
    .rules[]
    | select(.enabled == true and (.name | startswith($prefix)))
    | .targets[]
    | "\(.provider)/\(.model)"
  ' "$rules_file" | awk 'NF && !seen[$0]++' > "$main_models_file"

# Extract unique fallbacks from the same snapshot.
jq -r --arg prefix "$RULE_PREFIX" '
    .rules[]
    | select(.enabled == true and (.name | startswith($prefix)))
    | .fallbacks[]
  ' "$rules_file" | awk 'NF && !seen[$0]++' > "$fallback_models_file"

main_count=$(wc -l < "$main_models_file")
fallback_count=$(wc -l < "$fallback_models_file")

echo
echo "========================================="
echo "  BIFROST AGENT CR MODEL TEST"
echo "========================================="
echo "Main models: $main_count"
echo "Fallback models: $fallback_count"
echo

# Function to test a single model with retry logic
test_model() {
  local model="$1"
  local attempt=0
  local success=0
  local last_status=""
  local last_error=""

  while (( attempt < MAX_RETRIES )); do
    ((attempt++))
    local response_file
    response_file="$(mktemp)"

    local request_id="smoke-$(date +%s%N)-${attempt}"

    local metrics
    metrics=$(
      curl -sS \
        --connect-timeout 5 \
        --max-time "$TIMEOUT_SECONDS" \
        -o "$response_file" \
        -w '%{http_code}\t%{time_total}' \
        "$BIFROST_URL/openai/v1/responses" \
        -H "Authorization: Bearer $BIFROST_API_KEY" \
        -H "Content-Type: application/json" \
        -H "x-request-id: $request_id" \
        --data-binary "$(
          jq -nc --arg model "$model" '{
            model: $model,
            input: [{
              role: "user",
              content: [{ type: "input_text", text: "Reply with exactly OK" }]
            }],
            max_output_tokens: 40
          }'
        )" 2>/dev/null
    )
    local curl_exit=$?

    local http_code latency status returned_model

    if (( curl_exit == 28 )); then
      status="TIMEOUT"
      returned_model="-"
      http_code="408"
    elif (( curl_exit != 0 )); then
      status="CURL_ERROR_$curl_exit"
      returned_model="-"
      http_code="ERR"
    else
      IFS=$'\t' read -r http_code latency <<< "$metrics"

      status=$(
        jq -r '
          if .status then .status
          elif .error.type then .error.type
          else "UNKNOWN"
          end
        ' "$response_file" 2>/dev/null
      )

      returned_model=$(
        jq -r '.model // .extra_fields.resolved_model_used // "-"' "$response_file" 2>/dev/null
      )

      last_error=$(
        jq -r '.error.message // .error.error // .message // ""' "$response_file" 2>/dev/null
      )
    fi

    rm -f "$response_file"

    # Success if completed or incomplete (both mean model responded)
    if [[ "$status" == "completed" ]] || [[ "$status" == "incomplete" ]]; then
      success=1
      break
    fi

    last_status="$status"

    # Retry on transient errors
    if [[ "$status" == "TIMEOUT" ]] || [[ "$status" == "UNKNOWN" ]] || \
       [[ "$status" == "CURL_ERROR_"* ]] || [[ "$http_code" == "5"* ]] || \
       [[ "$http_code" == "429" ]] || [[ "$http_code" == "503" ]]; then
      if (( attempt < MAX_RETRIES )); then
        log_warn "Retry $model (attempt $attempt/$MAX_RETRIES): $status"
        sleep "$((RETRY_DELAY * attempt))"  # Exponential backoff
      fi
    else
      # Non-transient error, don't retry
      break
    fi
  done

  # Output: http_code|latency|status|returned_model|model|attempts
  echo "$http_code|$latency|$status|$returned_model|$model|$attempt"

  # Return success status for counting
  [[ "$success" == "1" ]]
}

# ========================================
# SECTION 1: Test MAIN (primary) models
# ========================================
echo "=============================================="
echo "  MAIN (Primary) MODELS"
echo "=============================================="

printf '%-50s | %-6s | %-10s | %-12s | %s\n' \
  "MODEL" "HTTP" "LATENCY" "STATUS" "RETURNED MODEL"

printf '%-50s-+-%-6s-+-%-10s-+-%-12s-+-%s\n' \
  "--------------------------------------------------" "------" "----------" "------------" "--------------"

main_passed=0
main_failed=0

while IFS= read -r model; do
  [[ -z "$model" ]] && continue

  result=$(test_model "$model")

  # Output format: http_code|latency|status|returned_model|model|attempts
  http_code=$(echo "$result" | cut -d'|' -f1)
  latency=$(echo "$result" | cut -d'|' -f2)
  status=$(echo "$result" | cut -d'|' -f3)
  returned=$(echo "$result" | cut -d'|' -f4)
  model_name=$(echo "$result" | cut -d'|' -f5)
  attempts=$(echo "$result" | cut -d'|' -f6)

  printf '%-50s | %-6s | %-10s | %-12s | %s\n' \
    "$model_name" "${http_code:-000}" "${latency:-$TIMEOUT_SECONDS}s" "$status" "$returned"

  # Accept both "completed" and "incomplete" as success (model ran, just may not have finished)
  if [[ "$status" == "completed" ]] || [[ "$status" == "incomplete" ]]; then
    ((main_passed++))
  else
    ((main_failed++))
  fi
done < "$main_models_file"

echo
echo "=============================================="
echo "  FALLBACK MODELS (Explicit Test)"
echo "=============================================="

printf '%-50s | %-6s | %-10s | %-12s | %s\n' \
  "MODEL" "HTTP" "LATENCY" "STATUS" "RETURNED MODEL"

printf '%-50s-+-%-6s-+-%-10s-+-%-12s-+-%s\n' \
  "--------------------------------------------------" "------" "----------" "------------" "--------------"

fallback_passed=0
fallback_failed=0

while IFS= read -r model; do
  [[ -z "$model" ]] && continue

  result=$(test_model "$model")

  # Output format: http_code|latency|status|returned_model|model|attempts
  http_code=$(echo "$result" | cut -d'|' -f1)
  latency=$(echo "$result" | cut -d'|' -f2)
  status=$(echo "$result" | cut -d'|' -f3)
  returned=$(echo "$result" | cut -d'|' -f4)
  model_name=$(echo "$result" | cut -d'|' -f5)
  attempts=$(echo "$result" | cut -d'|' -f6)

  printf '%-50s | %-6s | %-10s | %-12s | %s\n' \
    "$model_name" "${http_code:-000}" "${latency:-$TIMEOUT_SECONDS}s" "$status" "$returned"

  # Accept both "completed" and "incomplete" as success (model ran, just may not have finished)
  if [[ "$status" == "completed" ]] || [[ "$status" == "incomplete" ]]; then
    ((fallback_passed++))
  else
    ((fallback_failed++))
  fi
done < "$fallback_models_file"

# Summary
echo
echo "========================================="
log_info "=== SUMMARY ==="
log_info "Main models:    $main_passed passed, $main_failed failed (of $main_count)"
log_info "Fallback models: $fallback_passed passed, $fallback_failed failed (of $fallback_count)"

total_passed=$((main_passed + fallback_passed))
total_failed=$((main_failed + fallback_failed))
total=$((main_count + fallback_count))

if (( fallback_failed > 0 )); then
  log_error "✗ $fallback_failed fallback models FAILED - check fallback routing!"
  exit 1
elif (( main_failed > 0 )); then
  log_warn "⚠ $main_failed main models FAILED"
  exit 1
else
  log_success "✓ All $total_passed models passed!"
  exit 0
fi
