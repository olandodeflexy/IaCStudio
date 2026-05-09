import { useMemo, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type RefObject } from 'react';

import type { Resource } from '../../api';
import type { Edge } from '../../legacy';
import { S } from '../../styles';
import type { LayeredProject } from '../../types';

import { SwimlaneCanvas } from './SwimlaneCanvas';

export type CanvasMode = 'freeform' | 'swimlane';

export interface CanvasResource extends Resource {
  x: number;
  y: number;
  icon: string;
  label: string;
}

export interface CanvasToolMeta {
  color: string;
}

export interface ConnectingPreview {
  fromId: string;
  x: number;
  y: number;
}

export interface CanvasPanelProps {
  canvasRef: RefObject<HTMLElement>;
  nodes: CanvasResource[];
  edges: Edge[];
  selectedNodeId: string | null;
  selectedEdgeId: string | null;
  connecting: ConnectingPreview | null;
  layeredProject: LayeredProject | null;
  showEnvironmentSelector: boolean;
  activeEnvironment: string | null;
  canvasMode: CanvasMode;
  toolMeta: CanvasToolMeta;
  onMouseMove: (_event: ReactMouseEvent<HTMLElement>) => void;
  onDragEnd: () => void;
  onConnectionCancel: () => void;
  onNodeDragStart: (_event: ReactMouseEvent<HTMLDivElement>, _nodeId: string) => void;
  onStartConnection: (_nodeId: string, _position: { x: number; y: number }) => void;
  onCompleteConnection: (_nodeId: string) => void;
  onSelectNode: (_nodeId: string) => void;
  onSelectEdge: (_edgeId: string) => void;
  onClearSelection: () => void;
  onDeleteNode: (_nodeId: string) => void;
  onActiveEnvironmentChange: (_env: string) => void;
  onCanvasModeChange: (_mode: CanvasMode) => void;
}

const NODE_W = 180;
const NODE_H = 70;

