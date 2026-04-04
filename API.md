# API.md — Complete API Specification

Base URL: `http://localhost:3000` (configurable via `--port`)

All endpoints return JSON. Errors return `{"error": "message"}` with appropriate HTTP status codes.

---

## REST Endpoints

### GET /api/health

Health check. Use this to verify the backend is running.

**Response 200:**
```json
{
  "status": "ok",
  "version": "0.1.0"
}
```

---

### GET /api/tools

Detect IaC tools installed on the user's machine. Checks PATH for terraform, tofu, and ansible.

**Response 200:**
```json
[
  {
    "name": "Terraform",
    "binary": "/usr/local/bin/terraform",
    "version": "Terraform v1.9.0 on linux_amd64",
    "available": true
  },
  {
    "name": "OpenTofu",
    "binary": "tofu",
    "version": "",
    "available": false
  },
  {
    "name": "Ansible",
    "binary": "/usr/bin/ansible",
    "version": "ansible [core 2.16.0]",
    "available": true
  }
]
```

---

### GET /api/projects

List all projects in the projects directory.

**Response 200:**
```json
[
  {
    "name": "my-infra",
    "path": "/home/user/iac-projects/my-infra",
    "tool": "terraform"
  }
]
```

---

### POST /api/projects

Create a new project. Scaffolds the directory with initial files.

**Request:**
```json
{
  "name": "my-infra",
  "tool": "terraform"
}
```

**Response 201:**
```json
{
  "name": "my-infra",
  "path": "/home/user/iac-projects/my-infra",
  "tool": "terraform"
}
```

**Side effects:**
- Creates `~/iac-projects/my-infra/`
- For terraform: creates main.tf, variables.tf, outputs.tf, .gitignore
- For ansible: creates site.yml, inventory.ini, ansible.cfg, roles/, .gitignore
- Starts file watcher on the directory

---

### GET /api/projects/{name}/resources?tool=terraform

Parse all IaC files in the project directory and return structured resources.

**Response 200:**
```json
[
  {
    "id": "aws_vpc.main",
    "type": "aws_vpc",
    "name": "main",
    "properties": {
      "cidr_block": "10.0.0.0/16",
      "enable_dns_support": "true"
    },
    "file": "/home/user/iac-projects/my-infra/main.tf",
    "line": 5
  },
  {
    "id": "aws_subnet.public",
    "type": "aws_subnet",
    "name": "public",
    "properties": {
      "cidr_block": "10.0.1.0/24",
      "vpc_id": "aws_vpc.main.id"
    },
    "file": "/home/user/iac-projects/my-infra/main.tf",
    "line": 11
  }
]
```

---

### POST /api/projects/{name}/sync?tool=terraform

Receive resources from the UI and write them to disk as IaC code.

**Request:**
```json
[
  {
    "id": "node_1",
    "type": "aws_vpc",
    "name": "main",
    "properties": {
      "cidr_block": "10.0.0.0/16",
      "enable_dns_support": true
    }
  }
]
```

**Response 200:**
```json
{
  "file": "/home/user/iac-projects/my-infra/main.tf",
  "code": "provider \"aws\" {\n  region = \"us-east-1\"\n}\n\nresource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n  enable_dns_support = true\n}\n"
}
```

**Side effects:**
- Pauses file watcher (prevents echo)
- Writes main.tf (or site.yml)
- Resumes file watcher

---

### POST /api/projects/{name}/run

Execute an IaC CLI command. Output is streamed via WebSocket, not in the response.

**Request:**
```json
{
  "tool": "terraform",
  "command": "plan"
}
```

Valid commands per tool:
- **terraform/opentofu**: `init`, `plan`, `apply`, `destroy`, `validate`, `fmt`
- **ansible**: `check`, `playbook`, `syntax`, `inventory`

**Response 202:**
```json
{
  "status": "running"
}
```

Output arrives via WebSocket (see below).

---

### POST /api/ai/chat

Send a natural language message to the AI assistant. Returns a response message and optionally a list of resources to add to the canvas.

**Request:**
```json
{
  "message": "Add a VPC with 2 public subnets",
  "tool": "terraform"
}
```

**Response 200:**
```json
{
  "message": "I've created a VPC with two public subnets in different availability zones.",
  "resources": [
    {
      "id": "ai_1711000000_0",
      "type": "aws_vpc",
      "name": "main_vpc",
      "properties": {
        "cidr_block": "10.0.0.0/16",
        "enable_dns_support": true,
        "enable_dns_hostnames": true
      }
    },
    {
      "id": "ai_1711000000_1",
      "type": "aws_subnet",
      "name": "public_1",
      "properties": {
        "cidr_block": "10.0.1.0/24",
        "availability_zone": "us-east-1a",
        "map_public_ip_on_launch": true
      }
    },
    {
      "id": "ai_1711000000_2",
      "type": "aws_subnet",
      "name": "public_2",
      "properties": {
        "cidr_block": "10.0.2.0/24",
        "availability_zone": "us-east-1b",
        "map_public_ip_on_launch": true
      }
    }
  ]
}
```

When Ollama is unavailable, `resources` may be `null` and `message` will be a helpful fallback.

---

### GET /api/catalog?tool=terraform

Return the full resource catalog for a tool. Used by the frontend to build the resource palette.

