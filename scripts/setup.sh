#!/usr/bin/env bash
set -euo pipefail

# ─── IaC Studio Setup ───
# Interactive setup that checks dependencies, installs what's missing,
# configures the AI model based on your system, and gets you running.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/your-org/iac-studio/main/scripts/setup.sh | bash
#   or:  ./scripts/setup.sh

# ─── Colors & Helpers ───
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

info()    { echo -e "${BLUE}  ℹ${NC}  $1"; }
ok()      { echo -e "${GREEN}  ✓${NC}  $1"; }
warn()    { echo -e "${YELLOW}  ⚠${NC}  $1"; }
err()     { echo -e "${RED}  ✗${NC}  $1"; }
step()    { echo -e "\n${PURPLE}${BOLD}  $1${NC}"; }
prompt()  { echo -en "${CYAN}  ?${NC}  $1 "; }

# ─── Detect Platform ───
detect_platform() {
    OS="$(uname -s 2>/dev/null || echo "Unknown")"
    ARCH="$(uname -m 2>/dev/null || echo "Unknown")"

    case "$OS" in
        Linux)   PLATFORM="linux" ;;
        Darwin)  PLATFORM="macos" ;;
        MINGW*|MSYS*|CYGWIN*) PLATFORM="windows" ;;
        *)       PLATFORM="unknown" ;;
    esac

    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)             ARCH="unknown" ;;
    esac
}

# ─── Detect System Resources ───
detect_resources() {
    # RAM in GB
    case "$PLATFORM" in
        macos)
            RAM_BYTES=$(sysctl -n hw.memsize 2>/dev/null || echo "0")
            RAM_GB=$((RAM_BYTES / 1073741824))
            CPU_CORES=$(sysctl -n hw.ncpu 2>/dev/null || echo "1")
            ;;
        linux)
            RAM_KB=$(grep MemTotal /proc/meminfo 2>/dev/null | awk '{print $2}' || echo "0")
            RAM_GB=$((RAM_KB / 1048576))
            CPU_CORES=$(nproc 2>/dev/null || grep -c processor /proc/cpuinfo 2>/dev/null || echo "1")
            ;;
        windows)
            # Git Bash / MSYS
            RAM_GB=$(wmic computersystem get totalphysicalmemory 2>/dev/null | grep -o '[0-9]*' | head -1 | awk '{print int($1/1073741824)}' || echo "8")
            CPU_CORES=$(echo "$NUMBER_OF_PROCESSORS" 2>/dev/null || echo "4")
            ;;
        *)
            RAM_GB=8
            CPU_CORES=4
            ;;
    esac

    # Detect GPU (for Ollama acceleration)
    HAS_GPU=false
    if command -v nvidia-smi &>/dev/null; then
        HAS_GPU=true
        GPU_INFO=$(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null | head -1 || echo "NVIDIA GPU")
    elif [ "$PLATFORM" = "macos" ] && [ "$ARCH" = "arm64" ]; then
        HAS_GPU=true
        GPU_INFO="Apple Silicon (unified memory)"
    fi
}

# ─── Select Best Model ───
select_model() {
    if [ "$RAM_GB" -ge 32 ]; then
        RECOMMENDED_MODEL="gemma4"
        MODEL_SIZE="9.6 GB"
        MODEL_DESC="Gemma 4 26B MoE — best quality, excellent for IaC generation"
    elif [ "$RAM_GB" -ge 16 ]; then
        RECOMMENDED_MODEL="gemma4"
        MODEL_SIZE="9.6 GB"
        MODEL_DESC="Gemma 4 26B MoE — great quality, fits in 16GB+ RAM"
    elif [ "$RAM_GB" -ge 8 ]; then
        RECOMMENDED_MODEL="qwen2.5-coder:7b"
        MODEL_SIZE="4.7 GB"
        MODEL_DESC="Qwen 2.5 Coder 7B — optimized for code, fits in 8GB RAM"
    else
        RECOMMENDED_MODEL="gemma4:e2b"
        MODEL_SIZE="~2 GB"
        MODEL_DESC="Gemma 4 E2B — lightweight, runs on low-memory systems"
    fi
}

# ─── Check / Install Dependencies ───
check_command() {
    if command -v "$1" &>/dev/null; then
        local version
        version=$($2 2>&1 | head -1 || echo "installed")
        ok "$1 found — ${DIM}${version}${NC}"
        return 0
    else
        return 1
    fi
}

install_go() {
    case "$PLATFORM" in
        macos)
            if command -v brew &>/dev/null; then
                info "Installing Go via Homebrew..."
                brew install go
            else
                err "Install Go from https://go.dev/dl/"
                return 1
            fi
            ;;
        linux)
            info "Installing Go..."
            local GO_VERSION="1.22.5"
            curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tar.gz
            sudo rm -rf /usr/local/go
            sudo tar -C /usr/local -xzf /tmp/go.tar.gz
            rm /tmp/go.tar.gz
            export PATH=$PATH:/usr/local/go/bin
            ok "Go ${GO_VERSION} installed to /usr/local/go"
            ;;
        *)
            err "Install Go from https://go.dev/dl/"
            return 1
            ;;
    esac
}

