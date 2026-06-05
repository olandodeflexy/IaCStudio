import type { CloudConnection, Resource } from '../../api';
import type { Edge } from '../../legacy';
import { S } from '../../styles';
import { CloudConnectionsPanel } from '../CloudConnections';
import { CodeEditor } from '../CodeEditor';
import { ModuleRegistryPanel } from '../ModuleRegistry';
import { PolicyStudioPanel } from '../PolicyStudio';
import { ScanPanel } from '../ScanPanel';

export type RightPanelTab = 'inspect' | 'cloud' | 'policy' | 'scan' | 'modules';

export interface InspectorResource extends Resource {
  icon: string;
  label: string;
}

export interface InspectorToolMeta {
  color: string;
  ext: string;
}

export interface InspectorPanelProps {
  width: number;
  activeTab: RightPanelTab;
  tool: string;
  toolMeta: InspectorToolMeta;
  activeEnv?: string | null;
  projectId: string;
  nodes: InspectorResource[];
  edges: Edge[];
  selectedNodeId: string | null;
  selectedEdgeId: string | null;
  syncCode: string;
  codeFileLabel: string;
  codeEditorFilePath: string;
  codeSaving: boolean;
  unresolvedHybridEnv: boolean;
  selectedCloudConnection?: CloudConnection | null;
  onTabChange: (_tab: RightPanelTab) => void;
  onDeleteEdge: (_edgeId: string) => void;
  onSelectEdge: (_edgeId: string) => void;
  onUpdateNodeName: (_nodeId: string, _name: string) => void;
  onUpdateNodeProp: (_nodeId: string, _key: string, _value: any) => void;
  onSyncCodeChange: (_value: string) => void;
  onSaveCode: (_value: string) => void;
  onCloudConnectionSelect?: (_connection: CloudConnection | null) => void;
  onCopyCode?: (_value: string) => void;
}

const EMPTY_CODE_PLACEHOLDER = 'Add resources from the palette or write code here';

const fieldId = (nodeId: string, field: string) => (
  `inspector-${nodeId}-${field}`.replace(/[^a-zA-Z0-9_-]/g, '-')
);

