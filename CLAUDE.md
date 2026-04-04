# CLAUDE.md — IaC Studio Project Context

## What is this project?

IaC Studio is a **local-first, open source visual builder for Infrastructure as Code**. It lets DevOps engineers build Terraform, OpenTofu, and Ansible projects through a drag-and-drop UI with an embedded AI chat — while keeping full control of real files on disk.

**Key principle**: This is NOT a cloud SaaS. It runs as a single binary on the user's machine. No accounts, no telemetry, no cloud dependency. Apache 2.0 licensed.

## Tech Stack

- **Backend**: Go 1.22+ (single binary, no CGO)
- **Frontend**: React 18 + TypeScript + Vite
- **Real-time sync**: gorilla/websocket
- **IaC parsing**: hashicorp/hcl/v2 (Terraform/OpenTofu), gopkg.in/yaml.v3 (Ansible)
- **File watching**: fsnotify/fsnotify
- **AI**: Ollama API (local LLM), with fallback pattern matching
- **Distribution**: go:embed for frontend, cross-compiled to 6 platforms

## Architecture Overview

```
Browser (React/TS)  ←— WebSocket + REST —→  Go Backend (single binary)
     │                                            │
     ├── Visual Canvas (drag-drop nodes)          ├── Parser (HCL ↔ Resource structs)
     ├── Resource Palette                         ├── Generator (Resource structs → HCL/YAML)
     ├── Properties Panel                         ├── FileWatcher (fsnotify, 500ms debounce)
     ├── AI Chat Window                           ├── Runner (wraps terraform/tofu/ansible CLIs)
     ├── Code Preview (live)                      ├── AI Bridge (Ollama client)
     ├── Terminal Output                          ├── WebSocket Hub (broadcast to all clients)
     └── File Explorer                            ├── Catalog (resource registry)
                                                  └── Project Manager (state persistence)
                                                       │
                                              ~/iac-projects/{name}/
                                              ├── main.tf        ← real files on disk
                                              ├── variables.tf
                                              └── ...
```

## Bidirectional Sync (Critical Design)

This is the core differentiator. Changes flow BOTH directions:

1. **UI → Disk**: User adds/edits resource in canvas → Go generates code → writes to project dir
2. **Disk → UI**: User edits file in VS Code → fsnotify detects change → parser reads file → WebSocket pushes new state to browser
3. **Echo prevention**: When the backend writes files (UI→Disk), it calls `watcher.Pause(dir)` before writing and `watcher.Resume(dir)` after. This prevents the write from triggering a Disk→UI cycle.
4. **Debounce**: 500ms debounce on file events (editors save multiple times rapidly)

## Project Structure

```
iac-studio/
├── cmd/server/main.go              # Entry point, CLI flags, graceful shutdown, go:embed
├── internal/
│   ├── api/
│   │   ├── router.go               # All REST endpoints + WebSocket upgrade
│   │   └── ws.go                   # WebSocket hub (register/unregister/broadcast)
│   ├── parser/
│   │   ├── parser.go               # Parser interface + HCL + YAML implementations
│   │   └── parser_test.go
│   ├── generator/
│   │   ├── generator.go            # Generator interface + HCL + YAML implementations
│   │   └── generator_test.go
│   ├── watcher/
│   │   └── watcher.go              # fsnotify wrapper with pause/resume/debounce
│   ├── runner/
│   │   ├── runner.go               # CLI wrapper (terraform, tofu, ansible-playbook)
│   │   └── runner_test.go
│   ├── ai/
│   │   ├── bridge.go               # Ollama client + fallback pattern matching
│   │   └── bridge_test.go
│   ├── catalog/
│   │   └── catalog.go              # Full resource registry (50+ resources)
│   └── project/
│       └── project.go              # Project state manager (.iac-studio.json)
├── web/                            # React frontend
│   ├── src/
│   │   ├── main.tsx                # React entry
│   │   ├── App.tsx                 # Main app component (full UI)
│   │   ├── api.ts                  # REST API client
│   │   └── useWebSocket.ts         # WebSocket hook with auto-reconnect
│   ├── index.html
│   ├── package.json
│   └── vite.config.ts              # Dev proxy to Go backend
├── .github/workflows/ci.yml        # CI/CD: test, release, docker
├── Makefile                        # build, dev, test, release, docker, install
├── Dockerfile                      # Multi-stage (node → go → alpine, ~50MB)
├── docker-compose.yml              # IaC Studio + Ollama sidecar
├── go.mod
├── CONTRIBUTING.md
├── LICENSE                         # Apache 2.0
└── README.md
```

