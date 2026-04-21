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
  classify?: (r: Resource) => { environment: string; module: string } | null;
  onSelectResource?: (id: string) => void;
  className?: string;
}

const DEFAULT_ENVS_ROOT = 'environments';

// Default classifier for layered-v1 projects. Walks the resource's
// `file` path and picks out the environment + module segments based on
// the scaffolded directory layout:
//
//   environments/<env>/<module>/main.tf
//   modules/<module>/...              (no environment — skipped)
//
// Callers can override via SwimlaneCanvas.classify when their layout
// differs (e.g. workspaces vs. directories).
export function defaultLayeredClassifier(envs: string[], modules: string[]) {
  const envSet = new Set(envs);
  const modSet = new Set(modules);
  return (r: Resource) => {
    if (!r.file) return null;
    const parts = r.file.split('/');
    const idx = parts.indexOf(DEFAULT_ENVS_ROOT);
    if (idx < 0 || parts.length < idx + 3) return null;
    const env = parts[idx + 1];
    const mod = parts[idx + 2];
    if (!envSet.has(env) || !modSet.has(mod)) return null;
    return { environment: env, module: mod };
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
