# Quick Start

Get IaC Studio running in under 5 minutes.

## One-command Setup

```bash
./scripts/setup.sh
```

This interactive script will:
1. Detect your OS, RAM, CPU, and GPU
2. Check for Go and Node.js — offer to install if missing
3. Ask how you want to run AI (local Ollama or external API)
4. Recommend the best AI model for your hardware
5. Download the model and build the app
6. Start IaC Studio

## Manual Setup

### Prerequisites

- **Go 1.22+** — [go.dev/dl](https://go.dev/dl/)
- **Node.js 20+** — [nodejs.org](https://nodejs.org/)

### Install & Run

```bash
# Clone the repo
git clone https://github.com/your-org/iac-studio.git
cd iac-studio

# Install dependencies
make deps

# Build
make build

# Run
./bin/iac-studio
```

Open **http://localhost:3000** in your browser.

### AI Setup (Optional)

**Option A: Local model (free, private)**

```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Pull a model (choose based on your RAM)
ollama pull gemma4           # 9.6 GB — best quality (16GB+ RAM)
ollama pull qwen2.5-coder:7b # 4.7 GB — code-optimized (8GB+ RAM)
ollama pull gemma4:e4b       # 3 GB   — fast and capable (6GB+ RAM)

# Run with your model
./bin/iac-studio --ai-model gemma4
```

**Option B: External API (OpenAI, Groq, etc.)**

```bash
./bin/iac-studio
```

Then click **⚙** in the header → select your provider → enter your API key.

Supported: OpenAI, Anthropic, Groq, Together, Azure OpenAI, or any OpenAI-compatible API.

## What You Can Do

| Action | How |
|--------|-----|
| **Add resources** | Click + in the resource palette (left sidebar) |
| **Search resources** | Type in the search box — filters across 250+ resources |
| **Connect resources** | Drag from the circle port on a node to another node |
| **AI generate** | Type in the chat: "Add a VPC with 3 subnets" |
| **Build from description** | Click "Build from Description" on the start screen |
| **Import existing project** | Click "Import Existing Project" → browse to your .tf files |
| **Run terraform** | Click Init → Plan → Apply in the header |
| **Undo/Redo** | Ctrl+Z / Ctrl+Shift+Z or the ↩↪ buttons |
| **Delete** | Select a node or connection, press Delete |
| **Fix errors** | When Plan fails, click "Fix with AI" in the terminal |
| **Open in Finder** | Click 📂 next to the project name |
| **Resize panels** | Drag the borders between sidebar, canvas, and bottom panel |
| **AI settings** | Click ⚙ in the header |

## Development Mode

```bash
make dev
```

Runs Go backend on `:3001` and Vite dev server on `:5173` with hot reload.

## CLI Flags

```
./bin/iac-studio [flags]

  --host          Bind address (default: 127.0.0.1)
  --port          Port number (default: 3000)
  --projects-dir  Project directory (default: ~/iac-projects)
  --ai-endpoint   Ollama/API endpoint (default: http://localhost:11434)
  --ai-model      AI model name (default: deepseek-coder:6.7b)
  --version       Print version and exit
```
