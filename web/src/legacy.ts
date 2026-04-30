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
  pulumi: { name: 'Pulumi', icon: 'PU', color: '#8A63D2', ext: '.ts' },
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
  if (entry.ext === '.ts') return 'TS';
  if (entry.ext === '.yml' || entry.ext === '.yaml') return 'YML';
  return 'FILE';
}

const PULUMI_TYPE_OVERRIDES: Record<string, string> = {
  aws_vpc: 'aws.ec2.Vpc',
  aws_subnet: 'aws.ec2.Subnet',
  aws_internet_gateway: 'aws.ec2.InternetGateway',
  aws_nat_gateway: 'aws.ec2.NatGateway',
  aws_route_table: 'aws.ec2.RouteTable',
  aws_security_group: 'aws.ec2.SecurityGroup',
  aws_instance: 'aws.ec2.Instance',
  aws_lambda_function: 'aws.lambda.Function',
  aws_ecs_cluster: 'aws.ecs.Cluster',
  aws_eks_cluster: 'aws.eks.Cluster',
  aws_s3_bucket: 'aws.s3.Bucket',
  aws_db_instance: 'aws.rds.Instance',
  google_compute_network: 'gcp.compute.Network',
  google_compute_subnetwork: 'gcp.compute.Subnetwork',
  google_compute_instance: 'gcp.compute.Instance',
  google_container_cluster: 'gcp.container.Cluster',
  google_storage_bucket: 'gcp.storage.Bucket',
  azurerm_resource_group: 'azure.resources.ResourceGroup',
  azurerm_virtual_network: 'azure.network.VirtualNetwork',
  azurerm_subnet: 'azure.network.Subnet',
  azurerm_storage_account: 'azure.storage.StorageAccount',
};

function toCamelCase(value: string): string {
  return value.replace(/_([a-z0-9])/g, (_, c) => c.toUpperCase());
}

function toPascalCase(value: string): string {
  return value.split('_').filter(Boolean).map(part => part.charAt(0).toUpperCase() + part.slice(1)).join('');
}

function pulumiType(type: string): string {
  if (PULUMI_TYPE_OVERRIDES[type]) return PULUMI_TYPE_OVERRIDES[type];
  if (type.startsWith('aws_')) {
    const rest = type.slice(4);
    return `(aws as any).${guessAWSPackage(rest)}.${toPascalCase(rest)}`;
  }
  if (type.startsWith('google_')) {
    const rest = type.slice(7);
    return `(gcp as any).${guessGCPPackage(rest)}.${toPascalCase(rest)}`;
  }
  if (type.startsWith('azurerm_')) {
    const rest = type.slice(8);
    return `(azure as any).${guessAzurePackage(rest)}.${toPascalCase(rest)}`;
  }
  return toPascalCase(type);
}

function guessAWSPackage(rest: string): string {
  const head = rest.split('_')[0];
  const byHead: Record<string, string> = {
    s3: 's3',
    ec2: 'ec2',
    vpc: 'ec2',
    subnet: 'ec2',
    rds: 'rds',
    dynamodb: 'dynamodb',
    lambda: 'lambda',
    iam: 'iam',
    kms: 'kms',
    eks: 'eks',
    ecs: 'ecs',
    ecr: 'ecr',
    lb: 'lb',
    elb: 'elb',
    cloudwatch: 'cloudwatch',
    sns: 'sns',
    sqs: 'sqs',
    apigateway: 'apigateway',
  };
  return byHead[head] || 'ec2';
}

function guessGCPPackage(rest: string): string {
  const head = rest.split('_')[0];
  const byHead: Record<string, string> = {
    storage: 'storage',
    compute: 'compute',
    container: 'container',
    sql: 'sql',
    pubsub: 'pubsub',
    bigquery: 'bigquery',
  };
  return byHead[head] || 'compute';
}

function guessAzurePackage(rest: string): string {
  const head = rest.split('_')[0];
  const byHead: Record<string, string> = {
    resource: 'resources',
    storage: 'storage',
    virtual: 'network',
    subnet: 'network',
    network: 'network',
    key: 'keyvault',
    linux: 'compute',
    windows: 'compute',
    container: 'containerservice',
    managed: 'containerservice',
    postgresql: 'dbforpostgresql',
    mysql: 'dbformysql',
    redis: 'cache',
    app: 'web',
  };
  return byHead[head] || 'resources';
}

