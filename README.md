# ◆ IaC Studio

**A local-first, open source visual builder for Infrastructure as Code.**

Build Terraform, OpenTofu, and Ansible projects through a drag-and-drop UI with an AI assistant — while keeping full control of your files on disk.

![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)
![React](https://img.shields.io/badge/React-18+-61DAFB?logo=react)

---

## Why IaC Studio?

Most IaC tools are CLI-only. Visual tools are either cloud-hosted SaaS or locked to one provider. IaC Studio is different:

- **Runs locally** — your infrastructure code never leaves your machine
- **Multi-tool** — Terraform, OpenTofu, and Ansible in one interface
- **Bidirectional sync** — edit in the UI or in your editor, both stay in sync
- **AI-powered** — local LLM converts natural language to infrastructure components
- **Open source** — Apache 2.0, no telemetry, no accounts, no cloud dependency
- **Single binary** — download one file, run it, done

## Quick Start

### Install (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/your-org/iac-studio/main/scripts/install.sh | bash
```

### Or download directly

```bash
# Linux / macOS
curl -fsSL https://github.com/your-org/iac-studio/releases/latest/download/iac-studio-$(uname -s | tr A-Z a-z)-$(uname -m) -o iac-studio
chmod +x iac-studio
./iac-studio
```

### Or use Docker

```bash
docker compose up
```

Then open **http://localhost:3000**.

## Features

| Feature | Description |
|---------|-------------|
| **Visual Canvas** | Drag-and-drop resources, see connections |
| **Live Code Gen** | Every UI change writes real `.tf` / `.yml` files |
| **Bidirectional Sync** | Edit files in VS Code, see changes in the UI instantly |
| **AI Chat** | "Add a VPC with 3 subnets" → visual components + code |
| **Run from UI** | Init, plan, apply without touching the terminal |
| **Tool Detection** | Auto-detects Terraform, OpenTofu, Ansible on your PATH |
| **Project Scaffold** | Creates proper project structure on creation |
| **Zero Config** | Just run the binary — sensible defaults for everything |

## Architecture

```
Browser (React)                    Go Backend (single binary)
┌─────────────────┐     WebSocket  ┌──────────────────────┐
│ Visual Canvas    │◄──────────────►│ Parser (HCL/YAML)    │
│ Resource Palette │     REST API  │ Generator (HCL/YAML)  │
│ Properties Panel │◄──────────────►│ File Watcher          │
│ AI Chat          │               │ CLI Runner             │
│ Code Preview     │               │ AI Bridge (Ollama)     │
│ Terminal         │               │ WebSocket Hub          │
└─────────────────┘               └──────────┬───────────┘
                                             │
                              ┌──────────────┼──────────────┐
                              ▼              ▼              ▼
                        ~/projects/     Ollama (local)   terraform/
                        ├── main.tf     AI model         tofu/
                        ├── vars.tf                      ansible
                        └── ...
```

**Bidirectional sync**: UI changes → Go generates code → writes to disk. File changes on disk → fsnotify detects → parser reads → WebSocket pushes to UI. 500ms debounce prevents echo loops.

## AI Assistant

Integrates with **Ollama** for fully private, local AI. No API keys, no data leaves your machine.

```bash
# Install Ollama (one-time)
curl -fsSL https://ollama.com/install.sh | sh
ollama pull deepseek-coder:6.7b
```

IaC Studio auto-detects Ollama on `localhost:11434`. Falls back to built-in pattern matching if Ollama isn't running.

Also supports any OpenAI-compatible API via `--ai-endpoint`.

## Development

```bash
git clone https://github.com/your-org/iac-studio.git
cd iac-studio
make deps    # Install Go + Node dependencies
make dev     # Hot reload (frontend :5173, backend :3001)
make test    # Run all tests
make build   # Production binary → bin/iac-studio
make release # Cross-compile all platforms → dist/
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines.

## Configuration

```bash
iac-studio \
  --host 127.0.0.1 \
  --port 3000 \
  --projects-dir ~/iac-projects \
  --ai-endpoint http://localhost:11434 \
  --ai-model deepseek-coder:6.7b
```

All flags have sensible defaults. Just run `iac-studio` and go.

## Roadmap

- [x] Terraform support
- [x] OpenTofu support
- [x] Ansible support
- [x] AI chat with Ollama
- [x] Bidirectional file sync
- [ ] Connection lines between resources
- [ ] Import existing projects
- [ ] Azure / GCP resource palettes
- [ ] Pulumi support
- [ ] State visualization
- [ ] Module support
- [ ] Plugin system

## License

Apache License 2.0 — see [LICENSE](LICENSE).
