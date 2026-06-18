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

The setup script builds `bin/iac-studio` from source. It does not download the
prebuilt release binaries. To download the latest release binary from a script,
use `scripts/install.sh` instead.

## Manual Setup

### Prerequisites

- **Go 1.25+** — [go.dev/dl](https://go.dev/dl/)
- **Node.js 20+** — [nodejs.org](https://nodejs.org/)

### Install & Run

```bash
# Clone the repo
git clone https://github.com/olandodeflexy/IaCStudio.git
cd IaCStudio

# Install dependencies
make deps

# Build
make build

# Run
./bin/iac-studio
```

Open **http://localhost:3000** in your browser.

## Release Binaries

Release binaries are the local server that serves the web UI and API. Download
the asset for your machine, make it executable on macOS or Linux, run it, then
open **http://localhost:3000**.

| Machine | Asset |
|---------|-------|
| Apple Silicon Mac | `iac-studio-darwin-arm64` |
| Intel Mac | `iac-studio-darwin-amd64` |
| Linux x86_64 | `iac-studio-linux-amd64` |
| Linux ARM64 | `iac-studio-linux-arm64` |
| Windows x64 | `iac-studio-windows-amd64.exe` |
| Windows ARM64 | `iac-studio-windows-arm64.exe` |

macOS or Linux:

```bash
chmod +x ./iac-studio-darwin-arm64
./iac-studio-darwin-arm64
```

Optional rename:

```bash
mv iac-studio-darwin-arm64 iac-studio
chmod +x ./iac-studio
./iac-studio
```

If macOS blocks an unsigned binary:

```bash
xattr -d com.apple.quarantine ./iac-studio
./iac-studio
```

`checksums.txt` verifies download integrity. It is not executable.

Scripted release install:

```bash
curl -fsSL https://raw.githubusercontent.com/olandodeflexy/IaCStudio/main/scripts/install.sh | bash
iac-studio
```

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
| **Connect cloud target** | Open Cloud tab → save/test a named AWS, Azure, or GCP connection → click "Use for runs" |
| **Run terraform** | Click Init → Plan → Apply in the header |
| **Review plan risk** | After Plan, read the semantic summary. Risky, destructive, or unknown Terraform/OpenTofu changes require acknowledgement before Apply |
| **Undo/Redo** | Ctrl+Z / Ctrl+Shift+Z or the ↩↪ buttons |
| **Delete** | Select a node or connection, press Delete |
| **Fix errors** | When Plan fails, click "Fix with AI" in the terminal |
| **Open in Finder** | Click 📂 next to the project name |
| **Resize panels** | Drag the borders between sidebar, canvas, and bottom panel |
| **AI settings** | Click ⚙ in the header |

## Cloud Connections

Cloud Connections are named deployment targets for real infrastructure runs.
They let you choose the account, subscription, project, and region before
running `init`, `plan`, or `apply`.

Recommended auth paths:

| Provider | Prefer | Fallback |
|----------|--------|----------|
| AWS | Local AWS profile or AWS SSO profile | Static access key |
| Azure | Azure CLI login | Service principal |
| GCP | gcloud auth | Service account JSON |

Use them like this:

1. Open a project.
2. Open the **Cloud** tab in the inspector.
3. Save a named connection, for example `platform-dev`.
4. Click **Test** and fix any missing fields.
5. Click **Use for runs**.
6. Confirm the selected target appears in the header before running `Plan` or `Apply`.

Secret fields are encrypted locally before they are written to
`.iac-studio-connections.json`. When a saved connection contains secret fields
and `IAC_STUDIO_CONNECTIONS_KEY` is not set, IaC Studio creates a
`.iac-studio-connections.key` file next to the connections file with `0600`
permissions. Set `IAC_STUDIO_CONNECTIONS_KEY` if you need a stable key for
container redeploys or backups. API responses, WebSocket terminal messages, and
generated IaC files do not echo secret values.

For local auth flows, make sure the provider CLI is already logged in:

```bash
aws sso login --profile platform-dev
az login
gcloud auth application-default login
```

## Semantic Plan Gate

Terraform and OpenTofu runs save the exact plan artifact as `tfplan`, export it
to `tfplan.json`, then classify each planned change before apply.

The summary groups changes into:

| Risk | Meaning |
|------|---------|
| `safe` | Metadata-only or no-op changes, such as tag refreshes |
| `risky` | IAM, network exposure, or stateful resource changes that need review |
| `destructive` | Deletes or replacements that can remove existing infrastructure |
| `unknown` | Changes the classifier cannot safely explain |

`Apply` uses the saved `tfplan` file. If the classifier finds risky,
destructive, or unknown changes, IaC Studio shows the reviewer summary and
requires explicit acknowledgement before continuing.

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