function tsValue(value: any, parentKey = ''): string {
  if (value === null || value === undefined) return 'undefined';
  if (typeof value === 'boolean' || typeof value === 'number') return String(value);
  if (typeof value === 'string') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(item => tsValue(item, parentKey)).join(', ')}]`;
  if (typeof value === 'object') {
    const preserveKeys = ['tags', 'labels', 'metadata', 'annotations', 'environment', 'environment_variables', 'env'].includes(parentKey);
    return `{ ${Object.keys(value).sort().map(key => {
      const renderedKey = preserveKeys ? key : toCamelCase(key);
      return `${JSON.stringify(renderedKey)}: ${tsValue(value[key], key)}`;
    }).join(', ')} }`;
  }
  return JSON.stringify(String(value));
}

function tsIdentifier(value: string, fallback: string): string {
  const raw = toCamelCase(value || fallback || 'resource').replace(/[^A-Za-z0-9_$]/g, '_');
  return /^[0-9]/.test(raw) ? `_${raw}` : raw;
}

function uniqueTSIdentifier(base: string, used: Set<string>): string {
  const root = base || 'resource';
  let candidate = root;
  let suffix = 2;
  while (used.has(candidate)) {
    candidate = `${root}${suffix}`;
    suffix += 1;
  }
  used.add(candidate);
  return candidate;
}

function isListConnectionField(field: string): boolean {
  return field.endsWith('_ids') || ['roles', 'security_groups', 'subnets'].includes(field);
}

// generateLocalCode mirrors the Go backend's HCL/YAML emitter so the
// preview stays in sync while the user is adding resources. The backend
// is authoritative on save; this is purely a UX affordance (no
// round-trip wait).
export function generateLocalCode(tool: string, nodes: any[], edges: Edge[]): string {
  if (tool === 'pulumi') {
    const hasGCP = nodes.some((n) => n.type.startsWith('google_'));
    const hasAzure = nodes.some((n) => n.type.startsWith('azurerm_'));
    const hasAWS = nodes.some((n) => n.type.startsWith('aws_'));
    let c = 'import * as pulumi from "@pulumi/pulumi";\n';
    if (hasAWS) c += 'import * as aws from "@pulumi/aws";\n';
    if (hasGCP) c += 'import * as gcp from "@pulumi/gcp";\n';
    if (hasAzure) c += 'import * as azure from "@pulumi/azure-native";\n';
    c += '\nconst config = new pulumi.Config("iac-studio");\n';
    c += 'const environment = config.get("environment") ?? "dev";\n\n';

    const usedNames = new Set<string>();
    const varByNodeId = new Map<string, string>();
    const nodeById = new Map<string, any>();
    nodes.forEach((n) => {
      const name = n.name || n.type.replace(/^(aws_|google_|azurerm_)/, '');
      const varName = uniqueTSIdentifier(tsIdentifier(name, n.type), usedNames);
      if (n.id) {
        varByNodeId.set(n.id, varName);
        nodeById.set(n.id, n);
      }
    });

    const edgesByFrom = new Map<string, Map<string, Edge[]>>();
    edges.forEach((edge) => {
      if (!edge.field || edge.field === 'depends_on') return;
      const byField = edgesByFrom.get(edge.from) || new Map<string, Edge[]>();
      const list = byField.get(edge.field) || [];
      list.push(edge);
      byField.set(edge.field, list);
      edgesByFrom.set(edge.from, byField);
    });

    nodes.forEach((n) => {
      const name = n.name || n.type.replace(/^(aws_|google_|azurerm_)/, '');
      const varName = (n.id && varByNodeId.get(n.id)) || tsIdentifier(name, n.type);
      c += `const ${varName} = new ${pulumiType(n.type)}(${JSON.stringify(name)}, {\n`;
      const emittedFields = new Set<string>();
      (n.id ? edgesByFrom.get(n.id) || new Map<string, Edge[]>() : new Map<string, Edge[]>()).forEach((fieldEdges, field) => {
        const targetVars = Array.from(new Set(fieldEdges
          .map((edge) => nodeById.has(edge.to) ? varByNodeId.get(edge.to) : undefined)
          .filter((targetVar): targetVar is string => Boolean(targetVar))));
        if (targetVars.length === 0) return;
        const refs = targetVars.map((targetVar) => `${targetVar}.id`);
        const value = refs.length > 1 || isListConnectionField(field) ? `[${refs.join(', ')}]` : refs[0];
        c += `    ${toCamelCase(field)}: ${value},\n`;
        emittedFields.add(field);
      });
      Object.entries(n.properties || {}).forEach(([k, v]) => {
        if (emittedFields.has(k) || k.startsWith('__')) return;
        c += `    ${toCamelCase(k)}: ${tsValue(v, k)},\n`;
      });
      c += '});\n\n';
    });
    return c;
  }

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