export function CanvasPanel({
  canvasRef,
  nodes,
  edges,
  selectedNodeId,
  selectedEdgeId,
  connecting,
  layeredProject,
  showEnvironmentSelector,
  activeEnvironment,
  canvasMode,
  toolMeta,
  onMouseMove,
  onDragEnd,
  onConnectionCancel,
  onNodeDragStart,
  onStartConnection,
  onCompleteConnection,
  onSelectNode,
  onSelectEdge,
  onClearSelection,
  onDeleteNode,
  onActiveEnvironmentChange,
  onCanvasModeChange,
}: CanvasPanelProps) {
  const isSwimlaneMode = Boolean(layeredProject && canvasMode === 'swimlane');
  const nodeById = useMemo(() => new Map(nodes.map(node => [node.id, node])), [nodes]);
  const connectionCountByNodeId = useMemo(() => {
    const counts = new Map<string, number>();
    edges.forEach(edge => {
      counts.set(edge.from, (counts.get(edge.from) ?? 0) + 1);
      counts.set(edge.to, (counts.get(edge.to) ?? 0) + 1);
    });
    return counts;
  }, [edges]);

  const selectEdge = (edgeId: string) => {
    onSelectEdge(edgeId);
  };

  const selectEdgeFromKeyboard = (event: ReactKeyboardEvent, edgeId: string) => {
    if (!['Enter', ' ', 'Space', 'Spacebar'].includes(event.key) && event.code !== 'Space') return;
    event.preventDefault();
    selectEdge(edgeId);
  };

  const startConnection = (event: ReactMouseEvent, nodeId: string) => {
    event.stopPropagation();
    const rect = canvasRef.current?.getBoundingClientRect();
    if (!rect) return;
    onStartConnection(nodeId, {
      x: event.clientX - rect.left,
      y: event.clientY - rect.top,
    });
  };

  const completeConnection = (event: ReactMouseEvent<HTMLDivElement>, nodeId: string) => {
    if (!connecting || connecting.fromId === nodeId) return;
    event.stopPropagation();
    onCompleteConnection(nodeId);
  };

  const previewFromNode = connecting ? nodeById.get(connecting.fromId) : null;

  return (
    <main
      style={S.canvas}
      ref={canvasRef}
      onMouseMove={isSwimlaneMode ? undefined : onMouseMove}
      onMouseUp={() => {
        if (isSwimlaneMode) return;
        onDragEnd();
        if (connecting) onConnectionCancel();
      }}
      onMouseLeave={() => {
        if (!isSwimlaneMode) {
          onDragEnd();
          if (connecting) onConnectionCancel();
        }
      }}
      onClick={onClearSelection}
    >
      {layeredProject && (
        <div style={{ position: 'absolute', top: 12, right: 12, zIndex: 10, display: 'flex', gap: 6, padding: 4, background: 'var(--bg-elev-1)', border: '1px solid var(--border-main)', borderRadius: 8 }}>
          {showEnvironmentSelector && layeredProject.environments.map(env => {
            const envTool = layeredProject.environmentTools?.[env];
            return (
              <button
                key={env}
                type="button"
                aria-pressed={activeEnvironment === env}
                style={{ ...S.cmd, background: activeEnvironment === env ? toolMeta.color + '22' : 'transparent', color: activeEnvironment === env ? toolMeta.color : 'var(--text-muted)', padding: '5px 9px' }}
                onClick={(event) => {
                  event.stopPropagation();
                  onActiveEnvironmentChange(env);
                }}
                title={envTool ? `${env} uses ${envTool}` : env}
              >
                {envTool ? `${env} (${envTool})` : env}
              </button>
            );
          })}
          {(['swimlane', 'freeform'] as const).map(mode => (
            <button
              key={mode}
              type="button"
              aria-pressed={canvasMode === mode}
              style={{ ...S.cmd, background: canvasMode === mode ? toolMeta.color + '22' : 'transparent', color: canvasMode === mode ? toolMeta.color : 'var(--text-muted)', padding: '5px 9px' }}
              onClick={(event) => {
                event.stopPropagation();
                if (connecting) onConnectionCancel();
                onCanvasModeChange(mode);
              }}
            >
              {mode === 'swimlane' ? 'Swimlane' : 'Freeform'}
            </button>
          ))}
        </div>
      )}

      {isSwimlaneMode && layeredProject ? (
        <SwimlaneCanvas
          project={layeredProject}
          resources={nodes}
          onSelectResource={onSelectNode}
        />
      ) : (
        <>
          <div style={S.grid} className="iac-canvas-grid" />

          <svg style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', zIndex: 1 }}>
            <defs>
              <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
                <polygon points="0 0, 10 3.5, 0 7" fill={toolMeta.color} opacity="0.6" />
              </marker>
              <marker id="arrowhead-hover" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
                <polygon points="0 0, 10 3.5, 0 7" fill={toolMeta.color} />
              </marker>
            </defs>
            {edges.map(edge => {
              const fromNode = nodeById.get(edge.from);
              const toNode = nodeById.get(edge.to);
              if (!fromNode || !toNode) return null;
              const x1 = fromNode.x + NODE_W / 2;
              const y1 = fromNode.y + NODE_H;
              const x2 = toNode.x + NODE_W / 2;
              const y2 = toNode.y;
              const mx = (x1 + x2) / 2;
              const my = (y1 + y2) / 2;
              const isSelected = selectedEdgeId === edge.id;
              const path = `M ${x1} ${y1} C ${x1} ${y1 + 40}, ${x2} ${y2 - 40}, ${x2} ${y2}`;
              return (
                <g key={edge.id}>
                  <path
                    d={path}
                    fill="none"
                    stroke="transparent"
                    strokeWidth={12}
                    role="button"
                    tabIndex={0}
                    aria-label={`Select connection ${edge.field}`}
                    style={{ pointerEvents: 'stroke', cursor: 'pointer' }}
                    onClick={(event) => {
                      event.stopPropagation();
                      selectEdge(edge.id);
                    }}
                    onKeyDown={(event) => selectEdgeFromKeyboard(event, edge.id)}
                  />
                  <path
                    d={path}
                    fill="none"
                    stroke={isSelected ? toolMeta.color : `${toolMeta.color}55`}
                    strokeWidth={isSelected ? 2.5 : 1.5}
                    strokeDasharray={isSelected ? 'none' : '6 4'}
                    markerEnd={isSelected ? 'url(#arrowhead-hover)' : 'url(#arrowhead)'}
                    style={{ transition: 'stroke 0.2s, stroke-width 0.2s' }}
                  />
                  <text x={mx} y={my - 6} textAnchor="middle" fill={isSelected ? toolMeta.color : '#555'}
                    fontSize={9} fontFamily="JetBrains Mono" style={{ pointerEvents: 'none' }}>
                    {edge.field}
                  </text>
                </g>
              );
            })}
            {connecting && previewFromNode && (
              <line
                x1={previewFromNode.x + NODE_W / 2}
                y1={previewFromNode.y + NODE_H}
                x2={connecting.x}
                y2={connecting.y}
                stroke={toolMeta.color}
                strokeWidth={2}
                strokeDasharray="4 4"
                opacity={0.6}
              />
            )}
          </svg>

          {nodes.length === 0 && (
            <div style={S.empty}>
              <div style={{ fontSize: 20, opacity: 0.5, marginBottom: 16, fontFamily: 'JetBrains Mono', letterSpacing: 1.5 }}>CANVAS</div>
              <div style={{ fontSize: 16, opacity: 0.4 }}>Drag resources from the palette</div>
              <div style={{ fontSize: 14, opacity: 0.3, marginTop: 4 }}>or use AI chat below</div>
            </div>
          )}

          {nodes.map(node => {
            const connectionCount = connectionCountByNodeId.get(node.id) ?? 0;
            const hasConnections = connectionCount > 0;
            return (
              <div
                key={node.id}
                className="node-shell"
                style={{
                  ...S.node,
                  left: node.x,
                  top: node.y,
                  zIndex: 2,
                  borderColor: selectedNodeId === node.id ? toolMeta.color : hasConnections ? `${toolMeta.color}44` : 'var(--border-main)',
                  boxShadow: selectedNodeId === node.id ? `0 0 20px ${toolMeta.color}33` : '0 4px 12px rgba(0,0,0,0.3)',
                }}
                onMouseDown={event => onNodeDragStart(event, node.id)}
                onClick={event => {
                  event.stopPropagation();
                  onSelectNode(node.id);
                }}
                onMouseUp={event => completeConnection(event, node.id)}
              >
                <div style={S.nodeHead}>
                  <span style={{ fontSize: 18 }}>{node.icon}</span>
                  <span style={{ fontSize: 13, fontWeight: 600, color: '#ddd', flex: 1 }}>{node.label}</span>
                  {hasConnections && <span style={{ fontSize: 9, color: toolMeta.color, fontFamily: 'JetBrains Mono' }}>{connectionCount}</span>}
                  <button
                    type="button"
                    style={S.nodeDel}
                    aria-label={`Delete ${node.label}`}
                    onClick={event => {
                      event.stopPropagation();
                      onDeleteNode(node.id);
                    }}
                  >
                    ×
                  </button>
                </div>
                <div style={{ fontSize: 10, color: '#555', padding: '0 12px', fontFamily: 'JetBrains Mono' }}>{node.type}</div>
                <div style={{ display: 'flex', justifyContent: 'space-between', padding: '4px 12px 8px' }}>
                  <span style={{ fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono' }}>{node.name}</span>
                  <button
                    type="button"
                    style={{ width: 14, height: 14, borderRadius: '50%', border: `2px solid ${toolMeta.color}55`, background: 'var(--bg-elev-2)', cursor: 'crosshair', flexShrink: 0, padding: 0 }}
                    title="Drag to connect"
                    aria-label={`Start connection from ${node.label}`}
                    onMouseDown={event => startConnection(event, node.id)}
                  />
                </div>
              </div>
            );
          })}
        </>
      )}
    </main>
  );
}
