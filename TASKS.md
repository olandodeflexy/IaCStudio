# TASKS.md — Ordered Development Tasks

Each task is self-contained with clear acceptance criteria. Work through them in order — each builds on the previous.

---

## Phase 1: Make It Compile and Run (Foundation)

### Task 1.1: Fix go.mod and get the project compiling

The go.mod has dependency declarations but no go.sum. The code compiles conceptually but hasn't been run through `go build` yet.

**Steps:**
1. Run `go mod tidy` to generate go.sum
2. Fix any import path issues (the module is `github.com/iac-studio/iac-studio`)
3. The HCL parser uses `hclsyntax` and `hcl` packages — make sure the import for `ctyToInterface` properly handles `cty.Value` types (currently uses a simplified `fmt.Sprintf`)
4. Run `go build ./cmd/server` — fix all compilation errors
5. Run `go vet ./...` — fix all warnings
6. Run `go test ./...` — all tests should pass

**Acceptance criteria:**
- `go build ./cmd/server` succeeds with zero errors
- `go test ./...` passes all tests
- `go vet ./...` clean

### Task 1.2: Set up the frontend build

**Steps:**
1. `cd web && npm install`
2. Convert `App.tsx` to proper TypeScript (add missing type annotations)
3. Add `tsconfig.json` for the web/ directory
4. Run `npm run build` — fix any TypeScript/Vite errors
5. Verify `web/dist/` is produced

**Acceptance criteria:**
- `cd web && npm run build` succeeds
- `web/dist/index.html` exists
- No TypeScript errors

### Task 1.3: Wire go:embed and run end-to-end

**Steps:**
1. The `cmd/server/main.go` has `//go:embed web/dist/*` — this requires the embed path to be relative to the file. Since main.go is in `cmd/server/`, the embed directive needs to reference `../../web/dist/*` OR the frontend build output needs to be copied. The cleaner approach: change the embed to be in a separate file at the project root.
2. Create `embed.go` at project root:
   ```go
   package main // won't work — needs to be in cmd/server
   ```
   Better approach: build frontend first, then `cp -r web/dist cmd/server/web/dist` before Go build, and keep the embed directive as-is. Update the Makefile `build-backend` target to do this copy.
