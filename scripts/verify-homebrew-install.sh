#!/usr/bin/env bash
# Guard for Groundskeeper Homebrew install wiring.
#
# Groundskeeper currently ships through install.sh and GoReleaser archives, not
# Homebrew. Until a tap exists, this verifies that release docs/config do not
# accidentally advertise a brew install path. If a tap is added later, set the
# GROUNDSKEEPER_HOMEBREW_TAP_OWNER and GROUNDSKEEPER_HOMEBREW_TAP_REPO env vars
# and this script will verify the public formula path.

set -euo pipefail

PROJECT_OWNER="potato-hash"
PROJECT_REPO="groundskeeper"
FORMULA_NAME="${GROUNDSKEEPER_HOMEBREW_FORMULA:-groundskeeper}"
TAP_OWNER="${GROUNDSKEEPER_HOMEBREW_TAP_OWNER:-}"
TAP_REPO="${GROUNDSKEEPER_HOMEBREW_TAP_REPO:-}"
FORMULA_PATH="Formula/${FORMULA_NAME}.rb"
RAW_BASE="https://raw.githubusercontent.com/${TAP_OWNER}/${TAP_REPO}/main"
RELEASES_API="https://api.github.com/repos/${PROJECT_OWNER}/${PROJECT_REPO}/releases/latest"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

fail() { echo -e "${RED}FAIL${NC}: $*" >&2; exit 1; }
pass() { echo -e "${GREEN}OK${NC}  : $*"; }

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root="${script_dir}/.."
readme="${repo_root}/README.md"
installer="${repo_root}/install.sh"
goreleaser="${repo_root}/.goreleaser.yml"

if [ -z "${TAP_OWNER}" ] || [ -z "${TAP_REPO}" ]; then
  for file in "${readme}" "${installer}"; do
    if grep -Eq "brew install .*${FORMULA_NAME}" "${file}"; then
      fail "${file#${repo_root}/} advertises Homebrew for ${FORMULA_NAME}, but no Groundskeeper tap is configured"
    fi
  done
  if grep -Eq '^[[:space:]]*brews:' "${goreleaser}"; then
    fail ".goreleaser.yml configures Homebrew, but verifier tap settings are missing"
  fi
  pass "Groundskeeper Homebrew tap is not configured; install.sh remains the public install path"
  exit 0
fi

# 1. Tap reachable
if ! curl -sf -o /dev/null "https://github.com/${TAP_OWNER}/${TAP_REPO}"; then
  fail "tap repo https://github.com/${TAP_OWNER}/${TAP_REPO} is not reachable"
fi
pass "tap repo reachable"

# 2. Formula exists
formula_url="${RAW_BASE}/${FORMULA_PATH}"
formula=$(curl -sf "${formula_url}") || fail "formula not found at ${formula_url}"
pass "formula exists at ${FORMULA_PATH}"

# 3. Version matches latest release
formula_version=$(printf '%s\n' "${formula}" | sed -nE 's/^[[:space:]]*version "([^"]+)".*/\1/p' | head -1)
[ -n "${formula_version}" ] || fail "could not parse formula version"

latest_tag=$(curl -sf "${RELEASES_API}" | sed -nE 's/.*"tag_name":[[:space:]]*"v?([^"]+)".*/\1/p' | head -1)
[ -n "${latest_tag}" ] || fail "could not fetch latest release tag"

if [ "${formula_version}" != "${latest_tag}" ]; then
  fail "formula version ${formula_version} != latest release ${latest_tag} (goreleaser publish step likely failed)"
fi
pass "formula version ${formula_version} matches latest release"

# 4. All URLs in formula resolve
urls=$(printf '%s\n' "${formula}" | sed -nE 's/^[[:space:]]*url "([^"]+)".*/\1/p')
[ -n "${urls}" ] || fail "no asset URLs found in formula"

while IFS= read -r url; do
  code=$(curl -sIL -o /dev/null -w "%{http_code}" "${url}")
  if [ "${code}" != "200" ]; then
    fail "asset ${url} returned HTTP ${code}"
  fi
  pass "asset reachable: ${url##*/}"
done <<< "${urls}"

# 5. README still advertises the same install command (catches doc drift)
tap_name="${TAP_REPO#homebrew-}"
install_command="brew install ${TAP_OWNER}/${tap_name}/${FORMULA_NAME}"
if ! grep -qF "${install_command}" "${readme}"; then
  fail "README.md no longer references '${install_command}'; tap is configured but advertised command drifted"
fi
pass "README install command matches tap"

echo
echo "All homebrew install path checks passed."
