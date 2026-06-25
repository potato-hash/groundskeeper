#!/usr/bin/env bash
# Post-install smoke check for the public Groundskeeper + OMP + Espalier path.
#
# Run after the one-command installer on a clean or half-configured machine:
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/scripts/verify-install-state.sh | bash
#
# The secret scan uses sensitive environment variable values already present in
# the process, but never prints those values. It reports only the env var name
# and file path if a persisted copy is found.

set -euo pipefail

failures=0

ok() { printf '[OK]   %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*"; }
fail() {
  printf '[FAIL] %s\n' "$*" >&2
  failures=$((failures + 1))
}

xdg_dir() {
  local env_name="$1"
  local fallback="$2"
  local base="${!env_name:-}"
  if [[ -z "$base" ]]; then
    base="$HOME/$fallback"
  fi
  printf '%s/groundskeeper\n' "$base"
}

find_executable() {
  local name="$1"
  shift
  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return 0
  fi
  local candidate
  for candidate in "$@"; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

resolve_espalier() {
  local path="${GK_ESPALIER_PATH:-$(xdg_dir XDG_DATA_HOME .local/share)/espalier}"
  if [[ -f "$path" ]]; then
    ESPALIER_ENTRYPOINT="$path"
    ESPALIER_ROOT="$(cd "$(dirname "$path")/../.." && pwd)"
  else
    ESPALIER_ROOT="$path"
    ESPALIER_ENTRYPOINT="$path/dist/extensions/index.js"
  fi
}

is_sensitive_env_name() {
  local upper
  upper="$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')"
  [[ "$upper" == *API_KEY* ||
     "$upper" == *TOKEN* ||
     "$upper" == *SECRET* ||
     "$upper" == *PASSWORD* ||
     "$upper" == *PRIVATE_KEY* ||
     "$upper" == *ACCESS_KEY* ]]
}

scan_secret_values() {
  local dirs=("$@")
  local scanned=0
  local found=0
  local name value dir hits

  while IFS='=' read -r name value; do
    if [[ -z "${name:-}" || ${#value} -lt 4 ]]; then
      continue
    fi
    if ! is_sensitive_env_name "$name"; then
      continue
    fi
    scanned=$((scanned + 1))
    for dir in "${dirs[@]}"; do
      [[ -e "$dir" ]] || continue
      hits="$(
        find "$dir" \
          \( -path '*/.git/*' -o -path '*/node_modules/*' \) -prune -o \
          -type f -exec grep -IlF -- "$value" {} + 2>/dev/null || true
      )"
      if [[ -n "$hits" ]]; then
        found=1
        while IFS= read -r hit; do
          [[ -n "$hit" ]] && fail "sensitive value from $name persisted in $hit"
        done <<< "$hits"
      fi
    done
  done < <(env)

  if [[ "$found" -eq 0 ]]; then
    if [[ "$scanned" -eq 0 ]]; then
      warn "no sensitive env values available to scan"
    else
      ok "secret persistence scan passed across ${#dirs[@]} dirs"
    fi
  fi
}

config_dir="$(xdg_dir XDG_CONFIG_HOME .config)"
data_dir="$(xdg_dir XDG_DATA_HOME .local/share)"
cache_dir="$(xdg_dir XDG_CACHE_HOME .cache)"
gk_db="${GK_DB_PATH:-$data_dir/gk.db}"

if gk_bin="$(find_executable "${GK_BIN:-groundskeeper}" "$HOME/.local/bin/groundskeeper" "/usr/local/bin/groundskeeper")"; then
  ok "groundskeeper binary: $gk_bin"
else
  fail "groundskeeper binary not found on PATH, ~/.local/bin, or /usr/local/bin"
fi

if omp_bin="$(find_executable "${OMP_BIN:-omp}" "$HOME/.local/bin/omp" "$HOME/.bun/bin/omp")"; then
  ok "omp binary: $omp_bin"
else
  fail "omp binary not found on PATH, ~/.local/bin, or ~/.bun/bin"
fi

resolve_espalier
if [[ -d "$ESPALIER_ROOT" ]]; then
  ok "Espalier checkout: $ESPALIER_ROOT"
else
  fail "Espalier checkout missing: $ESPALIER_ROOT"
fi
if [[ -d "$ESPALIER_ROOT/node_modules" ]]; then
  ok "Espalier dependencies: $ESPALIER_ROOT/node_modules"
else
  fail "Espalier dependencies missing: $ESPALIER_ROOT/node_modules"
fi
if [[ -f "$ESPALIER_ENTRYPOINT" ]]; then
  ok "Espalier extension built: $ESPALIER_ENTRYPOINT"
else
  fail "Espalier extension entrypoint missing: $ESPALIER_ENTRYPOINT"
fi

if [[ -f "$gk_db" ]]; then
  ok "Groundskeeper DB: $gk_db"
else
  fail "Groundskeeper DB missing: $gk_db"
fi

scan_secret_values \
  "$config_dir" \
  "$data_dir" \
  "$cache_dir" \
  "$ESPALIER_ROOT" \
  "$HOME/.omp"

if [[ "$failures" -gt 0 ]]; then
  printf '\nInstall-state verification failed with %d issue(s).\n' "$failures" >&2
  printf 'Re-run the installer with --run-setup, then re-run this script.\n' >&2
  exit 1
fi

printf '\nInstall-state verification passed.\n'
