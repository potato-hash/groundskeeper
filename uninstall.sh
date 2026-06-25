#!/bin/bash
#
# Groundskeeper Uninstaller
# https://github.com/potato-hash/groundskeeper
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/potato-hash/groundskeeper/main/uninstall.sh | bash
#
# Options:
#   --keep-data         Keep XDG config/data/cache and legacy ~/.agent-deck/
#   --keep-tmux-config  Keep tmux configuration
#   --non-interactive   Skip all prompts (removes everything)
#   --dry-run           Show what would be removed without removing
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
DIM='\033[2m'
NC='\033[0m' # No Color

# Defaults
KEEP_DATA=false
KEEP_TMUX_CONFIG=false
NON_INTERACTIVE=false
DRY_RUN=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --keep-data)
            KEEP_DATA=true
            shift
            ;;
        --keep-tmux-config)
            KEEP_TMUX_CONFIG=true
            shift
            ;;
        --non-interactive)
            NON_INTERACTIVE=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            echo "Groundskeeper Uninstaller"
            echo ""
            echo "Usage: uninstall.sh [options]"
            echo ""
            echo "Options:"
            echo "  --keep-data         Keep XDG config/data/cache and legacy ~/.agent-deck/"
            echo "  --keep-tmux-config  Keep tmux configuration in ~/.tmux.conf"
            echo "  --non-interactive   Skip all prompts (removes everything)"
            echo "  --dry-run           Show what would be removed without removing"
            echo "  -h, --help          Show this help message"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}╔════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║       Groundskeeper Uninstaller        ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════╝${NC}"
echo ""

if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}DRY RUN MODE - Nothing will be removed${NC}"
    echo ""
fi

xdg_path() {
    local env_name="$1"
    local fallback="$2"
    local base="${!env_name:-}"
    if [[ -z "$base" ]]; then
        base="$HOME/$fallback"
    fi
    echo "$base/groundskeeper"
}

# Track what we find
FOUND_ITEMS=()
HOMEBREW_INSTALLED=false

# Check for Homebrew installation
if command -v brew &> /dev/null && brew list groundskeeper &> /dev/null 2>&1; then
    HOMEBREW_INSTALLED=true
    FOUND_ITEMS+=("homebrew")
    echo -e "Found: ${GREEN}Homebrew installation${NC}"
fi

# Check common binary locations
BINARY_LOCATIONS=(
    "$HOME/.local/bin/groundskeeper"
    "/usr/local/bin/groundskeeper"
    "$HOME/bin/groundskeeper"
)

for loc in "${BINARY_LOCATIONS[@]}"; do
    if [[ -f "$loc" ]] || [[ -L "$loc" ]]; then
        FOUND_ITEMS+=("binary:$loc")
        if [[ -L "$loc" ]]; then
            TARGET=$(readlink "$loc" 2>/dev/null || echo "unknown")
            echo -e "Found: ${GREEN}Binary (symlink)${NC} at $loc"
            echo -e "       ${DIM}-> $TARGET${NC}"
        else
            echo -e "Found: ${GREEN}Binary${NC} at $loc"
        fi
    fi
done

DATA_LOCATIONS=(
    "config:Config directory:$(xdg_path XDG_CONFIG_HOME .config)"
    "data:Data directory:$(xdg_path XDG_DATA_HOME .local/share)"
    "cache:Cache directory:$(xdg_path XDG_CACHE_HOME .cache)"
    "legacy:Legacy directory:$HOME/.agent-deck"
)

for data_item in "${DATA_LOCATIONS[@]}"; do
    kind="${data_item%%:*}"
    rest="${data_item#*:}"
    label="${rest%%:*}"
    loc="${rest#*:}"
    if [[ -e "$loc" ]] || [[ -L "$loc" ]]; then
        FOUND_ITEMS+=("data:$kind:$loc")
        DATA_SIZE=$(du -sh "$loc" 2>/dev/null | cut -f1 || true)
        echo -e "Found: ${GREEN}${label}${NC} at $loc"
        [[ -n "$DATA_SIZE" ]] && echo -e "       ${DIM}${DATA_SIZE} total${NC}"
    fi
done

# Check for tmux config
TMUX_CONF="$HOME/.tmux.conf"
if [[ -f "$TMUX_CONF" ]] && grep -q "# Groundskeeper configuration" "$TMUX_CONF" 2>/dev/null; then
    FOUND_ITEMS+=("tmux")
    echo -e "Found: ${GREEN}tmux configuration${NC} in $TMUX_CONF"
fi

echo ""

# Nothing found?
if [[ ${#FOUND_ITEMS[@]} -eq 0 ]]; then
    echo -e "${YELLOW}Groundskeeper does not appear to be installed.${NC}"
    echo ""
    echo "Checked locations:"
    for loc in "${BINARY_LOCATIONS[@]}"; do
        echo "  - $loc"
    done
    for data_item in "${DATA_LOCATIONS[@]}"; do
        echo "  - ${data_item##*:}"
    done
    echo "  - $TMUX_CONF (for Groundskeeper config)"
    exit 0
fi

# Summary of what will be removed
echo -e "${BLUE}The following will be removed:${NC}"
echo ""

for item in "${FOUND_ITEMS[@]}"; do
    case "$item" in
        homebrew)
            echo -e "  ${RED}•${NC} Homebrew package: groundskeeper"
            ;;
        binary:*)
            loc="${item#binary:}"
            echo -e "  ${RED}•${NC} Binary: $loc"
            ;;
        data:*)
            kind="${item#data:}"
            kind="${kind%%:*}"
            loc="${item#data:$kind:}"
            if [[ "$KEEP_DATA" == "true" ]]; then
                echo -e "  ${GREEN}•${NC} $kind directory: $loc ${YELLOW}(keeping)${NC}"
            else
                echo -e "  ${RED}•${NC} $kind directory: $loc"
            fi
            ;;
        tmux)
            if [[ "$KEEP_TMUX_CONFIG" == "true" ]]; then
                echo -e "  ${GREEN}•${NC} tmux config: ~/.tmux.conf ${YELLOW}(keeping)${NC}"
            else
                echo -e "  ${RED}•${NC} tmux config block in ~/.tmux.conf"
            fi
            ;;
    esac
