#!/usr/bin/env bash
#
# Groundskeeper Installer
# https://github.com/potato-hash/groundskeeper
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/install.sh |
#     bash -s -- --non-interactive --skip-setup
#   export OLLAMA_CLOUD_API_KEY='<key>'
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/install.sh |
#     bash -s -- --non-interactive --run-setup --model ollama-cloud/glm-5.2 --verify-model
#
# Options:
#   --name <name>       Custom binary name (default: groundskeeper)
#   --dir <path>        Installation directory (default: ~/.local/bin)
#   --version <ver>     Specific version (default: latest)
#   --skip-tmux-config  Skip tmux configuration prompt
#   --run-setup         Run `groundskeeper setup` after installing (no prompt)
#   --skip-setup        Do not offer the first-run setup wizard
#   --model <model>     Model to pass to first-run setup
#   --memory-backend <name> Memory backend for setup: mnemopi or hindsight
#   --hindsight-url <url> Hindsight base URL for setup
#   --verify-model      Verify model access during first-run setup
#   --install-cua-driver Install Cua Driver for computer-use support
#   --non-interactive   Skip all prompts (for CI/automated installs)
#   --pkg-manager <mgr> macOS package manager: 'brew' or 'port' (default: auto-detect)
#
# The installer will:
#   1. Download and install the groundskeeper binary
#   2. Check for tmux (offer to install if missing) - REQUIRED
#   3. Check for jq (offer to install if missing) - Optional, for session forking
#   4. Configure ~/.tmux.conf for mouse scrolling & clipboard - Optional
#   5. Report stack prerequisites (git, bun, jj, omp)
#   6. Offer the first-run stack setup wizard (OMP + Espalier + model)
#   7. Install Cua Driver when explicitly requested
# Supported platforms:
#   - macOS (darwin) - arm64 (Apple Silicon), amd64 (Intel)
#   - Linux - arm64, amd64
#   - Windows - via WSL (uses Linux binary, clipboard via clip.exe)
#

# verify_download_checksum verifies that file $1 (a downloaded release asset
# named $2) matches the SHA-256 recorded for it in the checksums.txt body passed
# as $3. Defined at top level (outside main) so it is unit-testable in isolation
# (see internal/releasetests/issue1206_install_checksum_test.go). Fails closed:
#   return 0 -> verified         return 2 -> no checksum entry for this asset
#   return 1 -> hash mismatch     return 3 -> no sha256 tool available
# Security: without this, `curl | bash` would extract and run a tampered or
# MITM'd binary with no integrity check (audit H1).
verify_download_checksum() {
    local file="$1" asset="$2" checksums="$3"
    local expected actual

    # checksums.txt lines are "<hex><spaces><name>"; tolerate a "*" binary-mode
    # marker on the name (as emitted by `sha256sum -b`).
    expected=$(printf '%s\n' "$checksums" | awk -v a="$asset" \
        '{name=$2; sub(/^\*/,"",name); if (name==a) {print $1; exit}}')
    if [[ -z "$expected" ]]; then
        return 2
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$file" 2>/dev/null | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$file" 2>/dev/null | awk '{print $1}')
    else
        return 3
    fi

    # Case-insensitive hex compare without relying on bash 4 ${var,,}.
    expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
    actual=$(printf '%s' "$actual" | tr 'A-F' 'a-f')
    [[ -n "$actual" && "$expected" == "$actual" ]]
}

groundskeeper_release_asset_name() {
    local version_num="${1#v}" os="$2" arch="$3"
    printf 'groundskeeper_%s_%s_%s.tar.gz\n' "$version_num" "$os" "$arch"
}

release_json_has_asset() {
    local release_json="$1" asset="$2"
    printf '%s\n' "$release_json" |
        tr ',' '\n' |
        sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        grep -Fxq "$asset"
}

release_json_has_install_assets() {
    local release_json="$1" version="$2" os="$3" arch="$4"
    local asset
    asset="$(groundskeeper_release_asset_name "$version" "$os" "$arch")"
    release_json_has_asset "$release_json" "$asset" &&
        release_json_has_asset "$release_json" "checksums.txt"
}

