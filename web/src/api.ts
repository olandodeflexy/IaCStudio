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

export const api = {
  // Health check
  async health(): Promise<{ status: string; version: string }> {
    const res = await fetch(`${BASE}/api/health`);
    return res.json();
  },

  // Detect installed IaC tools
  async detectTools(): Promise<ToolInfo[]> {
    const res = await fetch(`${BASE}/api/tools`);
    return res.json();
  },

  // List projects
  async listProjects(): Promise<Project[]> {
    const res = await fetch(`${BASE}/api/projects`);
    return res.json();
  },

  // Create a new project
  async createProject(name: string, tool: string): Promise<Project> {
    const res = await fetch(`${BASE}/api/projects`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, tool }),
    });
    return res.json();
  },

  // Parse project files into resources
  async getResources(projectName: string, tool: string): Promise<Resource[]> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/resources?tool=${tool}`);
    return res.json();
  },

  // Sync resources from UI to disk
  async syncToDisk(projectName: string, tool: string, resources: Resource[]): Promise<{ file: string; code: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/sync?tool=${tool}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(resources),
    });
    return res.json();
  },

  // Run IaC command
  async runCommand(projectName: string, tool: string, command: string): Promise<{ status: string }> {
    const res = await fetch(`${BASE}/api/projects/${projectName}/run`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool, command }),
    });
    return res.json();
  },

  // AI chat
  async chat(message: string, tool: string): Promise<{ message: string; resources: Resource[] | null }> {
    const res = await fetch(`${BASE}/api/ai/chat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, tool }),
    });
    return res.json();
  },
};