install_node() {
    case "$PLATFORM" in
        macos)
            if command -v brew &>/dev/null; then
                info "Installing Node.js via Homebrew..."
                brew install node
            else
                err "Install Node.js from https://nodejs.org/"
                return 1
            fi
            ;;
        linux)
            info "Installing Node.js via NodeSource..."
            curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
            sudo apt-get install -y nodejs 2>/dev/null || sudo yum install -y nodejs 2>/dev/null
            ;;
        *)
            err "Install Node.js from https://nodejs.org/"
            return 1
            ;;
    esac
}

install_ollama() {
    case "$PLATFORM" in
        macos|linux)
            info "Installing Ollama..."
            curl -fsSL https://ollama.com/install.sh | sh
            ok "Ollama installed"
            ;;
        *)
            err "Download Ollama from https://ollama.com/download"
            return 1
            ;;
    esac
}

# ─── Banner ───
show_banner() {
    echo ""
    echo -e "${PURPLE}${BOLD}"
    echo "  ◆ IaC Studio Setup"
    echo "  ────────────────────"
    echo -e "${NC}${DIM}  Local-first visual IaC builder${NC}"
    echo ""
}

# ─── Main ───
main() {
    show_banner
    detect_platform
    detect_resources

    # ── System Info ──
    step "System Information"
    info "Platform: ${BOLD}${PLATFORM}${NC} (${ARCH})"
    info "RAM: ${BOLD}${RAM_GB} GB${NC}"
    info "CPU cores: ${BOLD}${CPU_CORES}${NC}"
    if [ "$HAS_GPU" = true ]; then
        ok "GPU: ${GPU_INFO}"
    else
        info "GPU: none detected (CPU inference will be used)"
    fi

    # ── Dependencies ──
    step "Checking Dependencies"

    MISSING_DEPS=()

    if check_command "go" "go version"; then true
    else
        warn "Go not found"
        MISSING_DEPS+=("go")
    fi

    if check_command "node" "node --version"; then true
    else
        warn "Node.js not found"
        MISSING_DEPS+=("node")
    fi

    if check_command "npm" "npm --version"; then true
    else
        warn "npm not found"
        MISSING_DEPS+=("npm")
    fi

    # Optional tools
    echo ""
    info "${DIM}Optional IaC tools:${NC}"
    check_command "terraform" "terraform version" || info "  terraform — not installed (optional)"
    check_command "tofu" "tofu version" || info "  tofu — not installed (optional)"
    check_command "ansible" "ansible --version" || info "  ansible — not installed (optional)"

    # Install missing deps
    if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
        echo ""
        prompt "Install missing dependencies (${MISSING_DEPS[*]})? [Y/n]"
        read -r INSTALL_DEPS
        INSTALL_DEPS=${INSTALL_DEPS:-Y}

        if [[ "$INSTALL_DEPS" =~ ^[Yy] ]]; then
            for dep in "${MISSING_DEPS[@]}"; do
                case "$dep" in
                    go)   install_go ;;
                    node|npm) install_node ;;
                esac
            done
        else
            err "Required dependencies missing. Install them and re-run setup."
            exit 1
        fi
    fi

    # ── AI Configuration ──
    step "AI Model Configuration"
    echo ""
    echo -e "  ${BOLD}How do you want to run AI features?${NC}"
    echo ""
    echo -e "  ${GREEN}1)${NC} Local model (Ollama) — ${DIM}free, private, runs on your machine${NC}"
    echo -e "  ${GREEN}2)${NC} External API — ${DIM}OpenAI, Anthropic, Groq, or any OpenAI-compatible API${NC}"
    echo -e "  ${GREEN}3)${NC} Skip AI setup — ${DIM}configure later in the app settings${NC}"
    echo ""
    prompt "Choose [1/2/3]:"
    read -r AI_CHOICE
    AI_CHOICE=${AI_CHOICE:-1}

    AI_MODEL=""
    AI_ENDPOINT=""
    AI_FLAGS=""

    case "$AI_CHOICE" in
        1)
            # Local model with Ollama
            if ! check_command "ollama" "ollama --version"; then
                prompt "Install Ollama? [Y/n]"
                read -r INSTALL_OLLAMA
                INSTALL_OLLAMA=${INSTALL_OLLAMA:-Y}
                if [[ "$INSTALL_OLLAMA" =~ ^[Yy] ]]; then
                    install_ollama
                else
                    warn "Skipping Ollama — AI features will use pattern matching fallback"
                    AI_MODEL="none"
                fi
            fi

            if [ "$AI_MODEL" != "none" ]; then
                select_model
                echo ""
                echo -e "  ${BOLD}Recommended model for your system (${RAM_GB}GB RAM):${NC}"
                echo -e "  ${CYAN}${RECOMMENDED_MODEL}${NC} (${MODEL_SIZE}) — ${MODEL_DESC}"
                echo ""

                # Show alternatives
                echo -e "  ${DIM}Other options:${NC}"
                echo -e "  ${DIM}  gemma4        — 9.6 GB, best quality (needs 16GB+ RAM)${NC}"
                echo -e "  ${DIM}  qwen2.5-coder:7b — 4.7 GB, code-optimized (needs 8GB+ RAM)${NC}"
                echo -e "  ${DIM}  gemma4:e4b    — 3 GB, fast and capable (needs 6GB+ RAM)${NC}"
                echo -e "  ${DIM}  gemma4:e2b    — 2 GB, lightweight (any system)${NC}"
                echo ""
                prompt "Model to install [${RECOMMENDED_MODEL}]:"
                read -r CHOSEN_MODEL
                AI_MODEL=${CHOSEN_MODEL:-$RECOMMENDED_MODEL}

                # Check if model is already pulled
                if ollama list 2>/dev/null | grep -q "^${AI_MODEL}"; then
                    ok "${AI_MODEL} already downloaded"
                else
                    info "Downloading ${AI_MODEL}... (this may take several minutes)"
                    echo ""
                    ollama pull "$AI_MODEL"
                    ok "${AI_MODEL} downloaded successfully"
                fi

                AI_ENDPOINT="http://localhost:11434"
                AI_FLAGS="--ai-model ${AI_MODEL} --ai-endpoint ${AI_ENDPOINT}"
            fi
            ;;
        2)
            # External API
            echo ""
            echo -e "  ${BOLD}Supported providers:${NC}"
            echo -e "  • OpenAI — ${DIM}https://api.openai.com/v1${NC}"
            echo -e "  • Anthropic (via proxy) — ${DIM}https://api.anthropic.com/v1${NC}"
            echo -e "  • Groq — ${DIM}https://api.groq.com/openai/v1${NC}"
            echo -e "  • Together — ${DIM}https://api.together.xyz/v1${NC}"
            echo -e "  • Any OpenAI-compatible endpoint"
            echo ""
            info "You can configure the API key in the app (⚙ button in header)."
            info "The key is stored in memory only — never written to disk."
            AI_MODEL="gpt-4o"
            AI_ENDPOINT="https://api.openai.com/v1"
            AI_FLAGS="--ai-model ${AI_MODEL} --ai-endpoint ${AI_ENDPOINT}"
            ;;
        3)
            info "Skipping AI setup — you can configure it later in the app."
            AI_FLAGS=""
            ;;
    esac

    # ── Build Project ──
    step "Building IaC Studio"

    info "Installing Go dependencies..."
    go mod tidy

    info "Installing frontend dependencies..."
    cd web && npm install --silent && cd ..

    info "Building frontend..."
    cd web && npm run build --silent && cd ..

    info "Copying frontend to embed path..."
    rm -rf cmd/server/frontend/dist
    cp -r web/dist cmd/server/frontend/dist

    info "Building Go binary..."
    CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/iac-studio ./cmd/server

    ok "Build complete — ${BOLD}bin/iac-studio${NC}"

    # ── Done ──
    step "Setup Complete!"
    echo ""
    echo -e "  ${GREEN}${BOLD}Start IaC Studio:${NC}"
    echo ""
    if [ -n "$AI_FLAGS" ]; then
        echo -e "    ${CYAN}./bin/iac-studio ${AI_FLAGS}${NC}"
    else
        echo -e "    ${CYAN}./bin/iac-studio${NC}"
    fi
    echo ""
    echo -e "  Then open ${BOLD}http://localhost:3000${NC} in your browser."
    echo ""
    echo -e "  ${DIM}────────────────────────────────────────${NC}"
    echo -e "  ${DIM}Development mode (hot reload):${NC}"
    echo -e "    ${CYAN}make dev${NC}"
    echo ""
    echo -e "  ${DIM}Run tests:${NC}"
    echo -e "    ${CYAN}make test${NC}"
    echo ""
    echo -e "  ${DIM}AI settings:${NC}"
    echo -e "    Click ${BOLD}⚙${NC}${DIM} in the app header to change model/provider${NC}"
    echo ""

    # Offer to start now
    prompt "Start IaC Studio now? [Y/n]"
    read -r START_NOW
    START_NOW=${START_NOW:-Y}

    if [[ "$START_NOW" =~ ^[Yy] ]]; then
        echo ""
        if [ -n "$AI_FLAGS" ]; then
            exec ./bin/iac-studio $AI_FLAGS
        else
            exec ./bin/iac-studio
        fi
    fi
}

main "$@"