# Wrap in main() so the entire script is read before execution.
# Without this, `curl | bash` can fail because `read` commands
# consume script bytes from stdin, or hit EOF with set -e.
main() {

set -e

# Read user input from the terminal, even when script is piped (curl | bash).
# Falls back to stdin when already running interactively.
prompt_read() {
    if [[ -t 0 ]]; then
        read "$@"
    else
        read "$@" </dev/tty || true
    fi
}

has_prompt_tty() {
    [[ -t 0 ]] || { : </dev/tty; } 2>/dev/null
}

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Defaults
BINARY_NAME="groundskeeper"
INSTALL_DIR="${HOME}/.local/bin"
VERSION="latest"
REPO="potato-hash/groundskeeper"
SKIP_TMUX_CONFIG=false
SKIP_OPTIONAL_DEPS=false
INSTALL_CUA_DRIVER=false

SETUP_MODE="prompt"  # prompt, run, or skip
RUN_SETUP_REQUESTED=false
SKIP_SETUP_REQUESTED=false
SETUP_MODEL=""
SETUP_MEMORY_BACKEND=""
SETUP_HINDSIGHT_URL=""
VERIFY_MODEL=false
LATEST_RELEASE_CHECKED=false
LATEST_RELEASE_TAG=""
LATEST_RELEASE_JSON=""
SOURCE_BUILD_MIN_GO_VERSION="1.25.11"
# macOS package manager configuration
MACOS_SUPPORTED_PKG_MGRS=("brew" "port")  # Order matters for preference
MACOS_PKG_MANAGER=""  # Will be auto-detected or set by user

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --name)
            BINARY_NAME="$2"
            shift 2
            ;;
        --dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --version)
            VERSION="$2"
            shift 2
            ;;
        --skip-tmux-config)
            SKIP_TMUX_CONFIG=true
            shift
            ;;
        --run-setup)
            RUN_SETUP_REQUESTED=true
            shift
            ;;
        --skip-setup)
            SKIP_SETUP_REQUESTED=true
            shift
            ;;
        --model)
            if [[ -z "${2:-}" || "${2:0:1}" == "-" ]]; then
                echo -e "${RED}Error: --model requires a value${NC}"
                exit 1
            fi
            SETUP_MODEL="$2"
            shift 2
            ;;
        --memory-backend)
            if [[ -z "${2:-}" || "${2:0:1}" == "-" ]]; then
                echo -e "${RED}Error: --memory-backend requires a value (mnemopi or hindsight)${NC}"
                exit 1
            fi
            SETUP_MEMORY_BACKEND="$2"
            shift 2
            ;;
        --hindsight-url)
            if [[ -z "${2:-}" || "${2:0:1}" == "-" ]]; then
                echo -e "${RED}Error: --hindsight-url requires a value${NC}"
                exit 1
            fi
            SETUP_HINDSIGHT_URL="$2"
            shift 2
            ;;
        --verify-model)
            VERIFY_MODEL=true
            shift
            ;;
        --install-cua-driver)
            INSTALL_CUA_DRIVER=true
            shift
            ;;
        --non-interactive)
            SKIP_TMUX_CONFIG=true
            SKIP_OPTIONAL_DEPS=true
            shift
            ;;
        --pkg-manager)
            if [[ -z "${2:-}" || "${2:0:1}" == "-" ]]; then
                echo -e "${RED}Error: --pkg-manager requires a value (${MACOS_SUPPORTED_PKG_MGRS[*]})${NC}"
                exit 1
            fi
            MACOS_PKG_MANAGER="$2"
            # Validate against supported package managers
            valid=false
            for mgr in "${MACOS_SUPPORTED_PKG_MGRS[@]}"; do
                if [[ "$MACOS_PKG_MANAGER" == "$mgr" ]]; then
                    valid=true
                    break
                fi
            done
            if [[ "$valid" != "true" ]]; then
                echo -e "${RED}Error: --pkg-manager must be one of: ${MACOS_SUPPORTED_PKG_MGRS[*]}${NC}"
                exit 1
            fi
            shift 2
            ;;
        -h|--help)
            echo "Groundskeeper Stack Installer"
            echo ""
            echo "Usage: install.sh [options]"
            echo ""
            echo "Options:"
            echo "  --name <name>       Custom binary name (default: groundskeeper)"
            echo "  --dir <path>        Installation directory (default: ~/.local/bin)"
            echo "  --version <ver>     Specific version (default: latest)"
            echo "  --skip-tmux-config  Skip tmux configuration prompt"
            echo "  --run-setup         Run 'groundskeeper setup' after installing (no prompt)"
            echo "  --skip-setup        Do not offer the first-run setup wizard"
            echo "  --model <model>     Model to pass to first-run setup"
            echo "  --memory-backend <name> Memory backend for setup: mnemopi or hindsight"
            echo "  --hindsight-url <url> Hindsight base URL for setup"
            echo "  --verify-model      Verify model access during first-run setup"
            echo "  --install-cua-driver Install Cua Driver for computer-use support"
            echo "  --non-interactive   Skip all prompts (for CI/automated installs)"
            echo "  --pkg-manager <mgr> macOS package manager: ${MACOS_SUPPORTED_PKG_MGRS[*]} (default: auto-detect)"
            echo "  -h, --help          Show this help message"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done
if [[ "$RUN_SETUP_REQUESTED" == "true" && "$SKIP_SETUP_REQUESTED" == "true" ]]; then
    echo -e "${RED}Error: --run-setup and --skip-setup cannot be used together${NC}"
    exit 1
fi
if [[ "$RUN_SETUP_REQUESTED" == "true" ]]; then
    SETUP_MODE="run"
elif [[ "$SKIP_SETUP_REQUESTED" == "true" || "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
    SETUP_MODE="skip"
fi
echo -e "${BLUE}╔════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║     Groundskeeper Stack Installer      ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════╝${NC}"
echo ""

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
IS_WSL=false
case "$OS" in
    darwin) OS="darwin" ;;
    linux)
        OS="linux"
        # Detect WSL (Windows Subsystem for Linux)
        if grep -qi microsoft /proc/version 2>/dev/null || [[ -n "$WSL_DISTRO_NAME" ]]; then
            IS_WSL=true
        fi
        ;;
    *)
        echo -e "${RED}Error: Unsupported operating system: $OS${NC}"
        echo "Groundskeeper only supports macOS and Linux."
        exit 1
        ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
        echo -e "${RED}Error: Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

if [[ "$IS_WSL" == "true" ]]; then
    echo -e "Detected: ${GREEN}${OS}/${ARCH}${NC} (WSL - Windows Subsystem for Linux)"
else
    echo -e "Detected: ${GREEN}${OS}/${ARCH}${NC}"
fi

# macOS-specific package manager configuration
if [[ "$OS" == "darwin" ]]; then
    # Bash 3.2 compatibility: use case-based helpers instead of associative arrays.
    macos_pkg_mgr_name() {
        case "$1" in
            brew) echo "Homebrew" ;;
            port) echo "MacPorts" ;;
            *) return 1 ;;
        esac
    }

    macos_pkg_mgr_command() {
        case "$1" in
            brew) echo "brew" ;;
            port) echo "port" ;;
            *) return 1 ;;
        esac
    }

    macos_pkg_mgr_install_cmd() {
        case "$1" in
            brew) echo "brew install" ;;
            port) echo "sudo port install" ;;
            *) return 1 ;;
        esac
    }

    macos_pkg_mgr_link() {
        case "$1" in
            brew) echo "https://brew.sh" ;;
            port) echo "https://www.macports.org/install.php" ;;
            *) return 1 ;;
        esac
    }
fi

