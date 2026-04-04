# DESIGN.md — Design Decisions, Gotchas, and Known Issues

## Design Decisions (and why)

### Why Go over Rust?

Decided Go because:
- The entire IaC ecosystem (Terraform, OpenTofu, Packer) is written in Go
- Can directly import `hclwrite` to parse/generate HCL natively
- Single binary with `CGO_ENABLED=0` (same as Rust)
- Goroutines make WebSocket hub + file watcher + CLI runner trivial
- Most DevOps engineers know Go, lowering contribution barrier

Rust could be used for a high-performance parser subsystem if needed later, but Go handles the parsing fine for projects with thousands of resources.

### Why go:embed over separate frontend server?

Single binary = zero deployment friction. Users download one file and run it. No Node.js required on the user's machine. The frontend is built at compile time and baked into the Go binary.

### Why WebSocket over SSE or polling?

Need bidirectional communication:
- Server → Client: file change notifications, terminal output
- Client → Server: state updates (canvas positions)

SSE is one-directional. Polling adds latency. WebSocket gives us both directions with low latency.

### Why fsnotify over inotify directly?

fsnotify is cross-platform (Linux inotify, macOS kqueue, Windows ReadDirectoryChanges). Since we ship one binary for all platforms, we need the abstraction.

### Why .iac-studio.json over a database?

- No database dependency (SQLite would require CGO on some platforms)
- State file lives in the project directory — travels with git
- Easy to inspect and manually edit
- Each project is independent

### Why pattern matching fallback for AI?

Many users won't have Ollama installed or a GPU. The app must be fully functional without AI. Pattern matching covers the 80% case (common resources). Users get a working experience and are motivated to install Ollama for the full experience.

### Why no authentication/authorization?

IaC Studio binds to `127.0.0.1` by default — only accessible from the local machine. Adding auth would add friction for a tool that's meant to be as easy as opening a text editor. If users want to expose it on a network, they can use a reverse proxy with auth.

---

## Known Issues and Gotchas

### 1. HCL Parser — Expression evaluation

The current HCL parser in `internal/parser/parser.go` uses `attr.Expr.Value(nil)` to evaluate expressions. This works for literals but fails for:
- References: `aws_vpc.main.id` (returns nil context error)
- Functions: `cidrsubnet(...)` (returns nil context error)
- Variables: `var.region` (returns nil context error)

**Fix needed**: Instead of evaluating expressions, extract them as raw strings. Use `hclwrite` to read the raw expression bytes:
```go
// Instead of:
val, diags := attr.Expr.Value(nil)

// Do:
tokens := attr.Expr.(*hclsyntax.LiteralValueExpr) // or walk the expression
// Or use hclwrite to get raw bytes
```

For resource references like `vpc_id = aws_vpc.main.id`, the parser should store the raw string `"aws_vpc.main.id"` as the property value. The frontend can then detect this is a reference and create a connection.

### 2. HCL Generator — Doesn't handle resource references

The current generator wraps all values in quotes:
```go
b.WriteString(fmt.Sprintf("  %s = \"%v\"\n", k, val))
```

This breaks for references. `vpc_id = "aws_vpc.main.id"` is a string literal, not a reference. Need to detect references and output them unquoted:
```hcl
vpc_id = aws_vpc.main.id  # correct — this is a reference
```

**Fix**: If a property value matches the pattern `{resource_type}.{resource_name}.{attribute}`, emit it without quotes.

### 3. Ansible YAML parser — Limited structure handling

The parser only handles flat playbook structure:
```yaml
- name: Playbook
  hosts: all
  tasks:
    - name: Task
      module: {...}
```

It doesn't handle:
- `include_tasks` / `import_tasks`
- Role-based structure (`roles/rolename/tasks/main.yml`)
- `handlers`
- `pre_tasks` / `post_tasks`
- `block` / `rescue` / `always`
- Variables files (`vars/main.yml`, `group_vars/`)

These should be added incrementally.

### 4. File watcher — Doesn't watch subdirectories

`fsnotify.Watcher.Add()` only watches the specified directory, not subdirectories. If a user creates a new `.tf` file in a subdirectory (e.g., `modules/vpc/main.tf`), we won't detect it.

**Fix**: Walk the directory tree on startup and add all subdirs. Also watch for `CREATE` events on directories and add them dynamically.

### 5. Concurrent file writes — Last write wins

If two browser tabs are open and both make changes, the last write wins. There's no conflict resolution or OT/CRDT.

**Current approach**: This is acceptable for v1. IaC Studio is designed for single-user local use. If collaborative editing is added later, consider using CRDTs for the canvas state.

### 6. go:embed path issue

The `cmd/server/main.go` file has:
```go
//go:embed web/dist/*
var frontendFS embed.FS
```

But `web/dist/` is not relative to `cmd/server/`. The embed directive looks for paths relative to the Go source file.

**Fix options**:
1. Copy `web/dist/` to `cmd/server/web/dist/` before building (add to Makefile)
2. Move the embed directive to a file at the project root and pass it to the server
3. Use a build script that symlinks

Option 1 is simplest. Update Makefile:
```makefile
build-backend:
    mkdir -p cmd/server/web
    cp -r web/dist cmd/server/web/dist
    CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(APP_NAME) ./cmd/server
```

### 7. Frontend TypeScript — Needs type cleanup

The `App.tsx` was written quickly with `any` types in several places. Needs:
- Proper interfaces for all data structures
- Remove `any` casts
- Add `tsconfig.json` with strict mode
- Separate components (Canvas, Sidebar, Properties, Chat, Terminal)

### 8. Canvas performance — Large projects

With 100+ nodes, the canvas will slow down because:
- Each node is an absolute-positioned div (triggers layout on drag)
- SVG lines are re-rendered on every state change
- No virtualization (all nodes render even if off-screen)

**Fix for later**: Use `<canvas>` or a WebGL renderer for large projects. For now, the DOM approach works fine up to ~50 nodes.

### 9. Resource ID mismatch between parser and UI

- Parser generates IDs like `aws_vpc.main` (from HCL `resource "aws_vpc" "main"`)
- UI generates IDs like `node_1_1711000000` (random)

When syncing disk → UI, we need to match by `type + name`, not by ID. The current code in `App.tsx` matches by ID which will fail.

**Fix**: Match resources by composite key `${type}.${name}` when merging disk changes into the canvas.

### 10. No TLS/HTTPS

The server only does HTTP. For local use this is fine. If someone exposes it on a network, they should use a reverse proxy (nginx, caddy) for TLS.

---

## Performance Targets

| Metric | Target | Notes |
|--------|--------|-------|
| Binary size | < 30MB | Frontend + Go, compressed |
| Startup time | < 500ms | Including tool detection |
| UI → Disk sync | < 1.5s | Includes debounce |
| Disk → UI sync | < 1s | Includes debounce + parse |
| AI response | < 10s | Ollama dependent |
| Canvas drag | 60fps | Up to 50 nodes |
| Memory (idle) | < 50MB | Go runtime + embedded frontend |

---

## Security Considerations

1. **Local only by default** — Binds to 127.0.0.1, not 0.0.0.0
2. **No secrets in state** — .iac-studio.json stores canvas state, not credentials
3. **CLI passthrough** — Runner executes terraform/ansible as a child process with the user's existing credentials and environment
4. **AI is local** — Ollama runs on the user's machine, no data leaves
5. **No eval** — The backend never evaluates user-supplied code; it only parses and generates
6. **CORS is permissive** — Since it's local-only, CORS allows all origins. If exposed on a network, this must be restricted
