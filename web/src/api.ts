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

  // Sync resources from UI to disk
  async syncToDisk(projectName: string, tool: string, resources: Resource[]): Promise<{ file: string; code: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/sync?tool=${tool}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(resources),
    });
    return (await check(res)).json();
  },

  // Run IaC command
  async runCommand(projectName: string, tool: string, command: string): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool, command }),
    });
    return (await check(res)).json();
  },

  // AI chat
  async chat(message: string, tool: string): Promise<{ message: string; resources: Resource[] | null }> {
    const res = await fetch(`${BASE}/api/ai/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, tool }),
    });
    return (await check(res)).json();
  },
};