# Detect or select macOS package manager
detect_macos_package_manager() {
    # If user specified a package manager, verify it's available
    if [[ -n "$MACOS_PKG_MANAGER" ]]; then
        local cmd
        cmd="$(macos_pkg_mgr_command "$MACOS_PKG_MANAGER")"
        local name
        name="$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")"
        local link
        link="$(macos_pkg_mgr_link "$MACOS_PKG_MANAGER")"

        if ! command -v "$cmd" &> /dev/null; then
            echo -e "${RED}Error: $name not found but --pkg-manager=$MACOS_PKG_MANAGER was specified${NC}"
            echo "Install $name: $link"
            exit 1
        fi
        echo -e "Package manager: ${GREEN}${name}${NC} (user specified)"
        return
    fi

    # Auto-detect: check for available package managers
    local available_mgrs=()
    for mgr in "${MACOS_SUPPORTED_PKG_MGRS[@]}"; do
        if command -v "$(macos_pkg_mgr_command "$mgr")" &> /dev/null; then
            available_mgrs+=("$mgr")
        fi
    done

    # Handle based on how many are available
    if [[ ${#available_mgrs[@]} -eq 0 ]]; then
        # None available
        MACOS_PKG_MANAGER=""
        echo -e "${YELLOW}No package manager detected (Homebrew or MacPorts)${NC}"
        echo "You'll need to install dependencies manually or install a package manager first:"
        for mgr in "${MACOS_SUPPORTED_PKG_MGRS[@]}"; do
            echo "  • $(macos_pkg_mgr_name "$mgr"): $(macos_pkg_mgr_link "$mgr")"
        done
    elif [[ ${#available_mgrs[@]} -eq 1 ]]; then
        # Only one available
        MACOS_PKG_MANAGER="${available_mgrs[0]}"
        echo -e "Package manager: ${GREEN}$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")${NC} (auto-detected)"
    else
        # Multiple available - ask user to choose
        echo -e "${YELLOW}Multiple package managers are installed.${NC}"
        if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
            # Non-interactive mode: use first in preference order
            MACOS_PKG_MANAGER="${available_mgrs[0]}"
            echo -e "Package manager: ${GREEN}$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")${NC} (auto-selected in non-interactive mode)"
        else
            echo "Which package manager would you like to use?"
            local i=1
            for mgr in "${available_mgrs[@]}"; do
                echo "  $i) $(macos_pkg_mgr_name "$mgr") ($mgr)"
                ((i++))
            done
            prompt_read -p "Enter choice [1-${#available_mgrs[@]}]: " -n 1 -r
            echo

            local choice=$((REPLY - 1))
            if [[ $choice -ge 0 && $choice -lt ${#available_mgrs[@]} ]]; then
                MACOS_PKG_MANAGER="${available_mgrs[$choice]}"
                echo -e "Package manager: ${GREEN}$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")${NC}"
            else
                echo -e "${YELLOW}Invalid choice, defaulting to $(macos_pkg_mgr_name "${available_mgrs[0]}")${NC}"
                MACOS_PKG_MANAGER="${available_mgrs[0]}"
            fi
        fi
    fi
}

# Detect package manager on macOS
if [[ "$OS" == "darwin" ]]; then
    detect_macos_package_manager
fi

# Helper function to install packages on macOS
# Usage: install_macos_package <package_name>
# Note: Assumes package has same name across all package managers
# Prerequisite: MACOS_PKG_MANAGER must be set (validated by detect_macos_package_manager)
install_macos_package() {
    local PACKAGE_NAME="$1"
    local mgr_name
    mgr_name="$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")"

    echo -e "Installing $PACKAGE_NAME via $mgr_name..."
    case "$MACOS_PKG_MANAGER" in
        brew) brew install "$PACKAGE_NAME" ;;
        port)
            if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
                sudo -n port install "$PACKAGE_NAME"
            else
                sudo port install "$PACKAGE_NAME"
            fi
            ;;
    esac
}

# Helper function to print manual install commands on macOS
print_macos_manual_install_help() {
    local package_name="$1"
    echo "Install $package_name manually with one of:"
    for mgr in "${MACOS_SUPPORTED_PKG_MGRS[@]}"; do
        echo "  $(macos_pkg_mgr_install_cmd "$mgr") $package_name"
    done
}

github_api_curl() {
    local url="$1"
    local curl_args=(-fsSL)
    if [[ -n "${GITHUB_TOKEN:-}" ]]; then
        curl_args+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
    fi
    curl "${curl_args[@]}" "$url"
}

fetch_latest_release_tag() {
    if [[ "$LATEST_RELEASE_CHECKED" != "true" ]]; then
        LATEST_RELEASE_JSON=$(github_api_curl "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
        LATEST_RELEASE_TAG=$(printf '%s\n' "$LATEST_RELEASE_JSON" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' || true)
        LATEST_RELEASE_CHECKED=true
    fi
}

latest_release_installable() {
    fetch_latest_release_tag
    [[ -n "$LATEST_RELEASE_TAG" ]] || return 1
    release_json_has_install_assets "$LATEST_RELEASE_JSON" "$LATEST_RELEASE_TAG" "$OS" "$ARCH"
}

latest_release_unavailable_reason() {
    if [[ -z "$LATEST_RELEASE_TAG" ]]; then
        echo "No latest release found"
        return
    fi
    echo "Latest release ${LATEST_RELEASE_TAG} is missing $(groundskeeper_release_asset_name "$LATEST_RELEASE_TAG" "$OS" "$ARCH") or checksums.txt"
}

print_source_build_go_help() {
    echo "Pre-release installs fall back to building github.com/${REPO}/cmd/groundskeeper@main."
    echo "Install Go ${SOURCE_BUILD_MIN_GO_VERSION} or newer, then re-run the same install command."
    if [[ "$OS" == "darwin" ]]; then
        if [[ -n "$MACOS_PKG_MANAGER" ]]; then
            echo "  $(macos_pkg_mgr_install_cmd "$MACOS_PKG_MANAGER") go"
        else
            print_macos_manual_install_help "go"
        fi
    else
        echo "  https://go.dev/dl/"
        echo "  or your distro package, e.g. sudo apt install golang-go"
    fi
}

installed_go_version() {
    command -v go >/dev/null 2>&1 || return 1

    local version
    version=$(go env GOVERSION 2>/dev/null || true)
    if [[ -z "$version" ]]; then
        version=$(go version 2>/dev/null || true)
        version="${version#go version }"
        version="${version%% *}"
    fi
    version="${version#go}"
    version="${version%%[!0-9.]*}"
    [[ -n "$version" ]] || return 1
    echo "$version"
}

go_version_at_least() {
    local have="$1"
    local want="$2"
    local h_major h_minor h_patch w_major w_minor w_patch
    IFS=. read -r h_major h_minor h_patch <<< "$have"
    IFS=. read -r w_major w_minor w_patch <<< "$want"
    h_minor="${h_minor:-0}"
    h_patch="${h_patch:-0}"
    w_minor="${w_minor:-0}"
    w_patch="${w_patch:-0}"

    for part in "$h_major" "$h_minor" "$h_patch" "$w_major" "$w_minor" "$w_patch"; do
        [[ "$part" =~ ^[0-9]+$ ]] || return 1
    done

    h_major=$((10#$h_major))
    h_minor=$((10#$h_minor))
    h_patch=$((10#$h_patch))
    w_major=$((10#$w_major))
    w_minor=$((10#$w_minor))
    w_patch=$((10#$w_patch))

    (( h_major > w_major )) && return 0
    (( h_major < w_major )) && return 1
    (( h_minor > w_minor )) && return 0
    (( h_minor < w_minor )) && return 1
    (( h_patch >= w_patch ))
}

source_build_go_ok() {
    local version
    version=$(installed_go_version) || return 1
    go_version_at_least "$version" "$SOURCE_BUILD_MIN_GO_VERSION"
}

preflight_source_build_prereq() {
    [[ "$VERSION" == "latest" ]] || return 0

    fetch_latest_release_tag
    latest_release_installable && return 0

    local go_version
    if go_version=$(installed_go_version); then
        if go_version_at_least "$go_version" "$SOURCE_BUILD_MIN_GO_VERSION"; then
            return 0
        fi
        echo -e "${RED}Error: $(latest_release_unavailable_reason), and Go ${go_version} is too old.${NC}"
        echo "Groundskeeper source builds require Go ${SOURCE_BUILD_MIN_GO_VERSION} or newer."
    else
        echo -e "${RED}Error: $(latest_release_unavailable_reason), and Go is not installed.${NC}"
    fi
    print_source_build_go_help
    exit 1
}

preflight_source_build_prereq

# Check for tmux and offer to install
if ! command -v tmux &> /dev/null; then
    echo -e "${YELLOW}tmux is not installed.${NC}"
    echo "Groundskeeper requires tmux to function."
    echo ""

    # Try to auto-install tmux
    if [[ "$OS" == "darwin" ]]; then
        if [[ -n "$MACOS_PKG_MANAGER" ]]; then
            mgr_name="$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")"
            if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
                echo -e "Installing tmux via $mgr_name (non-interactive)..."
                if ! install_macos_package "tmux"; then
                    echo -e "${YELLOW}Warning: automatic tmux install failed in non-interactive mode.${NC}"
                fi
            else
                prompt_read -p "Install tmux via $mgr_name? [Y/n] " -n 1 -r
                echo
                if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                    install_macos_package "tmux"
                fi
            fi
        else
            print_macos_manual_install_help "tmux"
        fi
    else
        # Linux - try apt, dnf, or pacman
        if command -v apt-get &> /dev/null; then
            if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
                echo -e "Installing tmux via apt (non-interactive)..."
                if ! { sudo -n apt-get update && sudo -n apt-get install -y tmux; }; then
                    echo -e "${YELLOW}Warning: automatic tmux install via apt failed in non-interactive mode.${NC}"
                fi
            else
                prompt_read -p "Install tmux via apt? [Y/n] " -n 1 -r
                echo
                if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                    echo -e "Installing tmux..."
                    sudo apt-get update && sudo apt-get install -y tmux
                fi
            fi
        elif command -v dnf &> /dev/null; then
            if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
                echo -e "Installing tmux via dnf (non-interactive)..."
                if ! sudo -n dnf install -y tmux; then
                    echo -e "${YELLOW}Warning: automatic tmux install via dnf failed in non-interactive mode.${NC}"
                fi
            else
                prompt_read -p "Install tmux via dnf? [Y/n] " -n 1 -r
                echo
                if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                    echo -e "Installing tmux..."
                    sudo dnf install -y tmux
                fi
            fi
        elif command -v pacman &> /dev/null; then
            if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
                echo -e "Installing tmux via pacman (non-interactive)..."
                if ! sudo -n pacman -S --noconfirm tmux; then
                    echo -e "${YELLOW}Warning: automatic tmux install via pacman failed in non-interactive mode.${NC}"
                fi
            else
                prompt_read -p "Install tmux via pacman? [Y/n] " -n 1 -r
                echo
                if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                    echo -e "Installing tmux..."
                    sudo pacman -S --noconfirm tmux
                fi
            fi
        else
            echo "Please install tmux manually:"
            echo "  sudo apt install tmux    # Debian/Ubuntu"
            echo "  sudo dnf install tmux    # Fedora"
            echo "  sudo pacman -S tmux      # Arch"
        fi
    fi

    # Check again after attempted install
    if ! command -v tmux &> /dev/null; then
        if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
            echo -e "${RED}Error: tmux is required but was not found after automatic install attempts.${NC}"
            echo "Install tmux, then re-run the same install command."
            exit 1
        else
            echo ""
            prompt_read -p "tmux not found. Continue anyway? [y/N] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                exit 1
            fi
        fi
    else
        echo -e "${GREEN}tmux installed successfully!${NC}"
    fi
fi

# Check for jq (required for Claude session forking)
if ! command -v jq &> /dev/null && [[ "$SKIP_OPTIONAL_DEPS" != "true" ]]; then
    echo -e "${YELLOW}jq is not installed (optional but recommended).${NC}"
    echo "jq is required for Claude session forking/session ID capture."
    echo ""

    # Try to auto-install jq
    if [[ "$OS" == "darwin" ]]; then
        if [[ -n "$MACOS_PKG_MANAGER" ]]; then
            mgr_name="$(macos_pkg_mgr_name "$MACOS_PKG_MANAGER")"
            prompt_read -p "Install jq via $mgr_name? [Y/n] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                install_macos_package "jq"
            fi
        else
            print_macos_manual_install_help "jq"
        fi
    else
        if command -v apt-get &> /dev/null; then
            prompt_read -p "Install jq via apt? [Y/n] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                echo -e "Installing jq..."
                sudo apt-get install -y jq
            fi
        elif command -v dnf &> /dev/null; then
            prompt_read -p "Install jq via dnf? [Y/n] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                echo -e "Installing jq..."
                sudo dnf install -y jq
            fi
        elif command -v pacman &> /dev/null; then
            prompt_read -p "Install jq via pacman? [Y/n] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                echo -e "Installing jq..."
                sudo pacman -S --noconfirm jq
            fi
        else
            echo "Install jq manually for session forking support."
        fi
    fi

    if command -v jq &> /dev/null; then
        echo -e "${GREEN}jq installed successfully!${NC}"
    fi
fi

# Get version
INSTALLED_FROM_SOURCE=false
if [[ "$VERSION" == "latest" ]]; then
    echo -e "Fetching latest version..."
    fetch_latest_release_tag
    if latest_release_installable; then
        VERSION="$LATEST_RELEASE_TAG"
    else
        VERSION=""
    fi
    if [[ -z "$VERSION" ]]; then
        latest_reason="$(latest_release_unavailable_reason)"
        if [[ -f "go.mod" && -d "cmd/groundskeeper" ]] && source_build_go_ok; then
            echo -e "${YELLOW}${latest_reason}; building from local source checkout.${NC}"
            mkdir -p "$INSTALL_DIR"
            echo -e "Installing to ${GREEN}${INSTALL_DIR}/${BINARY_NAME}${NC}"
            go build -o "$INSTALL_DIR/$BINARY_NAME" ./cmd/groundskeeper
            chmod +x "$INSTALL_DIR/$BINARY_NAME"
            INSTALLED_FROM_SOURCE=true
        elif source_build_go_ok; then
            echo -e "${YELLOW}${latest_reason}; building from public source module.${NC}"
            mkdir -p "$INSTALL_DIR"
            echo -e "Installing to ${GREEN}${INSTALL_DIR}/${BINARY_NAME}${NC}"
            GOPROXY=direct GOBIN="$INSTALL_DIR" go install "github.com/${REPO}/cmd/groundskeeper@main"
            if [[ "$BINARY_NAME" != "groundskeeper" ]]; then
                mv -f "$INSTALL_DIR/groundskeeper" "$INSTALL_DIR/$BINARY_NAME"
            fi
            chmod +x "$INSTALL_DIR/$BINARY_NAME"
            INSTALLED_FROM_SOURCE=true
        else
            echo -e "${RED}Error: Could not determine latest version${NC}"
            echo "Please specify a version with --version"
            echo "Or run from a source checkout with Go installed to build locally."
            echo "Or install Go so the public module fallback can build from GitHub."
            exit 1
        fi
    fi
fi

if [[ "$INSTALLED_FROM_SOURCE" != "true" ]]; then
    # Remove 'v' prefix if present for URL
    VERSION_NUM="${VERSION#v}"
    echo -e "Installing version: ${GREEN}${VERSION}${NC}"

    # Download URL
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/groundskeeper_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
    echo -e "Downloading from: ${BLUE}${DOWNLOAD_URL}${NC}"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT

    # Download and extract
    echo -e "Downloading..."
    if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/groundskeeper.tar.gz"; then
    echo -e "${RED}Error: Download failed${NC}"
    echo "URL: $DOWNLOAD_URL"
    echo ""

    # Check if the release exists but has no assets (common when GoReleaser hasn't completed yet)
    RELEASE_JSON=$(github_api_curl "https://api.github.com/repos/${REPO}/releases/tags/${VERSION}" 2>/dev/null || true)

    # Parse asset list: prefer jq for reliability, fall back to grep
    if command -v jq &> /dev/null && [[ -n "$RELEASE_JSON" ]]; then
        ASSET_NAMES=$(printf '%s\n' "$RELEASE_JSON" | jq -r '.assets[]?.name // empty' 2>/dev/null || true)
        ASSET_COUNT=$(printf '%s\n' "$RELEASE_JSON" | jq '.assets | length' 2>/dev/null || true)
    else
        ASSET_NAMES=$(printf '%s\n' "$RELEASE_JSON" | grep '"name"' | sed 's/.*"name": *"\([^"]*\)".*/\1/' | grep '\.tar\.gz\|checksums' || true)
        ASSET_COUNT=$(printf '%s\n' "$RELEASE_JSON" | tr ',' '\n' | grep -c '"browser_download_url"' || true)
    fi
    if ! [[ "$ASSET_COUNT" =~ ^[0-9]+$ ]]; then
        ASSET_COUNT=0
    fi

    if [[ "$ASSET_COUNT" -eq 0 ]]; then
        echo "The release ${VERSION} exists but has no downloadable binaries."
        echo "This usually means the release CI workflow hasn't completed yet."
        echo "Wait a few minutes and try again, or check: https://github.com/${REPO}/actions"
    else
        # Release has assets, but not for this platform
        echo "The release ${VERSION} has ${ASSET_COUNT} assets, but not for ${OS}/${ARCH}."
        if [[ -n "$ASSET_NAMES" ]]; then
            echo ""
            echo "Available assets:"
            echo "$ASSET_NAMES" | while IFS= read -r name; do
                [[ -n "$name" ]] && echo "  - $name"
            done
        fi
        echo ""
        echo "This could mean:"
        echo "  - The version doesn't exist for your platform"
        echo "  - Network issues"
    fi
    echo ""

    echo "Build from source:"
    echo "  git clone https://github.com/${REPO}.git"
    echo "  cd groundskeeper && make install"
    exit 1
    fi

    # Verify SHA-256 before extracting/running the downloaded binary (audit H1).
    # goreleaser publishes checksums.txt alongside the archives (.goreleaser.yml
    # checksum.name_template). Fail closed: if checksums.txt cannot be fetched, the
    # asset is absent from it, or the hash mismatches, abort WITHOUT extracting.
    echo -e "Verifying checksum..."
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
    ASSET_NAME="groundskeeper_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
    CHECKSUMS=$(curl -fsSL "$CHECKSUMS_URL" 2>/dev/null || true)
    if [[ -z "$CHECKSUMS" ]]; then
    echo -e "${RED}Error: could not fetch checksums.txt for ${VERSION}${NC}"
    echo "Refusing to install an unverified binary. URL: $CHECKSUMS_URL"
    exit 1
    fi
    if verify_download_checksum "$TMP_DIR/groundskeeper.tar.gz" "$ASSET_NAME" "$CHECKSUMS"; then
    echo -e "${GREEN}Checksum verified.${NC}"
    else
    rc=$?
    case "$rc" in
        2) echo -e "${RED}Error: no published SHA-256 for ${ASSET_NAME} in checksums.txt${NC}" ;;
        3) echo -e "${RED}Error: no sha256sum/shasum tool available to verify the download${NC}" ;;
        *) echo -e "${RED}Error: SHA-256 mismatch for ${ASSET_NAME}${NC}" ;;
    esac
    echo "Refusing to install a tampered or corrupt artifact."
    exit 1
    fi

    echo -e "Extracting..."
    tar -xzf "$TMP_DIR/groundskeeper.tar.gz" -C "$TMP_DIR"

    # Create install directory
    mkdir -p "$INSTALL_DIR"

    # Install binary
    echo -e "Installing to ${GREEN}${INSTALL_DIR}/${BINARY_NAME}${NC}"
    mv "$TMP_DIR/groundskeeper" "$INSTALL_DIR/$BINARY_NAME"
    chmod +x "$INSTALL_DIR/$BINARY_NAME"
fi

# Check if install directory is in PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo -e "${YELLOW}Note: ${INSTALL_DIR} is not in your PATH${NC}"
    echo ""
    echo "Add it to your shell config:"
    echo ""
    if [[ -f "$HOME/.zshrc" ]]; then
        echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc"
        echo "  source ~/.zshrc"
    elif [[ -f "$HOME/.bashrc" ]]; then
        echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc"
        echo "  source ~/.bashrc"
    else
        echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    fi
    echo ""
fi

# Configure tmux for optimal Groundskeeper experience
configure_tmux() {
    local TMUX_CONF="$HOME/.tmux.conf"
    local MARKER="# Groundskeeper configuration"
    local VERSION_MARKER="# groundskeeper-tmux-config-version:"
    local CURRENT_VERSION="4"  # Bump this when config changes
    local NEEDS_UPDATE=false
    local HAS_CONFIG=false

    # Check if already configured and if update is needed
    if [[ -f "$TMUX_CONF" ]] && grep -q "$MARKER" "$TMUX_CONF" 2>/dev/null; then
        HAS_CONFIG=true
        # Check version
        local INSTALLED_VERSION=$(grep "$VERSION_MARKER" "$TMUX_CONF" 2>/dev/null | sed "s/.*$VERSION_MARKER//" | tr -d ' ')
        if [[ -z "$INSTALLED_VERSION" || "$INSTALLED_VERSION" -lt "$CURRENT_VERSION" ]]; then
            NEEDS_UPDATE=true
            echo ""
            echo -e "${YELLOW}tmux config update available!${NC}"
            if [[ -z "$INSTALLED_VERSION" ]]; then
                echo "Your current Groundskeeper tmux config is from an older version."
            else
                echo "Installed version: $INSTALLED_VERSION, Available: $CURRENT_VERSION"
            fi
            echo ""
            echo -e "${BLUE}What's new in this update:${NC}"
            echo "  • Deliver modified keys in csi-u form so Shift+Enter works (kitty etc.)"
            echo "  • Added extended-keys for Shift+Enter support (tmux 3.2+)"
            echo "  • Fixed mouse scrolling issues on WSL"
            echo "  • Added auto-enter copy-mode on scroll up"
            echo "  • Added explicit scroll bindings for copy-mode"
            echo "  • Improved terminal compatibility"
            echo ""
            prompt_read -p "Update tmux configuration? [Y/n] " -n 1 -r
            echo
            if [[ $REPLY =~ ^[Nn]$ ]]; then
                echo "Skipping tmux config update."
                return 0
            fi
            # Remove old config block
            echo "Removing old configuration..."
            # Use temp file for compatibility (BSD sed vs GNU sed)
            local TEMP_CONF=$(mktemp)
            sed "/$MARKER/,/# End Groundskeeper configuration/d" "$TMUX_CONF" > "$TEMP_CONF"
            mv "$TEMP_CONF" "$TMUX_CONF"
            echo -e "${GREEN}Old config removed${NC}"
        else
            echo -e "${GREEN}tmux already configured for Groundskeeper (v$INSTALLED_VERSION)${NC}"
            return 0
        fi
    fi

    echo ""
    echo -e "${BLUE}tmux Configuration${NC}"
    echo "Groundskeeper works best with mouse scroll and clipboard support."
    echo ""

    if [[ -f "$TMUX_CONF" ]] && [[ "$NEEDS_UPDATE" != "true" ]]; then
        echo -e "Found existing config: ${YELLOW}~/.tmux.conf${NC}"
        echo "The following settings will be APPENDED (your existing config is preserved):"
    elif [[ "$NEEDS_UPDATE" == "true" ]]; then
        echo "Installing updated configuration..."
    else
        echo "No ~/.tmux.conf found. The following settings will be created:"
    fi

    echo ""
    echo -e "${BLUE}  • Mouse scrolling & drag-to-copy (WSL compatible)${NC}"
    echo -e "${BLUE}  • Auto copy-mode on scroll up${NC}"
    echo -e "${BLUE}  • Clipboard integration (copy to system clipboard)${NC}"
    echo -e "${BLUE}  • 256-color terminal support${NC}"
    echo -e "${BLUE}  • 10,000 line history${NC}"
    echo ""

    # Skip prompt if we're updating (user already confirmed)
    if [[ "$NEEDS_UPDATE" != "true" ]]; then
        prompt_read -p "Configure tmux for Groundskeeper? [Y/n] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Nn]$ ]]; then
            echo "Skipping tmux configuration."
            echo "You can manually add the config later (see: Groundskeeper docs)"
            return 0
        fi
    fi

    # Determine clipboard command based on OS
    local CLIPBOARD_CMD
    if [[ "$OS" == "darwin" ]]; then
        CLIPBOARD_CMD="pbcopy"
    elif [[ "$IS_WSL" == "true" ]]; then
        # WSL: Use Windows clip.exe for clipboard integration
        CLIPBOARD_CMD="clip.exe"
        echo -e "${GREEN}WSL detected:${NC} Using Windows clipboard (clip.exe)"
    else
        # Linux - prefer xclip, fallback to xsel, or wl-copy for Wayland
        if [[ -n "$WAYLAND_DISPLAY" ]] && command -v wl-copy &> /dev/null; then
            CLIPBOARD_CMD="wl-copy"
        elif command -v xclip &> /dev/null; then
            CLIPBOARD_CMD="xclip -in -selection clipboard"
        elif command -v xsel &> /dev/null; then
            CLIPBOARD_CMD="xsel --clipboard --input"
        else
            echo -e "${YELLOW}Note: No clipboard tool found (xclip/xsel/wl-copy)${NC}"
            echo "Install with: sudo apt install xclip"
            CLIPBOARD_CMD="xclip -in -selection clipboard"
        fi
    fi

    # Create the config block
    # Note: WSL requires explicit scroll bindings; set-clipboard external doesn't work with clip.exe
    local CONFIG_BLOCK="
$MARKER
$VERSION_MARKER $CURRENT_VERSION
# Added by Groundskeeper installer - $(date +%Y-%m-%d)
# https://github.com/potato-hash/groundskeeper

# Terminal with true color support
set -g default-terminal \"tmux-256color\"
set -ag terminal-overrides \",xterm*:Tc:smcup@:rmcup@\"
set -ag terminal-overrides \",*256col*:Tc\"

# Performance
set -sg escape-time 0
set -g history-limit 50000

# Extended keys: forward Shift+Enter and other modified keys to apps (tmux 3.2+)
set -s extended-keys on
# Deliver them as ESC[13;2u (the kitty keyboard-protocol form Claude Code reads)
# rather than xterm modifyOtherKeys ESC[27;2;13~, which Claude Code ignores.
set -s extended-keys-format csi-u
set -as terminal-features 'tmux-256color:extkeys'

# Mouse support (scroll + drag-to-copy)
set -g mouse on

# Auto-enter copy-mode when scrolling up (critical for WSL compatibility)
# This handles: 1) apps with mouse support, 2) already in copy-mode, 3) normal pane
bind-key -n WheelUpPane if-shell -F -t = \"#{mouse_any_flag}\" \"send-keys -M\" \"if -Ft= '#{pane_in_mode}' 'send-keys -M' 'copy-mode -e'\"

# Scroll bindings in copy-mode (both vi and emacs modes)
bind-key -T copy-mode-vi WheelUpPane send-keys -X scroll-up
bind-key -T copy-mode-vi WheelDownPane send-keys -X scroll-down
bind-key -T copy-mode WheelUpPane send-keys -X scroll-up
bind-key -T copy-mode WheelDownPane send-keys -X scroll-down

# Clipboard integration (drag-to-copy)
bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel \"$CLIPBOARD_CMD\"
bind-key -T copy-mode MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel \"$CLIPBOARD_CMD\"
# End Groundskeeper configuration
"

    # Append to config file
    echo "$CONFIG_BLOCK" >> "$TMUX_CONF"

    echo -e "${GREEN}tmux configured successfully!${NC}"

    # kitty ignores xterm modifyOtherKeys (it only speaks its own CSI-u keyboard
    # protocol), so tmux can't negotiate Shift+Enter with it. kitty must emit the
    # sequence itself — we can't edit kitty.conf for the user, so just point it out.
    if [[ -n "$KITTY_WINDOW_ID" || "$TERM" == "xterm-kitty" || "$TERM_PROGRAM" == "kitty" ]]; then
        echo ""
        echo -e "${YELLOW}kitty detected:${NC} for Shift+Enter to insert a newline, add to ~/.config/kitty/kitty.conf:"
        echo "    map shift+enter send_text all \\x1b[13;2u"
        echo "  then reload kitty (Ctrl+Shift+F5). See troubleshooting.md."
    fi

    # Reload tmux config if tmux is running
    if tmux list-sessions &> /dev/null; then
        echo "Reloading tmux configuration..."
        tmux source-file "$TMUX_CONF" 2>/dev/null || true
        echo -e "${GREEN}tmux config reloaded${NC}"
    else
        echo "Run 'tmux source-file ~/.tmux.conf' to apply (or restart tmux)"
    fi
}

# Run tmux configuration (unless skipped)
if [[ "$SKIP_TMUX_CONFIG" != "true" ]]; then
    configure_tmux
else
    echo -e "${YELLOW}Skipping tmux configuration (--skip-tmux-config)${NC}"
fi

setup_command_display() {
    if [[ ":$PATH:" == *":$INSTALL_DIR:"* ]]; then
        echo "${BINARY_NAME} setup"
    else
        echo "${INSTALL_DIR}/${BINARY_NAME} setup"
    fi
}

find_bun() {
    if command -v bun >/dev/null 2>&1; then
        command -v bun
        return 0
    fi
    if [[ -n "${BUN_INSTALL:-}" && -x "$BUN_INSTALL/bin/bun" ]]; then
        echo "$BUN_INSTALL/bin/bun"
        return 0
    fi
    if [[ -x "$HOME/.bun/bin/bun" ]]; then
        echo "$HOME/.bun/bin/bun"
        return 0
    fi
    return 1
}

run_without_sensitive_env() {
    local env_args=()
    local name value upper
    while IFS='=' read -r name value; do
        [[ -n "$name" ]] || continue
        upper="$(printf '%s' "$name" | tr '[:lower:]' '[:upper:]')"
        case "$upper" in
            *API_KEY*|*TOKEN*|*SECRET*|*PASSWORD*|*PRIVATE_KEY*|*ACCESS_KEY*)
                env_args+=("-u" "$name")
                ;;
        esac
    done < <(env)
    env "${env_args[@]}" "$@"
}

find_cua_driver() {
    if command -v cua-driver >/dev/null 2>&1; then
        command -v cua-driver
        return 0
    fi
    if [[ -x "$INSTALL_DIR/cua-driver" ]]; then
        echo "$INSTALL_DIR/cua-driver"
        return 0
    fi
    if [[ -x "$HOME/.local/bin/cua-driver" ]]; then
        echo "$HOME/.local/bin/cua-driver"
        return 0
    fi
    return 1
}

install_cua_driver() {
    local cua_path
    if cua_path="$(find_cua_driver)"; then
        echo -e "${GREEN}Cua Driver already available at $cua_path${NC}"
        return 0
    fi

    if [[ "$IS_WSL" == "true" ]]; then
        echo -e "${YELLOW}Cua Driver on Windows/WSL is on the Groundskeeper roadmap.${NC}"
        echo "For now, install the Windows driver from PowerShell:"
        echo "  irm https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/scripts/install.ps1 | iex"
        return 1
    fi

    case "$OS" in
        darwin|linux) ;;
        *)
            echo -e "${YELLOW}Cua Driver install is supported here only on macOS/Linux.${NC}"
            return 1
            ;;
    esac

    echo "Installing Cua Driver for computer-use support..."
    if ! run_without_sensitive_env bash -o pipefail -c 'curl -fsSL https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/scripts/install.sh | bash -s -- --bin-dir "$1"' _ "$INSTALL_DIR"; then
        echo -e "${RED}Error: Cua Driver install failed.${NC}"
        echo "Install manually: /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/scripts/install.sh)\""
        return 1
    fi

    if cua_path="$(find_cua_driver)"; then
        echo -e "${GREEN}Cua Driver available at $cua_path${NC}"
        return 0
    fi

    echo -e "${RED}Error: Cua Driver installer completed but cua-driver is not discoverable.${NC}"
    echo "Add $INSTALL_DIR to PATH or rerun with --dir ~/.local/bin."
    return 1
}

if [[ "$INSTALL_CUA_DRIVER" == "true" ]]; then
    if ! install_cua_driver; then
        exit 1
    fi
fi

ensure_bun_for_first_run_setup() {
    local bun_path
    if bun_path="$(find_bun)"; then
        export PATH="$(dirname "$bun_path"):$PATH"
        return 0
    fi

    echo "Bun is required to build Espalier during first-run setup."
    echo "Installing Bun from https://bun.sh/install ..."
    if ! run_without_sensitive_env bash -o pipefail -c 'curl -fsSL https://bun.sh/install | bash'; then
        echo -e "${RED}Error: Bun install failed.${NC}"
        echo "Install Bun manually from https://bun.sh, then re-run: $(setup_command_display) --install-missing"
        return 1
    fi

    export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"
    export PATH="$BUN_INSTALL/bin:$PATH"
    if ! bun_path="$(find_bun)"; then
        echo -e "${RED}Error: Bun installer completed but bun is still not discoverable.${NC}"
        echo "Add $HOME/.bun/bin to PATH, then re-run: $(setup_command_display) --install-missing"
        return 1
    fi
    export PATH="$(dirname "$bun_path"):$PATH"
    echo -e "${GREEN}Bun available at $bun_path${NC}"
}

run_first_run_setup() {
    local installed_binary="${INSTALL_DIR}/${BINARY_NAME}"
    local setup_args=()
    if [[ -n "$SETUP_MODEL" ]]; then
        setup_args+=(--model "$SETUP_MODEL")
    fi
    if [[ -n "$SETUP_MEMORY_BACKEND" ]]; then
        setup_args+=(--memory-backend "$SETUP_MEMORY_BACKEND")
    fi
    if [[ -n "$SETUP_HINDSIGHT_URL" ]]; then
        setup_args+=(--hindsight-url "$SETUP_HINDSIGHT_URL")
    fi
    if [[ "$VERIFY_MODEL" == "true" ]]; then
        setup_args+=(--verify-model)
    fi
    if ! ensure_bun_for_first_run_setup; then
        return 1
    fi

    if [[ "$SKIP_OPTIONAL_DEPS" == "true" ]]; then
        setup_args+=(--non-interactive --install-missing)
        "$installed_binary" setup "${setup_args[@]}"
    elif has_prompt_tty; then
        "$installed_binary" setup "${setup_args[@]}" </dev/tty
    else
        setup_args+=(--non-interactive --install-missing)
        "$installed_binary" setup "${setup_args[@]}"
    fi
}

maybe_run_first_run_setup() {
    local setup_cmd
    setup_cmd="$(setup_command_display)"

    case "$SETUP_MODE" in
        skip)
            echo "First-run setup: skipped. Run later: ${setup_cmd}"
            return 0
            ;;
        run)
            ;;
        prompt)
            if ! has_prompt_tty; then
                echo "First-run setup: no interactive terminal. Run later: ${setup_cmd}"
                return 0
            fi
            echo -e "${BLUE}First-run setup wizard${NC}"
            echo "Configures OMP, Espalier Core, the worker model, and gk.db."
            echo ""
            REPLY=""
            prompt_read -p "Run first-run setup now? [Y/n] " -n 1 -r
            echo
            if [[ $REPLY =~ ^[Nn]$ ]]; then
                echo "First-run setup: skipped. Run later: ${setup_cmd}"
                return 0
            fi
            ;;
    esac

    echo ""
    echo -e "${BLUE}Starting first-run setup...${NC}"
    if ! run_first_run_setup; then
        echo -e "${YELLOW}First-run setup did not complete. Run again: ${setup_cmd}${NC}"
        if [[ "$SETUP_MODE" == "run" ]]; then
            exit 1
        fi
    fi
}

# Verify installation
if env GROUNDSKEEPER_SUPPRESS_TMUX_WARNING=1 AGENTDECK_SUPPRESS_TMUX_WARNING=1 "$INSTALL_DIR/$BINARY_NAME" version &> /dev/null; then
    INSTALLED_VERSION=$(env GROUNDSKEEPER_SUPPRESS_TMUX_WARNING=1 AGENTDECK_SUPPRESS_TMUX_WARNING=1 "$INSTALL_DIR/$BINARY_NAME" version 2>&1 || echo "unknown")
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║     Groundskeeper binary installed     ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "Version:  ${GREEN}${INSTALLED_VERSION}${NC}"
    echo -e "Binary:   ${GREEN}${INSTALL_DIR}/${BINARY_NAME}${NC}"
    echo -e "Platform: ${GREEN}${OS}/${ARCH}${NC}$([ "$IS_WSL" == "true" ] && echo -e " ${BLUE}(WSL)${NC}")"
    echo ""

    # Show dependency status
    echo "Dependencies:"
    if command -v tmux &> /dev/null; then
        echo -e "  ✓ tmux $(tmux -V 2>/dev/null | head -1)"
    else
        echo -e "  ${RED}✗ tmux (required - please install)${NC}"
    fi
    if command -v jq &> /dev/null; then
        echo -e "  ✓ jq $(jq --version 2>/dev/null)"
    else
        echo -e "  ${YELLOW}○ jq (optional - install for session forking)${NC}"
    fi
    if command -v git &> /dev/null; then
        echo -e "  ✓ git $(git --version 2>/dev/null | head -1)"
    else
        echo -e "  ${RED}✗ git (required for Espalier clone/worktrees)${NC}"
    fi
    if BUN_PATH="$(find_bun)"; then
        echo -e "  ✓ bun $("${BUN_PATH}" --version 2>/dev/null | head -1)"
    else
        echo -e "  ${YELLOW}○ bun (needed to build Espalier from source)${NC}"
    fi
    if command -v jj &> /dev/null; then
        echo -e "  ✓ jj $(jj --version 2>/dev/null | head -1)"
    else
        echo -e "  ${YELLOW}○ jj (needed for Espalier self-edit gates)${NC}"
    fi
    if command -v omp &> /dev/null; then
        echo -e "  ✓ omp found"
    else
        echo -e "  ${YELLOW}○ omp (installed by first-run setup if requested)${NC}"
    fi
    if CUA_PATH="$(find_cua_driver)"; then
        echo -e "  ✓ cua-driver $CUA_PATH"
    else
        echo -e "  ${YELLOW}○ cua-driver (optional computer-use driver; install with --install-cua-driver)${NC}"
    fi
    echo ""

    # Show tmux config status
    if [[ -f "$HOME/.tmux.conf" ]] && grep -q "# Groundskeeper configuration" "$HOME/.tmux.conf" 2>/dev/null; then
        echo -e "tmux config: ${GREEN}Configured for mouse scroll + clipboard${NC}"
    else
        echo -e "tmux config: ${YELLOW}Not configured (run installer again or see docs)${NC}"
    fi
    echo ""

    maybe_run_first_run_setup
    echo ""

    echo "Get started:"
    echo "  ${BINARY_NAME} setup        # First-run stack setup wizard"
    echo "  ${BINARY_NAME}              # Launch the TUI"
    echo "  ${BINARY_NAME} add .        # Add current directory as session"
    echo "  ${BINARY_NAME} --help       # Show help"
    # WSL-specific tips
    if [[ "$IS_WSL" == "true" ]]; then
        echo ""
        echo -e "${BLUE}WSL Tips:${NC}"
        echo "  • Clipboard works with Windows (via clip.exe)"
        echo "  • Run in Windows Terminal for best experience"
        echo "  • Mouse scrolling works out of the box"
        echo ""
        # Check WSL version for socket pooling info
        if grep -qi "microsoft-standard" /proc/version 2>/dev/null; then
            echo -e "  ${GREEN}•${NC} WSL2 detected: MCP socket pooling supported"
        else
            echo -e "  ${YELLOW}•${NC} WSL1 detected: MCP socket pooling disabled"
            echo "    MCPs work fine in stdio mode (just uses more memory)"
            echo "    Upgrade to WSL2 for socket pooling: wsl --set-version <distro> 2"
        fi
    fi
else
    echo -e "${RED}Warning: Installation completed but verification failed${NC}"
    echo "The binary was installed but may not work correctly."
    echo ""
    echo "Troubleshooting:"
    echo "  1. Check if ${INSTALL_DIR} is in your PATH"
    echo "  2. Try: ${INSTALL_DIR}/${BINARY_NAME} version"
    echo "  3. If using zsh: source ~/.zshrc"
    echo "  4. If using bash: source ~/.bashrc"
fi

} # end main

# Run the installer unless the script was sourced purely to load its functions
# for testing (see internal/releasetests/issue1206_install_checksum_test.go).
# Unset in normal `curl ... | bash` use, so the installer runs as before.
if [[ -z "${AGENT_DECK_INSTALL_SH_SOURCE_ONLY:-}" ]]; then
    main "$@"
fi
