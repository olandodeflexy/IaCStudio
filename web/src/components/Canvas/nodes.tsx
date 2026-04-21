import { memo } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';

import type { Resource } from '../../types';
import { cn } from '../../lib/utils';

// Custom xyflow node types for the swimlane canvas.
// - envHeader: top strip labeling an environment column.
// - envColumn: full-height band behind resources in one env; receives
//   pointer events only for panning, never selected.
// - moduleLabel: left-rail row label.
// - resource: an actual infrastructure resource node.
//
// All four are memoised: xyflow calls node components on every render
// frame of the graph and even pure ones get expensive at 100+ nodes.

export const EnvHeader = memo(({ data }: NodeProps) => (
  <div className="flex h-full w-full items-center justify-center rounded-t-md border-x border-t border-border bg-accent/40 px-4 text-xs font-semibold uppercase tracking-widest text-foreground">
    {(data as { label: string }).label}
  </div>
));
EnvHeader.displayName = 'EnvHeader';

export const EnvColumn = memo(() => (
  <div className="h-full w-full rounded-b-md border-x border-b border-border bg-muted/20" />
));
EnvColumn.displayName = 'EnvColumn';

export const ModuleLabel = memo(({ data }: NodeProps) => (
  <div className="flex h-full w-full items-center justify-end pr-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
    {(data as { label: string }).label}
  </div>
));
ModuleLabel.displayName = 'ModuleLabel';

export const ResourceNode = memo(({ data, selected }: NodeProps) => {
  const { resource } = data as { resource: Resource };
  return (
    <div
      className={cn(
        'flex h-full w-full flex-col justify-center rounded-md border bg-card px-3 py-2 shadow-sm transition-colors',
        selected
          ? 'border-primary ring-2 ring-primary/50'
          : 'border-border hover:border-primary/40',
      )}
    >
      <Handle type="target" position={Position.Left} className="!bg-primary" />
      <div className="truncate text-[11px] font-semibold uppercase tracking-wide text-primary">
        {resource.type}
      </div>
      <div className="truncate text-xs text-foreground">{resource.name}</div>
      <Handle type="source" position={Position.Right} className="!bg-primary" />
    </div>
  );
});
ResourceNode.displayName = 'ResourceNode';

export const swimlaneNodeTypes = {
  envHeader: EnvHeader,
  envColumn: EnvColumn,
  moduleLabel: ModuleLabel,
  resource: ResourceNode,
};
