import type { Edge, Node } from '@xyflow/react';

import type { Resource } from '../../types';

// Swimlane layout engine — turns a {environments, modules, resources-per-cell}
// description into xyflow nodes with absolute positions. Pure function
// so we can unit-test it without React Flow.
//
// Visual model:
//
//   ┌─────────┬──────── dev ────────┬──────── stage ──────┬──────── prod ───────┐
//   │  module │                     │                     │                     │
//   │ network │  [vpc] [subnet]     │  [vpc] [subnet]     │  [vpc] [subnet]     │
//   │  compute│  [ec2]              │  [ec2] [ec2]        │  [ec2] [ec2]        │
//   │   data  │  [rds]              │  [rds]              │  [rds] [replica]    │
//   └─────────┴─────────────────────┴─────────────────────┴─────────────────────┘
//
// Env columns are group nodes (xyflow draws a subtle border around the
// children); resource nodes are positioned absolutely within their
// column. Module row labels sit in a fixed-width gutter on the left.

export interface SwimlaneInput {
  environments: string[];
  modules: string[];
  // cells["env::module"] → resources rendered in that intersection
  cells: Record<string, Resource[]>;
}

export interface SwimlaneOutput {
  nodes: Node[];
  edges: Edge[];
}

// Layout constants. Tunable in one place so the test can assert against
// the same source of truth the component uses.
export const LAYOUT = {
  gutterWidth: 140,     // left rail with module names
  envWidth: 320,        // width of each environment column
  envGap: 24,           // gutter between columns
  rowHeight: 180,       // height of one module row (one cell)
  rowGap: 12,           // vertical spacing between rows
  headerHeight: 44,     // top strip holding environment names
  resourceWidth: 140,   // rendered width of a resource node
  resourceHeight: 60,   // rendered height of a resource node
  resourceGap: 8,       // gap between resources inside a cell
  padding: 12,          // inner padding of a cell before the first resource
} as const;

// cellKey is the canonical join key for SwimlaneInput.cells. Exported so
// callers can construct the input without reimplementing the
// separator.
export function cellKey(env: string, module: string): string {
  return `${env}::${module}`;
}

export function buildSwimlaneLayout(input: SwimlaneInput): SwimlaneOutput {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  const { gutterWidth, envWidth, envGap, rowHeight, rowGap, headerHeight, resourceWidth, resourceHeight, resourceGap, padding } = LAYOUT;

  const totalHeight = headerHeight + input.modules.length * (rowHeight + rowGap) - rowGap;

  // Environment column headers + group containers. The group node is
  // what xyflow uses for visual grouping; the header is a separate
  // plain node so its styling can differ.
  input.environments.forEach((env, ei) => {
    const x = gutterWidth + ei * (envWidth + envGap);

    nodes.push({
      id: `env-header-${env}`,
      type: 'envHeader',
      position: { x, y: 0 },
      data: { label: env, environmentIndex: ei },
      draggable: false,
      selectable: false,
      style: { width: envWidth, height: headerHeight },
    });

    nodes.push({
      id: `env-col-${env}`,
      type: 'envColumn',
      position: { x, y: headerHeight },
      data: { environment: env },
      draggable: false,
      selectable: false,
      style: {
        width: envWidth,
        height: totalHeight - headerHeight,
      },
    });
  });

  // Module row labels in the left gutter.
  input.modules.forEach((mod, mi) => {
    nodes.push({
      id: `module-label-${mod}`,
      type: 'moduleLabel',
      position: { x: 0, y: headerHeight + mi * (rowHeight + rowGap) },
      data: { label: mod, moduleIndex: mi },
      draggable: false,
      selectable: false,
      style: { width: gutterWidth, height: rowHeight },
    });
  });

  // Resource nodes, packed into their (env, module) cell.
  input.environments.forEach((env, ei) => {
    const cellX = gutterWidth + ei * (envWidth + envGap);

    input.modules.forEach((mod, mi) => {
      const cellY = headerHeight + mi * (rowHeight + rowGap);
      const resources = input.cells[cellKey(env, mod)] ?? [];

      // Grid pack — floor(availWidth / (resourceWidth + gap)) per row.
      const availWidth = envWidth - padding * 2;
      const perRow = Math.max(1, Math.floor((availWidth + resourceGap) / (resourceWidth + resourceGap)));

      resources.forEach((r, ri) => {
        const col = ri % perRow;
        const row = Math.floor(ri / perRow);
        nodes.push({
          id: `res-${env}-${mod}-${r.id}`,
          type: 'resource',
          position: {
            x: cellX + padding + col * (resourceWidth + resourceGap),
            y: cellY + padding + row * (resourceHeight + resourceGap),
          },
          data: {
            resource: r,
            environment: env,
            module: mod,
          },
          style: { width: resourceWidth, height: resourceHeight },
        });
      });
    });
  });

  return { nodes, edges };
}

// groupResourcesByCell is the ergonomic adapter: given a flat Resource[]
// and a per-resource (env, module) resolver, produces the shape
// buildSwimlaneLayout expects. Keeps the layout function a pure
// transform while the caller decides how to classify each resource
// (file path segmentation, tag, metadata field, etc.).
export function groupResourcesByCell(
  resources: Resource[],
  classify: (r: Resource) => { environment: string; module: string } | null,
): Record<string, Resource[]> {
  const out: Record<string, Resource[]> = {};
  for (const r of resources) {
    const cell = classify(r);
    if (!cell) continue;
    const key = cellKey(cell.environment, cell.module);
    (out[key] ??= []).push(r);
  }
  return out;
}
