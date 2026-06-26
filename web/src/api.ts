// API client for IaC Studio backend

const BASE = '';

export interface ApiErrorPayload {
  error?: string;
  detail?: string;
  findings?: PolicyFinding[];
  classification?: PlanClassification;
}

export class ApiError extends Error {
  status: number;
  payload?: ApiErrorPayload;

  constructor(status: number, fallback: string, payload?: ApiErrorPayload) {
    super(payload?.detail || payload?.error || fallback || `HTTP ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.payload = payload;
    Object.setPrototypeOf(this, ApiError.prototype);
  }
}

export interface ToolInfo {
  name: string;
  binary: string;
  version: string;
  available: boolean;
}

export interface Resource {
  id: string;
  type: string;
  name: string;
  properties: Record<string, any>;
  file?: string;
  line?: number;
  // UI-only fields
  x?: number;
  y?: number;
  icon?: string;
  label?: string;
}

export interface Project {
  name: string;
  path: string;
  tool?: string;
}

export interface Suggestion {
  type: string;
  label: string;
  reason: string;
  priority: number;
}

export function normalizeSuggestions(value: unknown): Suggestion[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is Suggestion => {
    if (!item || typeof item !== 'object') return false;
    const candidate = item as Partial<Suggestion>;
    return typeof candidate.type === 'string' &&
      typeof candidate.label === 'string' &&
      typeof candidate.reason === 'string' &&
      typeof candidate.priority === 'number';
  });
}

export interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  ext?: string;
  children?: number;
}

export interface ImportResult {
  tool: string;
  provider: string;
  files: { path: string; name: string; type: string; size: number }[];
  resources: Resource[];
  edges: { from_id: string; to_id: string; field: string }[];
  summary: string;
  warnings?: string[];
}

export interface CatalogResource {
  type: string;
  label: string;
  icon: string;
  category: string;
  provider?: string;
  defaults?: Record<string, any>;
  fields?: { name: string; type: string; required?: boolean; default?: any; description?: string; options?: string[] }[];
  connects_to?: string[];
  connects_via?: Record<string, string>;
}

export type CloudProvider = 'aws' | 'azure' | 'gcp';

export type CloudSecretStore = 'local_encrypted';

export type CloudAuthMethod =
  | 'aws_profile'
  | 'aws_sso'
  | 'aws_static'
  | 'azure_cli'
  | 'azure_service_principal'
  | 'gcp_gcloud'
  | 'gcp_service_account';

export interface CloudConnection {
  id: string;
  name: string;
  provider: CloudProvider;
  auth_method: CloudAuthMethod;
  region?: string;
  metadata?: Record<string, string>;
  secret_fields?: string[];
  secret_store?: CloudSecretStore;
  created_at?: string;
  updated_at?: string;
}

export interface CloudConnectionInput {
  id?: string;
  name: string;
  provider: CloudProvider;
  auth_method: CloudAuthMethod;
  region?: string;
  metadata?: Record<string, string>;
  secrets?: Record<string, string>;
}

export interface CloudConnectionTestResult {
  ok: boolean;
  summary: string;
  connection: CloudConnection;
  checks: { name: string; status: 'pass' | 'warn' | 'error'; message: string }[];
}

export interface MCPAirlockCheck {
  name: string;
  status: 'pass' | 'warn' | 'error';
  message: string;
}

export interface MCPAirlockServerDefinition {
  id: string;
  name: string;
  vendor: string;
  description: string;
  source_url: string;
  docs_url?: string;
  install_hint?: string;
  transport: string;
  command?: string;
  args?: string[];
  health_check_args?: string[];
  trusted: boolean;
  read_only_default: boolean;
  credential_mode: string;
  capabilities?: string[];
}

export interface MCPAirlockServerStatus {
  server: MCPAirlockServerDefinition;
  ready: boolean;
  running: boolean;
  configured: boolean;
  command_available: boolean;
  state: string;
  summary: string;
  checks: MCPAirlockCheck[];
  checked_at?: string;
  started_at?: string;
  last_exit_at?: string;
  last_exit_reason?: string;
}

export type MCPAirlockToolRisk =
  | 'read_only'
  | 'generate_code'
  | 'modify_workspace'
  | 'cloud_mutation'
  | 'secret_sensitive'
  | 'destructive'
  | 'unknown';

export interface MCPAirlockToolDecision {
  status: 'allowed' | 'blocked' | 'approval_required';
  allowed: boolean;
  approval_required: boolean;
  risk: MCPAirlockToolRisk;
  reason: string;
  allowlisted: boolean;
  untrusted_output: boolean;
}

export interface MCPAirlockToolEntry {
  server_id: string;
  name: string;
  description?: string;
  input_schema_hash: string;
  last_seen_at: string;
  schema_state: 'new' | 'known' | 'changed' | 'unknown';
  risk: MCPAirlockToolRisk;
  decision: MCPAirlockToolDecision;
}

export interface MCPAirlockToolInventory {
  server_id: string;
  discovered_at?: string;
  tools: MCPAirlockToolEntry[];
  checks: MCPAirlockCheck[];
}

export type PlanRiskLevel = 'safe' | 'risky' | 'destructive' | 'unknown';

export interface PlanFieldChange {
  path: string;
  old_value?: unknown;
  new_value?: unknown;
  sensitive?: boolean;
  computed?: boolean;
}

export interface PlanClassifiedChange {
  address: string;
  type?: string;
  name?: string;
  provider?: string;
  action: string;
  risk: PlanRiskLevel;
  categories: string[];
  reason: string;
  reviewer_focus: string[];
  field_changes?: PlanFieldChange[];
}

export interface PlanClassificationSummary {
  safe: number;
  risky: number;
  destructive: number;
  unknown: number;
  total: number;
  requires_acknowledgment: boolean;
  text: string;
}

export interface PlanClassification {
  summary: PlanClassificationSummary;
  changes: PlanClassifiedChange[];
  markdown?: string;
}

export interface PlanReference {
  hash: string;
  tool: string;
  env?: string;
  connection_id?: string;
  plan_command: string;
  created_at: string;
  expires_at: string;
}

export interface DriftField {
  path: string;
  code_value?: unknown;
  live_value?: unknown;
}

export interface DriftResource {
  address: string;
  type: string;
  name: string;
  changes?: DriftField[];
  status: 'drifted' | 'missing' | 'unmanaged';
  classification?: string;
  recommended_action?: string;
  reason?: string;
}

export interface DriftFinding {
  address: string;
  type: string;
  name: string;
  status: 'drifted' | 'missing' | 'unmanaged';
  path?: string;
  expected_value?: unknown;
  current_value?: unknown;
  classification: string;
  recommended_action: string;
  reason: string;
  suppressed?: boolean;
  suppression_reason?: string;
}

export interface DriftReport {
  has_state: boolean;
  state_path: string;
  connection_id?: string;
  connection_name?: string;
  connection_provider?: string;
  drifted: DriftResource[];
  findings: DriftFinding[];
  suppressed_findings?: DriftFinding[];
  suppressed?: number;
  missing: string[];
  unmanaged: string[];
  in_sync: number;
  total: number;
  classifications?: Record<string, number>;
  summary: string;
}

export type DriftRemediationMode = 'codify' | 'revert';

export interface DriftRequestScope {
  tool?: string;
  env?: string;
  connectionId?: string | null;
}

export interface DriftRemediationFileChange {
  path?: string;
  line?: number;
  action: string;
  address: string;
  field?: string;
  summary: string;
  before?: string;
  after?: string;
}

export interface DriftRemediationProposal {
  mode: DriftRemediationMode;
  title: string;
  branch: string;
  commit_message: string;
  body: string;
  findings: DriftFinding[];
  file_changes: DriftRemediationFileChange[];
  warnings?: string[];
}

export interface DriftRemediationArtifactFile {
  path: string;
  kind: string;
  summary: string;
  size: number;
}

export interface DriftRemediationArtifactSet {
  id: string;
  root: string;
  created_at: string;
  proposal: DriftRemediationProposal;
  files: DriftRemediationArtifactFile[];
}

export interface ReviewCommand {
  label: string;
  args: string[];
  display: string;
}

export interface PullRequestHandoff {
  title: string;
  branch: string;
  base_branch: string;
  commit: string;
  commit_message: string;
  body_path: string;
  files: string[];
  commands: ReviewCommand[];
  warnings?: string[];
}

export interface DriftRemediationPullRequestResponse {
  artifacts: DriftRemediationArtifactSet;
  pull_request: PullRequestHandoff;
}

export interface StateSnapshot {
  id: string;
  project: string;
  tool: string;
  env?: string;
  command: string;
  work_dir: string;
  state_path?: string;
  state_sha256?: string;
  state_size?: number;
  plan_path?: string;
  plan_sha256?: string;
  plan_size?: number;
  created_at: string;
  status: string;
  notes?: string[];
}

export interface RollbackProposal {
  id: string;
  title: string;
  branch: string;
  commit_message: string;
  body: string;
  tool: string;
  env?: string;
  work_dir: string;
  target_snapshot: StateSnapshot;
  current_snapshot?: StateSnapshot;
  classification: PlanClassification;
  warnings?: string[];
}

export interface RollbackArtifactFile {
  path: string;
  kind: string;
  summary: string;
  size: number;
}

export interface RollbackArtifactSet {
  id: string;
  root: string;
  created_at: string;
  proposal: RollbackProposal;
  files: RollbackArtifactFile[];
}

export interface RollbackPullRequestResponse {
  artifacts: RollbackArtifactSet;
  pull_request: PullRequestHandoff;
}

// Checks res.ok and throws with the backend's error message instead of
// letting callers hit an opaque JSON parse failure on text/plain errors.
async function check(res: Response): Promise<Response> {
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    let payload: ApiErrorPayload | undefined;
    if (text.trim().startsWith('{')) {
      try {
        payload = JSON.parse(text) as ApiErrorPayload;
      } catch {
        payload = undefined;
      }
    }
    throw new ApiError(res.status, text || res.statusText, payload);
  }
  return res;
}

export const api = {
  async listMCPAirlockServers(): Promise<MCPAirlockServerStatus[]> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers`);
    return (await check(res)).json();
  },

