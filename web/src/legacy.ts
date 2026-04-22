import type { CatalogResource, FileEntry } from './api';

// Constants + helpers moved out of the App.tsx monolith. Nothing here
// is framework-specific — TOOLS / FALLBACK_RESOURCES used to be the
// top-of-file declarations; the rest are small pure functions that
// several panels will need once the extraction is done.

// UI metadata for the supported IaC tools. Resource catalogs come from
// the backend at runtime; this just decorates the tool selector.
export const TOOLS: Record<string, { name: string; icon: string; color: string; ext: string }> = {
  terraform: { name: 'Terraform', icon: 'TF', color: '#2FB5A8', ext: '.tf' },
  opentofu: { name: 'OpenTofu', icon: 'TO', color: '#F2B447', ext: '.tf' },
  ansible: { name: 'Ansible', icon: 'AN', color: '#D95757', ext: '.yml' },
};

// Fallback resource list for when the backend catalog is unreachable —
// keeps the palette usable offline so the user isn't stuck on an empty
// sidebar.
export const FALLBACK_RESOURCES: CatalogResource[] = [
  { type: 'aws_vpc', label: 'VPC', icon: '🌐', category: 'Networking' },
  { type: 'aws_subnet', label: 'Subnet', icon: '📡', category: 'Networking' },
  { type: 'aws_instance', label: 'EC2 Instance', icon: '🖥️', category: 'Compute' },
  { type: 'aws_s3_bucket', label: 'S3 Bucket', icon: '🪣', category: 'Storage' },
  { type: 'aws_security_group', label: 'Security Group', icon: '🛡️', category: 'Security' },
];

// Edge describes a connection between two resource nodes on the
// freeform canvas. The persisted shape in api.ts is a subset — these
// extra fields are transient UI state (labels, types) that don't
// round-trip through the backend state file.
export interface Edge {
  id: string;
  from: string;     // source node id
  to: string;       // target node id
  fromType: string; // source resource type
  toType: string;   // target resource type
  field: string;    // the field that creates this connection (e.g., "vpc_id")
  label: string;    // human-readable label for the connection
}

// uid generates a monotonically-increasing, collision-resistant node id.
// Counter + timestamp so fast successive calls never clash even when
// Date.now() returns the same ms.
let _id = 0;
export const uid = () => `node_${++_id}_${Date.now()}`;

export const edgeId = (from: string, to: string, field: string) => `${from}->${to}:${field}`;

export function fileGlyph(entry: FileEntry): string {
  if (entry.is_dir) return 'DIR';
  if (entry.ext === '.tf') return 'TF';
  if (entry.ext === '.yml' || entry.ext === '.yaml') return 'YML';
  return 'FILE';
}

// generateLocalCode mirrors the Go backend's HCL/YAML emitter so the
// preview stays in sync while the user is adding resources. The backend
// is authoritative on save; this is purely a UX affordance (no
// round-trip wait).
export function generateLocalCode(tool: string, nodes: any[], edges: Edge[]): string {
  if (tool === 'ansible') {
    let c = '---\n- name: IaC Studio Playbook\n  hosts: all\n  become: true\n  tasks:\n';
    nodes.forEach((n) => {
      c += `    - name: ${n.name || n.type}\n      ${n.type}:\n`;
      Object.entries(n.properties).forEach(([k, v]) => {
        if (typeof v === 'boolean') c += `        ${k}: ${v ? 'yes' : 'no'}\n`;
        else if (typeof v === 'number') c += `        ${k}: ${v}\n`;
        else c += `        ${k}: "${v}"\n`;
      });
      c += '\n';
    });
    return c;
  }

  const hasGCP = nodes.some((n) => n.type.startsWith('google_'));
  const hasAzure = nodes.some((n) => n.type.startsWith('azurerm_'));
  const hasAWS = nodes.some((n) => n.type.startsWith('aws_'));

  let c = '';
  if (hasAWS) c += 'provider "aws" {\n  region = "us-east-1"\n}\n\n';
  if (hasGCP) c += 'provider "google" {\n  project = "my-project"\n  region  = "us-central1"\n}\n\n';
  if (hasAzure) c += 'provider "azurerm" {\n  features {}\n}\n\n';

  const edgesByFrom = new Map<string, Edge[]>();
  edges.forEach((e) => {
    const list = edgesByFrom.get(e.from) || [];
    list.push(e);
    edgesByFrom.set(e.from, list);
  });

  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  nodes.forEach((n) => {
    const name =
      n.name || n.type.replace(/^(aws_|google_|azurerm_|google_compute_|google_container_)/, '');
    c += `resource "${n.type}" "${name}" {\n`;

    // Emit reference fields first (e.g. vpc_id = aws_vpc.main.id) so
    // they come before the plain-value fields they override.
    const nodeEdges = edgesByFrom.get(n.id) || [];
    const emittedFields = new Set<string>();
    nodeEdges.forEach((edge) => {
      const target = nodeById.get(edge.to);
      if (target && edge.field !== 'depends_on') {
        const targetName =
          target.name ||
          target.type.replace(/^(aws_|google_|azurerm_|google_compute_|google_container_)/, '');
        c += `  ${edge.field} = ${target.type}.${targetName}.id\n`;
        emittedFields.add(edge.field);
      }
    });

    Object.entries(n.properties).forEach(([k, v]) => {
      if (emittedFields.has(k)) return;
      if (typeof v === 'boolean') c += `  ${k} = ${v}\n`;
      else if (typeof v === 'number') c += `  ${k} = ${v}\n`;
      else if (Array.isArray(v)) c += `  ${k} = ${JSON.stringify(v)}\n`;
      else c += `  ${k} = "${v}"\n`;
    });

    c += '}\n\n';
  });
  return c;
}
