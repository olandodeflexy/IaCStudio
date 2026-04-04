# QUICKSTART.md — First Commands for Claude Code

## When starting a Claude Code session on this project:

```bash
# 1. Verify Go is available
go version

# 2. Install Go dependencies
go mod tidy

# 3. Check it compiles
go build ./cmd/server

# 4. Run tests
go test ./... -v -count=1

# 5. Install frontend deps
cd web && npm install && cd ..

# 6. Build frontend
cd web && npm run build && cd ..

# 7. Run the server (if you want to test it)
go run ./cmd/server --port 3000
```

## Key files to read first:

1. **CLAUDE.md** — Full project context (architecture, conventions, what's done)
2. **TASKS.md** — Ordered task list (start with Phase 1)
3. **API.md** — Complete API specification with examples
4. **DESIGN.md** — Design decisions and known issues/gotchas

## Common development commands:

```bash
# Run just the backend
go run ./cmd/server --port 3001

# Run just the frontend (hot reload, proxies to :3001)
cd web && npm run dev

# Run both (what `make dev` does)
# Terminal 1: go run ./cmd/server --port 3001
# Terminal 2: cd web && npm run dev

# Run a specific test file
go test ./internal/parser/ -v -run TestHCLParser

# Add a new Go dependency
go get github.com/some/package
go mod tidy

# Check for issues
go vet ./...
```

## If something doesn't compile:

The most likely issue is the `go:embed` directive in `cmd/server/main.go`. It references `web/dist/*` which needs to exist. For development, either:
1. Build the frontend first: `cd web && npm run build && cd ..`
2. Or comment out the embed and serve frontend from Vite dev server instead

## Project conventions:

- Go code: standard library style, `internal/` packages, interfaces for testability
- Frontend: React functional components, TypeScript, inline styles
- Tests: `*_test.go` next to implementation, table-driven tests preferred
- Errors: always wrap with context `fmt.Errorf("doing X: %w", err)`
- No external Go web frameworks — just `net/http`