done

echo ""

# Confirm unless non-interactive
if [[ "$NON_INTERACTIVE" != "true" && "$DRY_RUN" != "true" ]]; then
    read -p "Proceed with uninstall? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Uninstall cancelled."
        exit 0
    fi
    echo ""
fi

# Dry run stops here
if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}Dry run complete. No changes made.${NC}"
    exit 0
fi

# Perform uninstallation
echo -e "${BLUE}Uninstalling...${NC}"
echo ""

# 1. Homebrew
if [[ "$HOMEBREW_INSTALLED" == "true" ]]; then
    echo -e "Removing Homebrew package..."
    brew uninstall groundskeeper
    echo -e "${GREEN}✓${NC} Homebrew package removed"
fi

# 2. Binary files
for item in "${FOUND_ITEMS[@]}"; do
    if [[ "$item" == binary:* ]]; then
        loc="${item#binary:}"
        echo -e "Removing binary at $loc..."

        # Check if we need sudo
        if [[ ! -w "$(dirname "$loc")" ]]; then
            echo -e "${YELLOW}Requires sudo to remove $loc${NC}"
            sudo rm -f "$loc"
        else
            rm -f "$loc"
        fi
        echo -e "${GREEN}✓${NC} Binary removed: $loc"
    fi
done

# 3. tmux config
if [[ " ${FOUND_ITEMS[*]} " =~ " tmux " ]] && [[ "$KEEP_TMUX_CONFIG" != "true" ]]; then
    echo -e "Removing tmux configuration..."

    # Create backup
    cp "$TMUX_CONF" "$TMUX_CONF.bak.groundskeeper-uninstall"

    # Remove the groundskeeper config block (between markers)
    # Using sed to delete from start marker to end marker
    if [[ "$(uname)" == "Darwin" ]]; then
        # macOS sed requires different syntax
        sed -i '' '/# Groundskeeper configuration/,/# End Groundskeeper configuration/d' "$TMUX_CONF"
    else
        sed -i '/# Groundskeeper configuration/,/# End Groundskeeper configuration/d' "$TMUX_CONF"
    fi

    # Remove any trailing empty lines at end of file
    if [[ "$(uname)" == "Darwin" ]]; then
        sed -i '' -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$TMUX_CONF" 2>/dev/null || true
    else
        sed -i -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$TMUX_CONF" 2>/dev/null || true
    fi

    echo -e "${GREEN}✓${NC} tmux configuration removed (backup: ~/.tmux.conf.bak.groundskeeper-uninstall)"
fi

# 4. XDG and legacy data directories
if [[ "$KEEP_DATA" != "true" ]]; then
    DATA_ITEMS=()
    for item in "${FOUND_ITEMS[@]}"; do
        [[ "$item" == data:* ]] && DATA_ITEMS+=("$item")
    done

    if [[ ${#DATA_ITEMS[@]} -gt 0 ]]; then
        echo -e "Removing data directories..."

        # Offer backup for interactive runs.
        if [[ "$NON_INTERACTIVE" != "true" ]]; then
            read -p "Create backup of data before removing? [Y/n] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                BACKUP_FILE="$HOME/groundskeeper-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
                TAR_ARGS=(-czf "$BACKUP_FILE" -C /)
                for item in "${DATA_ITEMS[@]}"; do
                    loc="${item#data:}"
                    loc="${loc#*:}"
                    [[ -L "$loc" ]] && continue
                    TAR_ARGS+=("${loc#/}")
                done
                if [[ ${#TAR_ARGS[@]} -gt 4 ]]; then
                    echo -e "Creating backup at $BACKUP_FILE..."
                    tar "${TAR_ARGS[@]}"
                    echo -e "${GREEN}✓${NC} Backup created: $BACKUP_FILE"
                fi
            fi
        fi

        for item in "${DATA_ITEMS[@]}"; do
            loc="${item#data:}"
            loc="${loc#*:}"
            rm -rf "$loc"
            echo -e "${GREEN}✓${NC} Removed: $loc"
        done
    fi
fi

echo ""
echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║     Uninstall complete!                ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
echo ""

if [[ "$KEEP_DATA" == "true" ]]; then
    echo -e "${YELLOW}Note:${NC} XDG config/data/cache and legacy ~/.agent-deck/ were preserved."
    echo "      Remove manually after reviewing their contents."
fi

if [[ "$KEEP_TMUX_CONFIG" == "true" ]]; then
    echo -e "${YELLOW}Note:${NC} tmux config preserved in ~/.tmux.conf"
    echo "      Remove the '# Groundskeeper configuration' block manually if desired"
fi

echo ""
echo "Thank you for using Groundskeeper!"
echo "Feedback: https://github.com/potato-hash/groundskeeper/issues"
