// API client for IaC Studio backend

const BASE = '';

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

// Checks res.ok and throws with the backend's error message instead of
// letting callers hit an opaque JSON parse failure on text/plain errors.
async function check(res: Response): Promise<Response> {
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(text || `HTTP ${res.status}`);
  }
  return res;
}

export const api = {
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
    return (await check(res)).json();
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
  async getResources(projectName: string, tool: string): Promise<Resource[]> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/resources?tool=${tool}`);
    return (await check(res)).json();
  },

  // Sync resources and connections from UI to disk
  async syncToDisk(projectName: string, tool: string, resources: Resource[], edges?: { from: string; to: string; field: string }[]): Promise<{ file: string; code: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/sync?tool=${tool}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ resources, edges: edges || [] }),
    });
    return (await check(res)).json();
  },

  // Run IaC command. For apply/destroy, pass approved:true after plan review.
  async runCommand(projectName: string, tool: string, command: string, approved = false): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool, command, approved }),
    });
    return (await check(res)).json();
  },

  // AI chat with conversation context
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
    return (await check(res)).json();
  },
};
