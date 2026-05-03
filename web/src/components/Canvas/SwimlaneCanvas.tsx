import { useMemo } from 'react';
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  type Node,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';

import type { LayeredProject, Resource } from '../../types';

import { buildSwimlaneLayout, cellKey, groupResourcesByCell } from './swimlaneLayout';
import { swimlaneNodeTypes } from './nodes';

export interface SwimlaneCanvasProps {
  project: LayeredProject;
  resources: Resource[];
  // classify receives a Resource and must return which (env, module)
  // cell it belongs in, or null to hide it. Default strategy: parse
  // the resource's `file` path as `<envs-root>/<env>/<module>/...`
  // which matches the layered-v1 scaffold layout from #1.
  classify?: (_r: Resource) => { environment: string; module: string } | null;
  onSelectResource?: (_id: string) => void;
  className?: string;
}

// Default classifier for the layered-v1 scaffold, which on disk looks
// like:
//
//   environments/<env>/{main,variables,outputs,backend}.tf
//   modules/<module>/{main,variables,outputs,versions}.tf
//
// Note the scaffold does NOT nest modules under environments — each
// environment's main.tf instantiates a module via an HCL `module "<x>"
// { source = "../../modules/<x>" }` block. The parser reports two
// kinds of resources:
//
//   (1) resources defined inside `modules/<mod>/...`    — shared template
//   (2) resources defined inside `environments/<env>/...` — env-local
//
// The swimlane view only meaningfully renders (2) today: the env is
// explicit, and the module is guessed from the filename stem when it
// matches a known module name, falling back to 'root' for env-level
// plumbing that isn't a module instantiation.
//
// (1) is returned as null — module templates don't belong to any
// single environment column. A later commit can expand them via HCL
// module-block introspection so e.g. `modules/networking/aws_vpc.main`
// shows up in (dev, networking), (stage, networking), (prod,
// networking) with a "template" badge.
//
// Callers can override via SwimlaneCanvas.classify when their layout
// differs (workspaces, Terragrunt, monorepo paths, …). 'root' is
// treated as a valid module name when present in the modules array —
// include it in LayeredProject.modules if you want env-level resources
// to have a row.
export function defaultLayeredClassifier(envs: string[], modules: string[]) {
  const envSet = new Set(envs);
  const modSet = new Set(modules);
  return (r: Resource) => {
    if (!r.file) return null;
    const parts = r.file.split('/');

    // modules/<mod>/...  → shared template, hide from the per-env view.
    const modIdx = parts.indexOf('modules');
    if (modIdx >= 0 && parts.length > modIdx + 1 && modSet.has(parts[modIdx + 1])) {
      return null;
    }

    // environments/<env>/<file> → env-scoped. Module = filename stem if
    // it matches a registered module, otherwise 'root'.
    const envIdx = parts.indexOf('environments');
    if (envIdx >= 0 && parts.length > envIdx + 1 && envSet.has(parts[envIdx + 1])) {
      const env = parts[envIdx + 1];
      const fileName = parts[parts.length - 1] ?? '';
      const stem = fileName.replace(/\.(tf|ts)$/, '');
      const module = modSet.has(stem) ? stem : 'root';
      if (!modSet.has(module)) return null;
      return { environment: env, module };
    }
    return null;
  };
}

// SwimlaneCanvas is the layered-v1 view: columns are environments,
// rows are modules, resources pack into their (env, module) cell.
// Freeform editing stays in the legacy FlowCanvas (still inside
// App.tsx until commit 4's split).
export function SwimlaneCanvas({
  project,
  resources,
  classify,
  onSelectResource,
  className,
}: SwimlaneCanvasProps) {
  const moduleNames = useMemo(() => project.modules.map((m) => m.name), [project.modules]);
  const resolvedClassify = classify ?? defaultLayeredClassifier(project.environments, moduleNames);

  const { nodes, edges } = useMemo(() => {
    const cells = groupResourcesByCell(resources, resolvedClassify);
    return buildSwimlaneLayout({
      environments: project.environments,
      modules: moduleNames,
      cells,
    });
  }, [project.environments, moduleNames, resources, resolvedClassify]);

  const handleSelection = (_: unknown, node: Node) => {
    if (node.type !== 'resource' || !onSelectResource) return;
    const res = (node.data as { resource?: Resource }).resource;
    if (res) onSelectResource(res.id);
  };

  return (
    <div className={className} style={{ width: '100%', height: '100%' }}>
      <ReactFlowProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={swimlaneNodeTypes}
          fitView
          fitViewOptions={{ padding: 0.15 }}
          panOnDrag
          zoomOnScroll
          onNodeClick={handleSelection}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={24} size={1} color="#27312d" />
          <MiniMap pannable zoomable maskColor="rgba(16,20,19,0.8)" />
          <Controls showInteractive={false} />
        </ReactFlow>
      </ReactFlowProvider>
    </div>
  );
}

// cellKey is re-exported so callers composing a custom classifier can
// reuse the canonical separator without importing the layout module
// directly.
export { cellKey };