3. Run `make build` end-to-end
4. Run `./bin/iac-studio` and verify http://localhost:3000 serves the React app
5. Verify API endpoints respond (curl http://localhost:3000/api/health)

**Acceptance criteria:**
- `make build` produces a single binary
- Running the binary serves the React frontend at /
- `/api/health` returns `{"status":"ok","version":"0.1.0"}`
- `/api/tools` returns detected tools

---

## Phase 2: Connect Frontend to Backend (Core Loop)

### Task 2.1: Frontend API integration — tool detection

**Steps:**
1. On the tool selection screen, call `api.detectTools()` on mount
2. Display which tools are installed (green checkmark + version) vs not installed
3. Handle the case where the backend is not running (show a "Backend not connected" message with instructions)

**Acceptance criteria:**
- Tool selection screen shows installed tools with versions
- Shows "not installed" for missing tools
- Gracefully handles backend being down

### Task 2.2: Project creation flow

**Steps:**
1. When user selects a tool, prompt for project name (pre-filled with "my-infra")
2. Call `api.createProject(name, tool)` — this scaffolds files on disk
3. After creation, load the editor view
4. Display the project path in the header

**Acceptance criteria:**
- Selecting a tool creates `~/iac-projects/{name}/` with scaffold files
- `main.tf` (or `site.yml`) exists on disk with proper initial content
- Editor loads with the correct tool context

### Task 2.3: Bidirectional sync — UI to disk

**Steps:**
1. When nodes change in the canvas, debounce 1 second then call `api.syncToDisk()`
2. The backend generates code and writes to `main.{tf,yml}`
3. Display the generated code in the Code Preview panel (right side)
4. Show a subtle "Saved" indicator in the header when sync completes

**Acceptance criteria:**
- Adding a resource in the UI creates the corresponding block in main.tf on disk
- Editing a property updates the file on disk within ~1 second
- Removing a resource removes it from the file
- Code Preview panel always shows current file content

### Task 2.4: Bidirectional sync — disk to UI

**Steps:**
1. Connect to the WebSocket on mount (`useWebSocket` hook)
2. When a `file_changed` message arrives, call `api.getResources()` to re-parse
3. Merge parsed resources with existing canvas positions (match by resource ID `type.name`)
4. Show a notification toast: "File changed: main.tf"
5. Handle new resources (assign random canvas position)
6. Handle deleted resources (remove from canvas)

**Acceptance criteria:**
- Edit main.tf in an external editor → canvas updates within 1 second
- Adding a resource block in the file → new node appears on canvas
- Deleting a resource block → node disappears from canvas
- Existing node positions are preserved during sync

### Task 2.5: Run commands (init/plan/apply)

**Steps:**
1. Wire the header buttons to `api.runCommand()`
2. The backend runs the CLI command and streams output via WebSocket
3. Display output in the Terminal panel at the bottom
4. Color code: green for success, red for errors, blue for resource additions
5. Disable the buttons while a command is running
6. Show a spinner or "Running..." state

**Acceptance criteria:**
- Clicking "Init" runs `terraform init` in the project dir
- Clicking "Plan" runs `terraform plan` and shows output
- Clicking "Apply" runs `terraform apply -auto-approve`
- Terminal shows real CLI output, not simulated
- Errors are displayed in red

---

## Phase 3: AI Integration

### Task 3.1: Wire AI chat to Ollama

**Steps:**
1. When user sends a chat message, call `api.chat(message, tool)`
2. The backend tries Ollama first, falls back to pattern matching
3. Display the AI response in the chat window
4. If the AI returns resources, add them to the canvas
5. Show a "thinking..." state while waiting for the AI

**Acceptance criteria:**
- With Ollama running: AI generates proper resources from natural language
- Without Ollama: pattern matching provides basic resources
- Resources appear on canvas immediately after AI responds
- Chat shows conversation history

### Task 3.2: Improve AI system prompt

**Steps:**
1. The current system prompt in `internal/ai/bridge.go` (`buildSystemPrompt`) is basic
2. Enhance it with:
   - Full list of available resource types for the current tool
   - Connection rules (e.g., "subnets must reference a VPC via vpc_id")
   - Naming conventions
   - Best practices (e.g., "always enable DNS support on VPCs")
3. Add a few-shot example in the prompt showing expected JSON output
4. Test with various natural language inputs

**Acceptance criteria:**
- "Create a VPC with 3 subnets" → VPC + 3 subnets with proper CIDR blocks
- "Set up a web server with a load balancer" → ALB + EC2 + SG
- Resources have sensible default values
- Connection relationships are established

### Task 3.3: Streaming AI responses

**Steps:**
1. Change the Ollama API call to use `stream: true`
2. Stream tokens to the frontend via WebSocket as they arrive
3. Update the chat message in real-time (typing effect)
4. Parse the final JSON response to extract resources

**Acceptance criteria:**
- AI responses stream in word by word
- Resources are added to canvas after the complete response
- No UI freezing during generation

---

## Phase 4: Canvas Improvements

### Task 4.1: Canvas pan and zoom

**Steps:**
1. Add mousewheel zoom (scale transform on the canvas container)
2. Add middle-click or space+drag to pan
3. Show zoom level indicator
4. Add "Fit to view" button that centers all nodes
5. Persist zoom/pan state per project

**Acceptance criteria:**
- Scroll to zoom in/out
- Space + drag to pan
- Double-click empty space to fit all nodes in view
- Zoom level shown in corner

### Task 4.2: Auto-layout

**Steps:**
1. Implement a basic graph layout algorithm (dagre-like):
   - Resources with no dependencies at the top
   - Connected resources flow downward
   - Group by category (networking left, compute center, database right)
2. Add an "Auto Layout" button in the header
3. Animate nodes to their new positions

**Acceptance criteria:**
- Clicking "Auto Layout" arranges nodes in a readable graph
- Connected nodes are visually close to each other
- No overlapping nodes
- Smooth animation to new positions

### Task 4.3: Connection line improvements

**Steps:**
1. Current SVG lines are basic bezier curves — improve with:
   - Arrowheads on the target end
   - Animated dashes (CSS animation on stroke-dashoffset)
   - Hover highlight (thicker + brighter on hover)
   - Click to select a connection (show delete option)
2. Smart routing to avoid overlapping with nodes

**Acceptance criteria:**
- Lines have arrowheads
- Lines animate subtly
- Hovering a line highlights it
- Can delete connections by clicking them

### Task 4.4: Multi-select and group operations

**Steps:**
1. Shift+click to add to selection
2. Drag to create selection rectangle
3. Move all selected nodes together
4. Delete all selected nodes (Delete key)
5. Visual indicator for selected nodes (highlight border)

**Acceptance criteria:**
- Can select multiple nodes
- Moving one selected node moves all
- Delete key removes all selected
- Escape clears selection

---

## Phase 5: Import Existing Projects

### Task 5.1: Parse existing Terraform projects

**Steps:**
1. Add a "Open Existing Project" option to the start screen
2. File picker (or path input) to select a directory
3. Run the parser on all .tf files
4. Assign canvas positions using auto-layout
5. Detect connections from resource references (e.g., `vpc_id = aws_vpc.main.id`)
6. Start the file watcher on the directory

**Acceptance criteria:**
- Can open any existing Terraform project
- All resources appear on canvas
- References between resources create connection lines
- Files are NOT modified (read-only import)

### Task 5.2: Parse existing Ansible projects

**Steps:**
1. Same as 5.1 but for Ansible playbooks
2. Parse all .yml/.yaml files in the directory
3. Handle roles directory structure
4. Handle includes and imports

**Acceptance criteria:**
- Can open existing Ansible projects
- Tasks from all playbooks appear on canvas
- Role tasks are included

---

## Phase 6: Enhanced Features

### Task 6.1: State visualization

**Steps:**
1. Parse `terraform.tfstate` to show deployed resources
2. Color code nodes: green = deployed, yellow = changed, red = will be destroyed
3. Compare state with code to highlight drift
4. Show resource attributes from state on hover

**Acceptance criteria:**
- After `terraform apply`, nodes show deployment status
- Running `terraform plan` highlights which nodes will change
- Drift is visually indicated

### Task 6.2: Module support (Terraform)

**Steps:**
1. Add a "Module" node type that represents a Terraform module
2. Module nodes expand to show internal resources
3. Support local modules (./modules/vpc) and registry modules
4. Generate proper module blocks in HCL

**Acceptance criteria:**
- Can add module nodes to canvas
- Modules can reference other resources
- Generated code uses proper `module {}` blocks

### Task 6.3: Variables and outputs management

**Steps:**
1. Add a Variables panel (tab in the sidebar)
2. UI to add/edit/delete variables with name, type, default, description
3. Write to `variables.tf` (or playbook vars)
4. Reference variables in resource properties (autocomplete `var.xxx`)
5. Similarly for outputs

**Acceptance criteria:**
- Can manage variables through the UI
- `variables.tf` stays in sync
- Resource properties can reference variables

### Task 6.4: Provider configuration

**Steps:**
1. Provider selection UI (AWS, Azure, GCP)
2. Region picker
3. Credential configuration hints (env vars, AWS profiles)
4. Multiple provider support

**Acceptance criteria:**
- Can configure provider and region from UI
- `providers.tf` is generated
- Supports multi-region setups

---

## Phase 7: Polish and Distribution

### Task 7.1: Error handling and edge cases

**Steps:**
1. Handle malformed HCL/YAML gracefully (show errors, don't crash)
2. Handle concurrent file writes (multiple browser tabs)
3. Handle disk full, permission errors
4. Add proper logging (structured, with levels)
5. Add request timeout handling

### Task 7.2: One-line installer

**Steps:**
1. Test `scripts/install.sh` on Linux (amd64, arm64), macOS (both archs), and WSL
2. Add checksum verification after download
3. Add update command: `iac-studio update`
4. Add uninstall: `iac-studio uninstall`

### Task 7.3: Documentation site

**Steps:**
1. Create docs/ with mkdocs or similar
2. Getting started guide
3. Tutorial: build a VPC from scratch
4. Tutorial: import existing project
5. API reference
6. Contributing guide with architecture walkthrough

### Task 7.4: Plugin system (stretch)

**Steps:**
1. Define plugin interface for adding resource types
2. YAML-based resource definitions that can be loaded at startup
3. Plugin directory: `~/.iac-studio/plugins/`
4. Community resource packs (Azure, GCP, Kubernetes)
