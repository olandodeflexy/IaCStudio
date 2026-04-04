# Contributing to IaC Studio

Thanks for your interest in contributing! IaC Studio is a community-driven project and we welcome contributions of all kinds.

## Getting Started

1. Fork the repo and clone it locally
2. Install dependencies: `make deps`
3. Run in dev mode: `make dev`
4. Open http://localhost:5173 (frontend with hot reload, proxied to Go backend on :3001)

## Development Workflow

### Backend (Go)

The Go backend lives in `cmd/` and `internal/`. Key packages:

- **`internal/parser/`** — Reads `.tf` and `.yml` files into `Resource` structs
- **`internal/generator/`** — Converts `Resource` structs back to IaC code
- **`internal/watcher/`** — `fsnotify`-based file watcher with debounce
- **`internal/runner/`** — Wraps CLI tools (`terraform`, `tofu`, `ansible-playbook`)
- **`internal/ai/`** — Ollama client + fallback pattern matching
- **`internal/api/`** — HTTP router + WebSocket hub

Run backend tests:
```bash
go test ./... -v -race
```

### Frontend (React + TypeScript)

The frontend lives in `web/`. Key files:

- **`src/App.tsx`** — Main application component
- **`src/api.ts`** — API client for the Go backend
- **`src/useWebSocket.ts`** — WebSocket hook for live sync

Run frontend in dev mode:
```bash
cd web && npm run dev
```

## What We Need Help With

### High Priority
- **More cloud providers** — Azure, GCP resource palettes and code generation
- **Ansible module coverage** — expanding beyond the initial set
- **Parser robustness** — handling complex HCL (modules, data sources, locals)
- **Tests** — unit tests for parsers, generators, and API handlers

### Medium Priority
- **Canvas improvements** — connection lines between resources, zoom/pan, grouping
- **State management** — displaying `terraform.tfstate` resources in the UI
- **Import existing projects** — parse an existing Terraform/Ansible project into the canvas
- **AI prompt engineering** — better system prompts for different models

### Nice to Have
- **Pulumi support** — TypeScript/Python IaC
- **CloudFormation support** — YAML/JSON templates
- **Plugin system** — user-contributed resource types
- **Themes** — light mode, custom color schemes

## Pull Request Process

1. Create a feature branch from `main`
2. Write tests for new functionality
3. Run `make test` and `make lint` before submitting
4. Keep PRs focused — one feature or fix per PR
5. Update documentation if you change APIs or add features

## Code Style

### Go
- Follow standard Go conventions (`gofmt`, `golangci-lint`)
- Use meaningful variable names, not abbreviations
- Error handling: always handle errors, wrap with context using `fmt.Errorf("doing X: %w", err)`

### TypeScript/React
- Functional components with hooks
- No external state management libraries (React state + context is sufficient)
- Inline styles for now (we may move to CSS modules later)

## Adding a New IaC Tool

To add support for a new IaC tool (e.g., Pulumi):

1. Add a parser in `internal/parser/` that implements the `Parser` interface
2. Add a generator in `internal/generator/` that implements the `Generator` interface
3. Add the tool definition in `web/src/App.tsx` (TOOLS object)
4. Add CLI commands in `internal/runner/runner.go`
5. Add detection in `runner.DetectTools()`
6. Add AI patterns in `internal/ai/bridge.go`

## Adding a New Cloud Provider

To add resources for a new cloud provider:

1. Add resource definitions in the frontend TOOLS object
2. Add default properties for code generation
3. Add AI pattern matching for the new resource types
4. Update documentation with examples

## Reporting Issues

- Use GitHub Issues
- Include: OS, Go version, Node version, browser
- For bugs: steps to reproduce, expected vs actual behavior
- For features: describe the use case, not just the solution

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