  async checkMCPAirlockServer(id: string): Promise<MCPAirlockServerStatus> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/health`, { method: 'POST' });
    return (await check(res)).json();
  },

  async startMCPAirlockServer(id: string): Promise<MCPAirlockServerStatus> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/start`, { method: 'POST' });
    return (await check(res)).json();
  },

  async stopMCPAirlockServer(id: string): Promise<MCPAirlockServerStatus> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/stop`, { method: 'POST' });
    return (await check(res)).json();
  },

  async getMCPAirlockTools(id: string): Promise<MCPAirlockToolInventory> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/tools`);
    return (await check(res)).json();
  },

  async discoverMCPAirlockTools(id: string): Promise<MCPAirlockToolInventory> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/tools/discover`, { method: 'POST' });
    return (await check(res)).json();
  },

  async evaluateMCPAirlockTool(id: string, toolName: string, project?: string): Promise<MCPAirlockToolEntry> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/tools/evaluate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool_name: toolName, project }),
    });
    return (await check(res)).json();
  },

  async setMCPAirlockToolAllowlist(id: string, toolName: string, allowed: boolean, project?: string): Promise<MCPAirlockToolEntry> {
    const res = await fetch(`${BASE}/api/mcp-airlock/servers/${encodeURIComponent(id)}/tools/allowlist`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool_name: toolName, project, allowed }),
    });
    return (await check(res)).json();
  },

  async listCloudConnections(): Promise<CloudConnection[]> {
    const res = await fetch(`${BASE}/api/cloud/connections`);
    return (await check(res)).json();
  },

  async createCloudConnection(input: CloudConnectionInput): Promise<CloudConnection> {
    const res = await fetch(`${BASE}/api/cloud/connections`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
    return (await check(res)).json();
  },

  async updateCloudConnection(id: string, input: CloudConnectionInput): Promise<CloudConnection> {
    const res = await fetch(`${BASE}/api/cloud/connections/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
    return (await check(res)).json();
  },

  async deleteCloudConnection(id: string): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/cloud/connections/${encodeURIComponent(id)}`, { method: 'DELETE' });
    return (await check(res)).json();
  },

  async testCloudConnection(id: string): Promise<CloudConnectionTestResult> {
    const res = await fetch(`${BASE}/api/cloud/connections/${encodeURIComponent(id)}/test`, { method: 'POST' });
    return (await check(res)).json();
  },

  // Fetch resource catalog for a tool (optionally filtered by provider)
  async getCatalog(tool: string, provider?: string): Promise<{ tool: string; resources: CatalogResource[] }> {
    const params = new URLSearchParams({ tool });
    if (provider) params.set('provider', provider);
    const res = await fetch(`${BASE}/api/catalog?${params}`);
    return (await check(res)).json();
  },

  // Browse local filesystem
  async browse(path?: string): Promise<{ path: string; parent: string; entries: FileEntry[] }> {
    const params = path ? `?path=${encodeURIComponent(path)}` : '';
    const res = await fetch(`${BASE}/api/browse${params}`);
    return (await check(res)).json();
  },

  // Import an existing project
  async importProject(path: string): Promise<ImportResult> {
    const res = await fetch(`${BASE}/api/import`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    });
    return (await check(res)).json();
  },

  // AI topology builder (async — result comes via WebSocket)
  async generateTopology(description: string, tool: string, provider: string): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/ai/topology`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ description, tool, provider }),
    });
    return (await check(res)).json();
  },

  // AI vision topology builder (sync multipart upload).
  async generateTopologyFromImages(req: {
    description?: string;
    tool: string;
    provider: string;
    images: File[];
  }): Promise<{ message: string; resources: Resource[] }> {
    const form = new FormData();
    form.set('tool', req.tool);
    form.set('provider', req.provider);
    if (req.description?.trim()) form.set('description', req.description.trim());
    req.images.forEach(file => form.append('image', file, file.name));

    const res = await fetch(`${BASE}/api/ai/topology/image`, {
      method: 'POST',
      body: form,
    });
    return (await check(res)).json();
  },

  // AI provider settings
  async getAISettings(): Promise<{ type: string; endpoint: string; model: string; api_key: string }> {
    const res = await fetch(`${BASE}/api/ai/settings`);
    return (await check(res)).json();
  },

  async updateAISettings(config: { type: string; endpoint: string; model: string; api_key: string }): Promise<any> {
    const res = await fetch(`${BASE}/api/ai/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config),
    });
    return (await check(res)).json();
  },

  // Delete a project
  async deleteProject(projectName: string): Promise<any> {
    const res = await fetch(`${BASE}/api/projects/${projectName}`, { method: 'DELETE' });
    return (await check(res)).json();
  },

  // Open project directory in OS file manager
  async revealProject(projectName: string): Promise<any> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/reveal`, { method: 'POST' });
    return (await check(res)).json();
  },

  // Load project state (canvas, edges, tool)
  async loadState(projectName: string): Promise<any> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/state`);
    return (await check(res)).json();
  },

  // Save project state
  async saveState(projectName: string, state: any): Promise<any> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/state`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(state),
    });
    return (await check(res)).json();
  },

  // List all projects with their state
  async listProjectStates(): Promise<any[]> {
    const res = await fetch(`${BASE}/api/projects/states`);
    const states = await (await check(res)).json();
    return Array.isArray(states) ? states : [];
  },

  // Health check
  async health(): Promise<{ status: string; version: string }> {
    const res = await fetch(`${BASE}/api/health`);
    return (await check(res)).json();
  },

  // Detect installed IaC tools
  async detectTools(): Promise<ToolInfo[]> {
    const res = await fetch(`${BASE}/api/tools`);
    return (await check(res)).json();
  },

  // List projects
  async listProjects(): Promise<Project[]> {
    const res = await fetch(`${BASE}/api/projects`);
    return (await check(res)).json();
  },

  // Create a new project
  async createProject(name: string, tool: string): Promise<Project> {
    const res = await fetch(`${BASE}/api/projects`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, tool }),
    });
    return (await check(res)).json();
  },

  // Parse project files into resources
  async getResources(projectName: string, tool: string, env?: string): Promise<Resource[]> {
    const params = new URLSearchParams({ tool });
    if (env) params.set('env', env);
    const res = await fetch(`${BASE}/api/projects/${projectName}/resources?${params}`);
    return (await check(res)).json();
  },

  // Sync resources and connections from UI to disk
  async syncToDisk(projectName: string, tool: string, resources: Resource[], edges?: { from: string; to: string; field: string }[], env?: string): Promise<{ file: string; code: string }> {
    const params = new URLSearchParams({ tool });
    if (env) params.set('env', env);
    const res = await fetch(`${BASE}/api/projects/${projectName}/sync?${params}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ resources, edges: edges || [] }),
    });
    return (await check(res)).json();
  },

  // Save an explicit editor buffer through the same sync endpoint. This is
  // intentionally separate from resource sync so Monaco edits only write when
  // the user saves, not on every canvas change.
  async syncCodeToDisk(projectName: string, tool: string, code: string, file?: string, env?: string): Promise<{ file: string; code: string }> {
    const params = new URLSearchParams({ tool });
    if (env) params.set('env', env);
    const res = await fetch(`${BASE}/api/projects/${projectName}/sync?${params}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code, file }),
    });
    return (await check(res)).json();
  },

  // Run IaC command. For apply/destroy, pass approved:true after plan
  // review. For pulumi layered-v1 projects, pass env so the runner
  // executes inside environments/<env> and threads --stack <env> to
  // pulumi (otherwise the workspace-selected stack is targeted, which
  // can be wrong). Pass acknowledged:true only when the user has reviewed
  // blocking policy findings and still wants to override the gate.
  async runCommand(
    projectName: string,
    tool: string,
    command: string,
    opts: { approved?: boolean; env?: string; acknowledged?: boolean; riskAcknowledged?: boolean; connectionId?: string | null; planHash?: string | null } = {},
  ): Promise<{ status: string; connection?: CloudConnection }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool,
        command,
        approved: opts.approved ?? false,
        env: opts.env,
        acknowledged: opts.acknowledged ?? false,
        risk_acknowledged: opts.riskAcknowledged ?? false,
        connection_id: opts.connectionId || undefined,
        plan_hash: opts.planHash || undefined,
      }),
    });
    return (await check(res)).json();
  },

  async classifyPlan(projectName: string, req: { tool?: string; env?: string; plan_json?: unknown; plan_text?: string } = {}): Promise<PlanClassification> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/plan/classify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  async runDrift(projectName: string, req: DriftRequestScope = {}): Promise<DriftReport> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/drift`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: req.tool,
        env: req.env,
        connection_id: req.connectionId || undefined,
      }),
    });
    return (await check(res)).json();
  },

  async createDriftRemediation(projectName: string, req: DriftRequestScope & { mode: DriftRemediationMode }): Promise<DriftRemediationProposal> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/drift/remediation`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: req.tool,
        env: req.env,
        connection_id: req.connectionId || undefined,
        mode: req.mode,
      }),
    });
    return (await check(res)).json();
  },

  async createDriftRemediationArtifacts(projectName: string, req: DriftRequestScope & { mode: DriftRemediationMode; proposal?: DriftRemediationProposal }): Promise<DriftRemediationArtifactSet> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/drift/remediation/artifacts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: req.tool,
        env: req.env,
        connection_id: req.connectionId || undefined,
        mode: req.mode,
        proposal: req.proposal,
      }),
    });
    return (await check(res)).json();
  },

  async createDriftRemediationPullRequest(projectName: string, req: DriftRequestScope & { mode: DriftRemediationMode; proposal?: DriftRemediationProposal }): Promise<DriftRemediationPullRequestResponse> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/drift/remediation/pr`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: req.tool,
        env: req.env,
        connection_id: req.connectionId || undefined,
        mode: req.mode,
        proposal: req.proposal,
      }),
    });
    return (await check(res)).json();
  },

  async listStateSnapshots(projectName: string, env?: string): Promise<StateSnapshot[]> {
    const params = new URLSearchParams();
    if (env) params.set('env', env);
    const query = params.toString();
    const res = await fetch(`${BASE}/api/projects/${projectName}/snapshots${query ? `?${query}` : ''}`);
    return (await check(res)).json();
  },

  async createRollbackProposal(projectName: string, snapshotId: string, req: { env?: string } = {}): Promise<RollbackProposal> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/snapshots/${snapshotId}/rollback`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  async createRollbackArtifacts(projectName: string, snapshotId: string, req: { env?: string; proposal?: RollbackProposal } = {}): Promise<RollbackArtifactSet> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/snapshots/${snapshotId}/rollback/artifacts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  async createRollbackPullRequest(projectName: string, snapshotId: string, req: { env?: string; proposal?: RollbackProposal } = {}): Promise<RollbackPullRequestResponse> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/snapshots/${snapshotId}/rollback/pr`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  // Kill a running command. Pass env to match the workdir the command
  // was launched in (layered-v1 / pulumi); otherwise the kill targets
  // the project root and won't find the active execution.
  async killCommand(projectName: string, env?: string): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/kill`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: env ? JSON.stringify({ env }) : '',
    });
    return (await check(res)).json();
  },

  // AI chat with conversation context (non-streaming).
  // Kept for callers that don't need progressive rendering.
  async chat(req: {
    message: string;
    tool: string;
    provider: string;
    history: { role: string; content: string }[];
    canvas: { type: string; name: string }[];
  }): Promise<{ message: string; resources: Resource[] | null; suggestions?: Suggestion[] }> {
    const res = await fetch(`${BASE}/api/ai/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  // AI chat with streaming — tokens arrive via onDelta as the model generates.
  // The promise resolves with the final parsed {message, resources, suggestions}
  // once the "complete" SSE event arrives. Throws on network error or abort.
  //
  // We don't use EventSource because EventSource only supports GET and can't
  // send a JSON body. The manual ReadableStream reader below handles the SSE
  // wire format directly.
  async chatStream(
    req: {
      message: string;
      tool: string;
      provider: string;
      history: { role: string; content: string }[];
      canvas: { type: string; name: string }[];
    },
    onDelta: (text: string) => void,
    signal?: AbortSignal,
  ): Promise<{ message: string; resources: Resource[] | null; suggestions?: Suggestion[] | null }> {
    const res = await fetch(`${BASE}/api/ai/chat/stream`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
      body: JSON.stringify(req),
      signal,
    });
    if (!res.ok) {
      const errorText = (await res.text()).trim();
      throw new Error(
        errorText
          ? `chat stream failed: ${res.status} ${res.statusText} - ${errorText}`
          : `chat stream failed: ${res.status} ${res.statusText}`,
      );
    }
    if (!res.body) {
      throw new Error(`chat stream failed: ${res.status} ${res.statusText}`);
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let complete: { message: string; resources: Resource[] | null; suggestions?: Suggestion[] | null } | null = null;
    let streamError: Error | null = null;

    // SSE events are separated by "\n\n"; each event has "event: X\ndata: Y\n"
    // lines. We accumulate into buffer and split on blank lines.
    const processSseEvent = (raw: string) => {
      let eventType = 'message';
      const dataLines: string[] = [];

      for (const line of raw.split('\n')) {
        if (line.startsWith('event:')) {
          let value = line.slice(6);
          if (value.startsWith(' ')) value = value.slice(1);
          eventType = value.trim() || 'message';
        } else if (line.startsWith('data:')) {
          let value = line.slice(5);
          if (value.startsWith(' ')) value = value.slice(1);
          dataLines.push(value);
        }
      }

      if (dataLines.length === 0) return;
      const data = dataLines.join('\n');

      let payload: any;
      try {
        payload = JSON.parse(data);
      } catch (e) {
        // Malformed event — skip rather than aborting the whole stream.
        console.warn('malformed SSE event', e, data);
        return;
      }

      if (eventType === 'delta' && typeof payload.text === 'string') {
        onDelta(payload.text);
      } else if (eventType === 'complete') {
        complete = payload;
      } else if (eventType === 'error' || eventType === 'provider_error') {
        // The server may emit an informational error event and then still send
        // a final complete event with a fallback result. Record the error, but
        // keep reading so we do not discard a subsequent complete payload.
        streamError = new Error(payload.error || 'chat stream error');
      }
    };

    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        buffer += decoder.decode();
        buffer = buffer.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      buffer = buffer.replace(/\r\n/g, '\n').replace(/\r/g, '\n');

      let sep = buffer.indexOf('\n\n');
      while (sep !== -1) {
        const raw = buffer.slice(0, sep);
        buffer = buffer.slice(sep + 2);
        processSseEvent(raw);
        sep = buffer.indexOf('\n\n');
      }
    }

    if (buffer.trim()) {
      processSseEvent(buffer);
    }
    if (complete) {
      return complete;
    }
    if (streamError) {
      throw streamError;
    }
    throw new Error('chat stream ended without a complete event');
  },

  // Analyze plan/apply output and get AI fix suggestions
  async analyzePlan(req: {
    tool: string;
    provider: string;
    command: string;
    output: string;
    exit_code: number;
    canvas: { type: string; name: string }[];
  }): Promise<{
    message: string;
    fixes: { resource_type: string; resource_name: string; field: string; old_value: string; new_value: string; reason: string }[];
    new_resources: Resource[];
  }> {
    const res = await fetch(`${BASE}/api/ai/fix`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  // Smart resource suggestions
  async suggest(tool: string, provider: string, canvas: { type: string; name: string }[]): Promise<Suggestion[]> {
    const res = await fetch(`${BASE}/api/ai/suggest`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool, provider, canvas }),
    });
    return normalizeSuggestions(await (await check(res)).json());
  },

  // Policy engines — the UI uses listPolicyEngines to render engine
  // toggles, then runPolicy to fire the actual evaluation.
  async listPolicyEngines(): Promise<{ name: string; available: boolean }[]> {
    const res = await fetch(`${BASE}/api/policy/engines`);
    return (await check(res)).json();
  },
  async runPolicy(
    projectName: string,
    req: { engines?: string[]; tool?: string; plan_json?: string; env?: string } = {},
  ): Promise<PolicyRunResponse> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/policy/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  // Security scanners. Parallel shape to policy — same request/response
  // idiom so the Scan panel and the Policy Studio share a render
  // pipeline.
  async listSecurityScanners(): Promise<{ name: string; available: boolean }[]> {
    const res = await fetch(`${BASE}/api/security/scanners`);
    return (await check(res)).json();
  },
  async runScanners(
    projectName: string,
    req: { scanners?: string[]; tool?: string } = {},
  ): Promise<ScanRunResponse> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/security/scanners/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    return (await check(res)).json();
  },

  // Terraform module registry search. Backend proxies to the public
  // registry so the browser doesn't need any CORS exception.
  async searchModules(q: string, limit = 20): Promise<ModuleSearchResult> {
    const res = await fetch(
      `${BASE}/api/registry/modules/search?q=${encodeURIComponent(q)}&limit=${limit}`,
    );
    return (await check(res)).json();
  },
};

// ─── Policy / Scan / Registry types ───────────────────────────────

export type Severity = 'error' | 'warning' | 'info';

export interface PolicyFinding {
  engine: string;
  policy_id: string;
  policy_name: string;
  severity: Severity;
  category?: string;
  resource?: string;
  message: string;
  suggestion?: string;
  policy_file?: string;
}

export interface EngineResult {
  engine: string;
  available: boolean;
  findings?: PolicyFinding[];
  error?: string;
}

export interface PolicyRunResponse {
  results: EngineResult[];
  findings: PolicyFinding[];
  blocking: boolean;
}

// Scan findings share the policy finding shape today — the backend
// emits a superset-compatible Finding from both surfaces. If that
// drifts we can split the types; for now one alias keeps rendering
// code shared.
export type ScanFinding = PolicyFinding;
export type ScanResult = EngineResult;
export type ScanRunResponse = PolicyRunResponse;

export interface RegistryModule {
  id: string;
  namespace: string;
  name: string;
  provider: string;
  version: string;
  description: string;
  // source is optional — the public Terraform registry always includes
  // it, but self-hosted / private registries occasionally omit it, and
  // the panel already falls back to hiding the link. Keeping the type
  // honest so callers don't assume it's always present.
  source?: string;
  published_at: string;
  downloads: number;
  verified: boolean;
}

export interface ModuleSearchResult {
  modules: RegistryModule[];
}