**Response 200:**
```json
{
  "tool": "terraform",
  "resources": [
    {
      "type": "aws_vpc",
      "label": "VPC",
      "icon": "🌐",
      "category": "Networking",
      "provider": "aws",
      "defaults": {"cidr_block": "10.0.0.0/16", "enable_dns_support": true},
      "fields": [
        {"name": "cidr_block", "type": "string", "required": true, "default": "10.0.0.0/16", "description": "CIDR block"},
        {"name": "enable_dns_support", "type": "bool", "default": true}
      ],
      "connects_to": [],
      "connects_via": {}
    }
  ]
}
```

---

### GET /api/templates?tool=terraform

Return pre-built infrastructure templates.

**Response 200:**
```json
[
  {
    "id": "vpc-public-private",
    "name": "VPC with Public & Private Subnets",
    "description": "Production-ready VPC with 2 AZs...",
    "icon": "🌐",
    "category": "Networking",
    "resources": [...],
    "connections": [{"from_index": 1, "to_index": 0, "field": "vpc_id"}]
  }
]
```

---

### POST /api/projects/{name}/validate?tool=terraform

Validate resources for common issues before plan/apply.

**Request:** Same as sync (array of resources)

**Response 200:**
```json
{
  "issues": [
    {
      "resource_id": "aws_s3_bucket.site",
      "field": "bucket",
      "severity": "error",
      "message": "S3 bucket names must be 3-63 chars, lowercase, no underscores",
      "suggestion": "my-site-bucket"
    }
  ],
  "valid": false
}
```

---

### GET /api/projects/{name}/git/status

Get Git repository status.

**Response 200:**
```json
{
  "branch": "main",
  "is_clean": false,
  "staged": [],
  "modified": ["main.tf"],
  "untracked": ["outputs.tf"],
  "commit_hash": "a1b2c3d",
  "commit_msg": "IaC Studio: add VPC",
  "commit_time": "2026-04-04 12:00:00 -0400"
}
```

---

### POST /api/projects/{name}/git/commit

Stage all changes and commit.

**Request:**
```json
{
  "message": "Add VPC with subnets"
}
```

**Response 200:**
```json
{
  "status": "committed",
  "hash": "e4f5g6h"
}
```

---

### GET /api/projects/{name}/git/log?limit=20

Get commit history.

**Response 200:**
```json
[
  {"hash": "e4f5g6h7...", "short": "e4f5g6h", "message": "Add VPC with subnets", "author": "IaC Studio", "time": "2026-04-04 12:00"}
]
```

---

### GET /api/catalog/dynamic?provider=aws

Fetch real provider schemas from Terraform CLI. Slower (runs terraform init) but returns ALL resources.

**Response 200:** Same format as `/api/catalog` but with potentially 1,500+ resources.

---

## WebSocket Protocol

### Connection

```
ws://localhost:3000/ws
```

Auto-reconnects on disconnect (client-side, 3 second delay).

### Server → Client Messages

**File changed on disk:**
```json
{
  "type": "file_changed",
  "project": "my-infra",
  "file": "/home/user/iac-projects/my-infra/main.tf",
  "tool": "terraform"
}
```

When received, the frontend should call `GET /api/projects/{name}/resources` to get the updated resource list.

**Terminal output from CLI command:**
```json
{
  "type": "terminal",
  "project": "my-infra",
  "output": "Terraform has been successfully initialized!\n\nYou may now begin working with Terraform.",
  "error": null
}
```

When `error` is non-null, display it in red in the terminal panel.

### Client → Server Messages

**State update (persist canvas positions):**
```json
{
  "type": "state_update",
  "project": "my-infra",
  "resources": [
    {
      "id": "aws_vpc.main",
      "x": 150,
      "y": 80,
      "connections": [{"targetId": "aws_subnet.public", "field": "vpc_id"}]
    }
  ]
}
```

---

## Data Models

### Resource (shared between frontend and backend)

```typescript
interface Resource {
  id: string;            // Unique ID: "aws_vpc.main" (from parser) or "node_123_456" (from UI)
  type: string;          // Resource type: "aws_vpc", "aws_instance", "apt", etc.
  name: string;          // Resource name: "main", "web_server", etc.
  properties: Record<string, any>;  // Key-value pairs of resource attributes
  file?: string;         // Source file path (from parser)
  line?: number;         // Source line number (from parser)

  // UI-only fields (not sent to backend for code generation)
  x?: number;            // Canvas X position
  y?: number;            // Canvas Y position
  icon?: string;         // Emoji icon
  label?: string;        // Human-readable label
  connections?: Connection[];  // Links to other resources
  connectsVia?: Record<string, string>;  // Connection rules from catalog
}

interface Connection {
  targetId: string;      // ID of the target resource
  field: string;         // Property name that references target (e.g., "vpc_id")
  label: string;         // Display label for the connection line
}
```

### Project State (.iac-studio.json)

```json
{
  "name": "my-infra",
  "tool": "terraform",
  "path": "/home/user/iac-projects/my-infra",
  "resources": [
    {
      "id": "aws_vpc.main",
      "type": "aws_vpc",
      "name": "main",
      "label": "VPC",
      "icon": "🌐",
      "properties": {"cidr_block": "10.0.0.0/16"},
      "x": 150.0,
      "y": 80.0,
      "connections": [
        {"target_id": "aws_subnet.public", "field": "vpc_id", "label": "vpc_id"}
      ]
    }
  ],
  "created_at": "2026-03-25T10:00:00Z",
  "updated_at": "2026-03-25T12:30:00Z"
}
```
