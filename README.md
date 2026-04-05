# IaC Studio

**A local-first, open source visual builder for Infrastructure as Code.**

Build Terraform, OpenTofu, and Ansible projects through a drag-and-drop UI with an AI assistant — while keeping full control of your files on disk.

![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)
![React](https://img.shields.io/badge/React-18+-61DAFB?logo=react)

---

## Why IaC Studio?

Most IaC tools are CLI-only. Visual tools are either cloud-hosted SaaS or locked to one provider. IaC Studio is different:

- **Runs locally** — your infrastructure code never leaves your machine
- **Multi-cloud** — AWS, GCP, and Azure with 250+ resources across all three
- **Multi-tool** — Terraform, OpenTofu, and Ansible in one interface
- **Topology-aware** — visual connection lines that generate real Terraform references
- **AI-powered** — local LLM or external API converts natural language to infrastructure
- **Security scanning** — graph-based checks with CIS, SOC2, HIPAA compliance mapping
- **Open source** — Apache 2.0, no telemetry, no accounts, no cloud dependency
- **Single binary** — download one file, run it, done

## Quick Start

```bash
# Interactive setup — checks deps, installs AI model, builds, and starts
./scripts/setup.sh
```

Or manually:

```bash
make deps && make build && ./bin/iac-studio --ai-model gemma4
```

Then open **http://localhost:3000**.

See [QUICKSTART.md](QUICKSTART.md) for detailed instructions.

## Features

| Feature | Description |
|---------|-------------|
| **Visual Canvas** | Drag-and-drop resources with connection lines and topology |
| **250+ Resources** | AWS, GCP, Azure for Terraform/OpenTofu + 80 Ansible modules |
| **Topology Connections** | Auto-connect resources; generates real Terraform references |
| **AI Chat** | "Add a VPC with 3 subnets" — works with Ollama or OpenAI API |
| **AI Topology Builder** | Describe an architecture in plain text, AI generates everything |
| **AI Plan Fix** | When terraform plan fails, AI diagnoses and auto-fixes the issue |
| **Security Scanner** | Graph-based checks: CIS, SOC2, HIPAA, OWASP compliance |
| **Drift Detection** | Compare terraform.tfstate against code, find what changed |
| **Multi-Format Export** | Export to Pulumi TypeScript, CDK Python, CloudFormation |
| **Smart Suggestions** | AI predicts your next resource based on IaC best practices |
| **Import Projects** | Browse filesystem, scan existing .tf/.yml files, auto-detect topology |
| **Live Code Preview** | Every canvas change updates the code in real time |
| **Project Persistence** | Auto-save canvas state, restore on reopen |
| **Undo/Redo** | Ctrl+Z / Ctrl+Shift+Z with 100-step history |
| **Plan/Apply from UI** | Init, plan, apply with approval gates and kill switch |
| **Resizable Panels** | Drag borders to resize sidebar, code panel, terminal |
| **Resource Search** | Filter 250+ resources by name, type, or category |
| **Resource Tooltips** | Hover to see fields, connection rules, and defaults |
| **Keyboard Shortcuts** | Ctrl+Z, Delete, Escape, and more |
| **Open in Finder** | Click to reveal project directory in your file manager |
| **Zero Config** | Just run the binary — sensible defaults for everything |

## Architecture

```
Browser (React/TS)                   Go Backend (single binary)
┌─────────────────────┐   WebSocket  ┌────────────────────────────┐
│ Visual Canvas        │◄────────────►│ Parser (HCL/YAML)          │
│ Topology Connections │   REST API   │ Generator (multi-provider)  │
│ Resource Palette     │◄────────────►│ File Watcher (debounce)     │
│ Properties Panel     │              │ SafeRunner (timeouts/kill)  │
│ AI Chat + Fix        │              │ AI Bridge (Ollama/OpenAI)   │
│ Code Preview         │              │ Security Scanner            │
│ Terminal             │              │ Drift Detector              │
│ Smart Suggestions    │              │ Multi-Format Exporter       │
│ Import Wizard        │              │ Project State Manager       │
└─────────────────────┘              │ WebSocket Hub               │
                                     └────────────┬───────────────┘
                                                  │
                                   ┌──────────────┼──────────────┐
                                   ▼              ▼              ▼
                             ~/iac-projects/   Ollama       terraform/
                             ├── main.tf       (local AI)   tofu/
                             ├── variables.tf               ansible
                             └── .iac-studio.json (state)
```

## AI Integration

IaC Studio works with **any AI provider**:

| Provider | Setup | Cost |
|----------|-------|------|
| **Ollama (local)** | `ollama pull gemma4` | Free |
| **OpenAI** | Enter API key in settings | Pay per use |
| **Groq** | Enter API key in settings | Free tier available |
| **Together** | Enter API key in settings | Free tier available |
| **Any OpenAI-compatible API** | Set endpoint + key in settings | Varies |

Click **gear icon** in the app header to switch providers at any time.

### Recommended Local Models

| RAM | Model | Size | Quality |
|-----|-------|------|---------|
| 32GB+ | `gemma4` | 9.6 GB | Best |
| 16GB+ | `gemma4` | 9.6 GB | Best |
| 8GB+ | `qwen2.5-coder:7b` | 4.7 GB | Great for code |
| 6GB+ | `gemma4:e4b` | 3 GB | Good |
| 4GB+ | `gemma4:e2b` | 2 GB | Basic |

## Security

IaC Studio runs locally and is designed to be secure by default:

- **Localhost-only binding** — not exposed to the network
- **CORS/WebSocket origin verification** — only localhost origins accepted
- **Path traversal protection** — project names validated and sandboxed
- **Request size limits** — 1MB cap on all endpoints
- **Execution safety** — command timeouts, process group kill, approval gates
- **Plan-before-apply** — server-side gate requires successful plan before apply
- **No telemetry** — zero data collection, no phone-home

## Development

```bash
git clone https://github.com/your-org/iac-studio.git
cd iac-studio

./scripts/setup.sh    # Interactive setup (recommended)
# or:
make deps             # Install Go + Node dependencies
make dev              # Hot reload (frontend :5173, backend :3001)
make test             # Run all tests
make build            # Production binary -> bin/iac-studio
make release          # Cross-compile all platforms -> dist/
make docker           # Docker image
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## Configuration

```bash
iac-studio \
  --host 127.0.0.1 \
  --port 3000 \
  --projects-dir ~/iac-projects \
  --ai-endpoint http://localhost:11434 \
  --ai-model gemma4
```

All flags have sensible defaults. Just run `iac-studio` and go.

## Roadmap

- [x] Terraform / OpenTofu / Ansible support
- [x] AWS, GCP, Azure resource catalogs (250+ resources)
- [x] AI chat with conversation context and provider awareness
- [x] AI topology builder (describe architecture in plain text)
- [x] AI plan error diagnosis and auto-fix
- [x] Visual topology with connection lines and references
- [x] Graph-based security scanner (CIS, SOC2, HIPAA, OWASP)
- [x] Drift detection from terraform.tfstate
- [x] Multi-format export (Pulumi TS, CDK Python, CloudFormation)
- [x] Smart resource suggestions (IaC best practices)
- [x] Project persistence (auto-save/restore)
- [x] Import existing projects with filesystem browser
- [x] Undo/redo with keyboard shortcuts
- [x] Resizable panels and resource search
- [x] Execution safety (timeouts, approval gates, kill switch)
- [x] Policy engine (15+ built-in guardrails)
- [x] Cost estimation (30+ resource types)
- [x] CI/CD pipeline generator (GitHub Actions, GitLab CI)
- [x] Environment promotion (dev/staging/prod workspaces)
- [ ] Module support (Terraform modules)
- [ ] State visualization (deployed resource status)
- [ ] Plugin system (custom resource types)
- [ ] Multi-user collaboration

## License

Apache License 2.0 — see [LICENSE](LICENSE).