## How to Run in Development

```bash
make deps    # go mod tidy + cd web && npm install
make dev     # Runs Go backend on :3001 + Vite dev server on :5173 (with proxy)
```

Vite proxies `/api/*` and `/ws` to `localhost:3001` (the Go backend).

## How to Build

```bash
make build     # Builds frontend, then Go binary with embedded frontend → bin/iac-studio
make release   # Cross-compiles for linux/darwin/windows × amd64/arm64 → dist/
make docker    # Multi-stage Docker image
```

## Coding Conventions

### Go
- Standard Go project layout (cmd/, internal/)
- Interfaces for testability (Parser, Generator, Broadcaster)
- Error wrapping: `fmt.Errorf("doing X: %w", err)`
- No external web frameworks — just net/http ServeMux (Go 1.22 routing)
- `CGO_ENABLED=0` always — pure Go, no system deps
- Tests in `*_test.go` files next to implementation

### TypeScript/React
- Functional components with hooks only
- No external state management (useState + context)
- Inline styles (may move to CSS modules later)
- API calls in `src/api.ts`, WebSocket in `src/useWebSocket.ts`
- Types colocated with their module

### API Design
- REST for CRUD operations
- WebSocket for real-time sync (file changes, terminal output)
- All responses are JSON
- Endpoints follow pattern: `/api/projects/{name}/action`

## Key API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | /api/health | Health check |
| GET | /api/tools | Detect installed IaC tools |
| GET | /api/projects | List projects |
| POST | /api/projects | Create project (scaffolds files) |
| GET | /api/projects/{name}/resources | Parse project → resource list |
| POST | /api/projects/{name}/sync | UI resources → write to disk |
| POST | /api/projects/{name}/run | Execute IaC command (init/plan/apply) |
| POST | /api/ai/chat | AI natural language → resources |
| GET | /ws | WebSocket upgrade for live sync |

## WebSocket Message Types

```json
// Server → Client: file changed on disk
{"type": "file_changed", "project": "my-infra", "file": "/path/to/main.tf", "tool": "terraform"}

// Server → Client: terminal output from CLI command
{"type": "terminal", "project": "my-infra", "output": "...", "error": "..."}

// Client → Server: resource state update (for state persistence)
{"type": "state_update", "project": "my-infra", "resources": [...]}
```

## Important Design Decisions

1. **Single binary distribution** — Frontend is embedded via `go:embed`. Users download one file and run it.
2. **Zero config** — Auto-detects tools from PATH, Ollama on localhost:11434, binds to 127.0.0.1:3000.
3. **State file** — `.iac-studio.json` in each project stores canvas positions and connections (NOT in a database).
4. **Resource connections** — Resources reference each other via `connectsVia` map (e.g., subnet → vpc via `vpc_id`). This generates proper Terraform references like `vpc_id = aws_vpc.main.id`.
5. **AI fallback** — If Ollama is not running, pattern matching provides basic resource generation so the app is still useful without AI.
6. **Catalog-driven** — Resource types, defaults, fields, and connection rules come from `internal/catalog/`. Adding a new resource = adding an entry to the catalog.

## What's Working

- ✅ Go project structure with all packages
- ✅ HCL parser (reads .tf files → Resource structs)
- ✅ YAML parser (reads Ansible playbooks → Resource structs)
- ✅ HCL generator (Resource structs → .tf files)
- ✅ YAML generator (Resource structs → Ansible playbooks)
- ✅ File watcher with debounce and pause/resume
- ✅ CLI runner for terraform/tofu/ansible
- ✅ Tool auto-detection
- ✅ Ollama AI client with fallback
- ✅ WebSocket hub
- ✅ REST API router
- ✅ Resource catalog (50+ resources)
- ✅ Project state manager
- ✅ Unit tests for parser, generator, runner, AI
- ✅ React frontend with full UI
- ✅ API client + WebSocket hook
- ✅ CI/CD pipeline
- ✅ Dockerfile + docker-compose
- ✅ Makefile for all build targets

## Recent Additions (New Packages)

These packages are implemented but not yet wired into the API router or frontend:

### `internal/catalog/dynamic.go` — Dynamic Provider Schemas
Instead of hardcoding 50 resources, this runs `terraform providers schema -json` to fetch ALL resources from any installed Terraform provider (AWS has 1,500+). It:
- Creates a temp directory, writes a minimal config with requested providers
- Runs `terraform init` + `terraform providers schema -json`
- Parses the JSON output into our Resource catalog format
- Caches schemas for 24 hours in `~/.iac-studio/cache/`
- Auto-detects connections from field names (vpc_id → aws_vpc, etc.)
- Guesses icons and categories from resource type names

**To wire in**: Add a `GET /api/catalog/dynamic?provider=aws` endpoint in router.go that calls `DynamicCatalog.FetchProviderSchema()` and returns the converted resources. The frontend can merge these with the hardcoded catalog.

### `internal/templates/templates.go` — Infrastructure Templates
Pre-built patterns users can deploy with one click:
- VPC with public & private subnets (8 resources, fully connected)
- Web app with ALB (6 resources)
- High-availability RDS (2 resources)
- Serverless REST API (3 resources)
- Static site with S3 + CloudFront
- Monitoring stack
- Ansible: NGINX server, Docker host, server hardening

**To wire in**: Add `GET /api/templates?tool=terraform` endpoint. Frontend needs a "Templates" tab in the sidebar that shows these as cards.

### `internal/validator/validator.go` — Resource Validation
Validates resources before plan/apply:
- Required field checks
- CIDR block format validation
- Subnet overlap detection
- S3 bucket name rules
- Lambda memory/timeout bounds
- Duplicate resource name detection
- Cross-resource checks (subnets without VPC, EC2 without SG)
- Returns issues with severity (error/warning/info) and suggested fixes

**To wire in**: Add `POST /api/projects/{name}/validate` endpoint. Frontend should show validation issues as colored badges on canvas nodes and a validation panel.

### `internal/git/git.go` — Git Integration
Full Git operations for version control from the UI:
- Auto-init repos on project creation
- Status (branch, staged, modified, untracked files)
- Add, commit with auto-generated messages
- Diff with unified format
- Log with commit history
- Branch list, create, checkout

**To wire in**: Add git endpoints (`GET /api/projects/{name}/git/status`, `POST /api/projects/{name}/git/commit`, etc.). Frontend needs a "Git" tab or panel showing status and commit button.

### `web/src/useHistory.ts` — Undo/Redo
State history hook for the frontend canvas:
- Maintains past/future stacks (max 100 entries)
- `set()` pushes current state, clears future
- `undo()` pops from past, pushes to future
- `redo()` pops from future, pushes to past
- `reset()` clears all history

**To wire in**: Replace `useState` for nodes with `useHistory`. Wire Ctrl+Z/Ctrl+Y via the keyboard shortcuts hook.

### `web/src/useKeyboardShortcuts.ts` — Keyboard Shortcuts
Global keyboard handler that ignores input fields:
- Format: `"ctrl+z"`, `"ctrl+shift+z"`, `"delete"`, `"escape"`
- Skips events when typing in input/textarea
- Always allows Escape in inputs

**To wire in**: Call from App.tsx with a shortcut map for undo, redo, delete, escape, save, etc.

## Current Focus

Currently working on **Phase 1: getting the project to compile and run** (Tasks 1.1–1.3 in TASKS.md).

## Verification Commands

After making changes, verify with:

```bash
# Backend
go build ./cmd/server          # Must compile cleanly
go vet ./...                   # No warnings
go test ./... -v -race -cover  # All tests pass

# Frontend
cd web && npm run build        # Must produce web/dist/
```

Full test suite: `make test`

## Guardrails

- **No external web frameworks** in the Go backend — use only `net/http` ServeMux (Go 1.22 routing)
- **No CSS-in-JS libraries** in the frontend — inline styles for now, CSS modules later
- **No external state management** (Redux, Zustand, etc.) — use `useState` + React context
- **CGO_ENABLED=0 always** — the binary must be pure Go with zero system dependencies
- **No telemetry, analytics, or cloud calls** — this is a local-first, privacy-respecting tool
- **Don't modify user's IaC files** unless explicitly triggered by a sync or apply action

## What Needs To Be Done (Priority Order)

See TASKS.md for the complete, ordered task list.