export function InspectorPanel({
  width,
  activeTab,
  tool,
  toolMeta,
  activeEnv,
  projectId,
  nodes,
  edges,
  selectedNodeId,
  selectedEdgeId,
  syncCode,
  codeFileLabel,
  codeEditorFilePath,
  codeSaving,
  unresolvedHybridEnv,
  selectedCloudConnection,
  onTabChange,
  onDeleteEdge,
  onSelectEdge,
  onUpdateNodeName,
  onUpdateNodeProp,
  onSyncCodeChange,
  onSaveCode,
  onCloudConnectionSelect,
  onCopyCode,
}: InspectorPanelProps) {
  const selected = nodes.find(node => node.id === selectedNodeId);
  const selectedEdge = edges.find(edge => edge.id === selectedEdgeId);
  const saveDisabled = codeSaving || unresolvedHybridEnv || !syncCode.trim();

  const copyCode = async () => {
    if (onCopyCode) {
      onCopyCode(syncCode);
      return;
    }
    try {
      await navigator.clipboard?.writeText(syncCode);
    } catch (err) {
      console.warn('Failed to copy code to clipboard', err);
    }
  };

  const tabs: { key: RightPanelTab; label: string }[] = [
    { key: 'inspect', label: selected || selectedEdge ? 'Inspect' : 'Code' },
    { key: 'cloud', label: 'Cloud' },
    { key: 'policy', label: 'Policy' },
    { key: 'scan', label: 'Scan' },
    ...(tool === 'ansible' ? [] : [{ key: 'modules' as const, label: 'Modules' }]),
  ];

  return (
    <aside style={{ ...S.right, width }}>
      <div style={S.tabs}>
        {tabs.map(tab => (
          <button
            key={tab.key}
            style={{
              ...S.tab,
              ...(activeTab === tab.key ? { color: toolMeta.color, borderBottomColor: toolMeta.color } : {}),
              fontSize: 10,
            }}
            onClick={() => onTabChange(tab.key)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {activeTab === 'inspect' && (
        <>
          {selectedEdge && (() => {
            const fromNode = nodes.find(node => node.id === selectedEdge.from);
            const toNode = nodes.find(node => node.id === selectedEdge.to);
            return (
              <div style={S.props}>
                <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12 }}>🔗 Connection</div>
                <div style={S.field}>
                  <div style={S.flabel}>From</div>
                  <div style={{ fontSize: 12, color: '#aaa', fontFamily: 'JetBrains Mono' }}>{fromNode?.icon} {fromNode?.type}.{fromNode?.name}</div>
                </div>
                <div style={S.field}>
                  <div style={S.flabel}>To</div>
                  <div style={{ fontSize: 12, color: '#aaa', fontFamily: 'JetBrains Mono' }}>{toNode?.icon} {toNode?.type}.{toNode?.name}</div>
                </div>
                <div style={S.field}>
                  <div style={S.flabel}>Via Field</div>
                  <div style={{ fontSize: 12, color: toolMeta.color, fontFamily: 'JetBrains Mono' }}>{selectedEdge.field}</div>
                </div>
                <div style={S.field}>
                  <div style={S.flabel}>Generated Reference</div>
                  <div style={{ fontSize: 11, color: '#888', fontFamily: 'JetBrains Mono', background: '#111120', padding: '6px 8px', borderRadius: 4 }}>
                    {selectedEdge.field} = {toNode?.type}.{toNode?.name}.id
                  </div>
                </div>
                <button
                  style={{ ...S.cmd, background: '#ef444433', color: '#ef4444', width: '100%', marginTop: 8 }}
                  onClick={() => onDeleteEdge(selectedEdge.id)}
                >
                  Delete Connection
                </button>
              </div>
            );
          })()}

          {selected && !selectedEdge && (
            <div style={S.props}>
              <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12 }}>{selected.icon} Properties</div>
              <div style={S.field}>
                <label style={S.flabel} htmlFor={fieldId(selected.id, 'name')}>Name</label>
                <input
                  id={fieldId(selected.id, 'name')}
                  style={S.finput}
                  value={selected.name}
                  onChange={event => onUpdateNodeName(selected.id, event.target.value)}
                />
              </div>
              {Object.entries(selected.properties).map(([key, value]) => (
                <div key={key} style={S.field}>
                  <label style={S.flabel} htmlFor={fieldId(selected.id, key)}>{key}</label>
                  {typeof value === 'boolean' ? (
                    <button
                      id={fieldId(selected.id, key)}
                      aria-pressed={value}
                      style={{ ...S.ftoggle, background: value ? toolMeta.color + '33' : 'var(--bg-elev-2)', color: value ? toolMeta.color : 'var(--text-muted)' }}
                      onClick={() => onUpdateNodeProp(selected.id, key, !value)}
                    >
                      {value ? 'true' : 'false'}
                    </button>
                  ) : (
                    <input
                      id={fieldId(selected.id, key)}
                      style={S.finput}
                      value={String(value)}
                      onChange={event => onUpdateNodeProp(selected.id, key, event.target.value)}
                    />
                  )}
                </div>
              ))}

              {(() => {
                const nodeEdges = edges.filter(edge => edge.from === selected.id || edge.to === selected.id);
                if (nodeEdges.length === 0) return null;
                return (
                  <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--border-soft)' }}>
                    <div style={S.flabel}>Connections ({nodeEdges.length})</div>
                    {nodeEdges.map(edge => {
                      const other = nodes.find(node => node.id === (edge.from === selected.id ? edge.to : edge.from));
                      const direction = edge.from === selected.id ? '→' : '←';
                      return (
                        <button
                          key={edge.id}
                          type="button"
                          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 0', fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono', cursor: 'pointer', background: 'transparent', border: 0, width: '100%', textAlign: 'left' }}
                          onClick={() => onSelectEdge(edge.id)}
                        >
                          <span>{direction}</span>
                          <span>{other?.icon}</span>
                          <span style={{ color: '#aaa' }}>{other?.name}</span>
                          <span style={{ color: toolMeta.color, marginLeft: 'auto', fontSize: 9 }}>{edge.field}</span>
                        </button>
                      );
                    })}
                  </div>
                );
              })()}
            </div>
          )}

          <div style={S.codePanel}>
            <div style={S.codeHead}>
              <span>FILE {codeFileLabel}</span>
              <button
                style={{ ...S.copyBtn, color: saveDisabled ? '#555' : toolMeta.color }}
                disabled={saveDisabled}
                title="Save editor buffer to disk"
                onClick={() => onSaveCode(syncCode)}
              >
                {codeSaving ? 'Saving...' : 'Save'}
              </button>
              <button style={{ ...S.copyBtn, color: toolMeta.color }} onClick={copyCode}>Copy</button>
            </div>
            <div style={S.codePre}>
              <div style={{ position: 'relative', flex: 1, minWidth: 0, height: '100%', display: 'flex' }}>
                {!syncCode && (
                  <div style={{ position: 'absolute', top: 14, left: 18, zIndex: 1, color: '#555', fontFamily: 'JetBrains Mono', fontSize: 13, pointerEvents: 'none' }}>
                    {EMPTY_CODE_PLACEHOLDER}
                  </div>
                )}
                <CodeEditor
                  value={syncCode}
                  filePath={codeEditorFilePath}
                  readOnly={false}
                  onChange={onSyncCodeChange}
                  onSave={saveDisabled ? undefined : onSaveCode}
                />
              </div>
            </div>
          </div>
        </>
      )}

      {activeTab === 'cloud' && (
        <div style={{ flex: 1, minHeight: 0 }}>
          <CloudConnectionsPanel
            selectedConnectionId={selectedCloudConnection?.id}
            onConnectionSelected={onCloudConnectionSelect}
          />
        </div>
      )}

      {activeTab === 'policy' && (
        <div style={{ flex: 1, minHeight: 0 }}>
          {unresolvedHybridEnv ? (
            <div style={{ padding: 16, color: 'var(--text-muted)', fontSize: 13 }}>
              Environment "{activeEnv}" has no configured IaC tool in .iac-studio.json.
            </div>
          ) : (
            <PolicyStudioPanel projectName={projectId} tool={tool} env={activeEnv || undefined} />
          )}
        </div>
      )}

      {activeTab === 'scan' && (
        <div style={{ flex: 1, minHeight: 0 }}>
          <ScanPanel projectName={projectId} tool={tool} />
        </div>
      )}

      {activeTab === 'modules' && tool !== 'ansible' && (
        <div style={{ flex: 1, minHeight: 0 }}>
          <ModuleRegistryPanel initialQuery="vpc" />
        </div>
      )}
    </aside>
  );
}
