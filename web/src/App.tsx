import { useState, useCallback, useRef, useEffect } from 'react';
import { api, Resource, ToolInfo, CatalogResource, Suggestion } from './api';
import { useWebSocket, WSMessage } from './useWebSocket';

// ─── Tool Definitions (UI metadata only — resources loaded from backend catalog) ───
const TOOLS: Record<string, { name: string; icon: string; color: string; ext: string }> = {
  terraform: { name: 'Terraform', icon: '⬡', color: '#7B42F6', ext: '.tf' },
  opentofu: { name: 'OpenTofu', icon: '🟢', color: '#FFDA18', ext: '.tf' },
  ansible: { name: 'Ansible', icon: '🅰️', color: '#EE0000', ext: '.yml' },
};

// Fallback resources when backend is unreachable (small subset)
const FALLBACK_RESOURCES: CatalogResource[] = [
  { type: 'aws_vpc', label: 'VPC', icon: '🌐', category: 'Networking' },
  { type: 'aws_subnet', label: 'Subnet', icon: '📡', category: 'Networking' },
  { type: 'aws_instance', label: 'EC2 Instance', icon: '🖥️', category: 'Compute' },
  { type: 'aws_s3_bucket', label: 'S3 Bucket', icon: '🪣', category: 'Storage' },
  { type: 'aws_security_group', label: 'Security Group', icon: '🛡️', category: 'Security' },
];

// Connection edge between two resource nodes
interface Edge {
  id: string;
  from: string;     // source node id
  to: string;       // target node id
  fromType: string; // source resource type
  toType: string;   // target resource type
  field: string;    // the field that creates this connection (e.g., "vpc_id")
  label: string;    // human-readable label for the connection
}

let _id = 0;
const uid = () => `node_${++_id}_${Date.now()}`;
const edgeId = (from: string, to: string, field: string) => `${from}->${to}:${field}`;

export default function App() {
  const [tool, setTool] = useState<string | null>(null);
  const [detectedTools, setDetectedTools] = useState<ToolInfo[]>([]);
  const [projectName, setProjectName] = useState('my-infra-project');
  const [catalogResources, setCatalogResources] = useState<CatalogResource[]>([]);
  const [projectId, setProjectId] = useState(''); // immutable after creation — used for API calls
  const [nodes, setNodes] = useState<(Resource & { x: number; y: number; icon: string; label: string })[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<{ fromId: string; x: number; y: number } | null>(null);
  const [chatMessages, setChatMessages] = useState<{ role: string; text: string }[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [activePanel, setActivePanel] = useState('palette');
  const [showGuide, setShowGuide] = useState(true);
  const [terminalOutput, setTerminalOutput] = useState<string[]>([]);
  const [dragging, setDragging] = useState<{ id: string; ox: number; oy: number } | null>(null);
  const [wsConnected, setWsConnected] = useState(false);
  const [syncCode, setSyncCode] = useState('');
  const [notification, setNotification] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [hoveredResource, setHoveredResource] = useState<CatalogResource | null>(null);
  const [hoverPos, setHoverPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 });
  // Resizable panel sizes
  const [sidebarWidth, setSidebarWidth] = useState(240);
  const [rightWidth, setRightWidth] = useState(300);
  const [bottomHeight, setBottomHeight] = useState(220);
  const [resizing, setResizing] = useState<{ panel: string; startPos: number; startSize: number } | null>(null);

  const canvasRef = useRef<HTMLDivElement>(null);
  const chatEndRef = useRef<HTMLDivElement>(null);

  // Detect tools on mount
  useEffect(() => {
    api.detectTools().then(setDetectedTools).catch(() => {});
  }, []);

  // WebSocket for live sync
  const handleWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'terminal' && msg.output) {
      setTerminalOutput(prev => [...prev, ...msg.output!.split('\n')]);
      if (msg.error) setTerminalOutput(prev => [...prev, `ERROR: ${msg.error}`]);
    }
    if (msg.type === 'file_changed') {
      setNotification(`File changed externally: ${msg.file?.split('/').pop()}`);
      setTimeout(() => setNotification(null), 4000);
      // Re-parse project to update UI
      if (msg.project && msg.tool) {
        api.getResources(msg.project, msg.tool).then(resources => {
          // Merge positions from existing nodes
          setNodes(prev => {
            return resources.map(r => {
              const existing = prev.find(n => n.id === r.id);
              return {
                ...r,
                x: existing?.x ?? 80 + Math.random() * 300,
                y: existing?.y ?? 80 + Math.random() * 200,
                icon: existing?.icon ?? '📦',
                label: existing?.label ?? r.type,
              };
            });
          });
        }).catch(() => {});
      }
    }
  }, []);

  const { connected } = useWebSocket(handleWSMessage);

  useEffect(() => { setWsConnected(connected); }, [connected]);
  useEffect(() => { chatEndRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [chatMessages]);

  // Global resize drag handler
  useEffect(() => {
    if (!resizing) return;
    const onMove = (e: MouseEvent) => {
      switch (resizing.panel) {
        case 'sidebar':
          setSidebarWidth(Math.max(160, Math.min(500, resizing.startSize + (e.clientX - resizing.startPos))));
          break;
        case 'right':
          setRightWidth(Math.max(200, Math.min(600, resizing.startSize - (e.clientX - resizing.startPos))));
          break;
        case 'bottom':
          setBottomHeight(Math.max(100, Math.min(500, resizing.startSize - (e.clientY - resizing.startPos))));
          break;
      }
    };
    const onUp = () => setResizing(null);
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    document.body.style.cursor = resizing.panel === 'bottom' ? 'row-resize' : 'col-resize';
    document.body.style.userSelect = 'none';
    return () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
  }, [resizing]);

  // Filter resources by search query
  const filteredResources = searchQuery
    ? catalogResources.filter(r =>
        r.label.toLowerCase().includes(searchQuery.toLowerCase()) ||
        r.type.toLowerCase().includes(searchQuery.toLowerCase()) ||
        r.category.toLowerCase().includes(searchQuery.toLowerCase()))
    : catalogResources;
  const filteredCategories = [...new Set(filteredResources.map(r => r.category))];

  // Fetch resource catalog from backend when tool changes
  useEffect(() => {
    if (!tool) return;
    api.getCatalog(tool).then(cat => {
      setCatalogResources(cat.resources || []);
    }).catch(() => {
      setCatalogResources(FALLBACK_RESOURCES);
    });
  }, [tool]);

  // Generate code preview whenever nodes change
  useEffect(() => {
    if (!tool || !nodes.length) {
      setSyncCode(tool ? `# Add resources from the palette or use AI chat\n` : '');
      return;
    }
    const code = generateLocalCode(tool, nodes, edges);
    setSyncCode(code);
  }, [nodes, edges, tool]);

  // Sync to disk (debounced) — syncs even when nodes is empty so that
  // deleting the last resource clears the generated file on disk.
  const syncTimer = useRef<ReturnType<typeof setTimeout>>();
  const hasCreatedProject = useRef(false);
  useEffect(() => {
    if (!tool || !hasCreatedProject.current) return;
    clearTimeout(syncTimer.current);
    syncTimer.current = setTimeout(() => {
      api.syncToDisk(projectId, tool, nodes).catch(() => {});
    }, 1000);
  }, [nodes, tool, projectId]);

  // ─── Handlers ───

  const addNode = useCallback((resourceDef: any) => {
    const node = {
      id: uid(),
      type: resourceDef.type,
      name: resourceDef.type.replace(/^(aws_|google_|azurerm_)/, '').replace(/^compute_|^container_/, ''),
      label: resourceDef.label,
      icon: resourceDef.icon,
      properties: { ...(resourceDef.defaults || {}) },
      x: 100 + Math.random() * 280,
      y: 80 + Math.random() * 180,
    };
    setNodes(prev => {
      // Auto-connect: check if this resource type has ConnectsVia rules
      const catEntry = catalogResources.find(c => c.type === resourceDef.type);
      if (catEntry?.connects_via) {
        const newEdges: Edge[] = [];
        for (const [field, targetType] of Object.entries(catEntry.connects_via)) {
          // Find existing nodes of the target type
          const target = prev.find(n => n.type === targetType);
          if (target) {
            newEdges.push({
              id: edgeId(node.id, target.id, field),
              from: node.id,
              to: target.id,
              fromType: node.type,
              toType: target.type,
              field,
              label: field.replace(/_/g, ' '),
            });
          }
        }
        if (newEdges.length > 0) {
          setEdges(prevEdges => [...prevEdges, ...newEdges]);
        }
      }
      return [...prev, node];
    });
    setSelectedNode(node.id);
  }, [catalogResources]);

  const removeNode = useCallback((id: string) => {
    setNodes(prev => prev.filter(n => n.id !== id));
    setEdges(prev => prev.filter(e => e.from !== id && e.to !== id));
    setSelectedNode(prev => prev === id ? null : prev);
    setSelectedEdge(prev => {
      // Clear selected edge if it involved the removed node
      const edge = edges.find(e => e.id === prev);
      return edge && (edge.from === id || edge.to === id) ? null : prev;
    });
  }, [edges]);

  const updateProp = useCallback((id: string, key: string, value: any) => {
    setNodes(prev => prev.map(n => n.id === id ? { ...n, properties: { ...n.properties, [key]: value } } : n));
  }, []);

  const updateName = useCallback((id: string, name: string) => {
    setNodes(prev => prev.map(n => n.id === id ? { ...n, name } : n));
  }, []);

  const onMouseDown = (e: React.MouseEvent, nodeId: string) => {
    e.stopPropagation();
    const rect = canvasRef.current!.getBoundingClientRect();
    const node = nodes.find(n => n.id === nodeId)!;
    setDragging({ id: nodeId, ox: e.clientX - rect.left - node.x, oy: e.clientY - rect.top - node.y });
    setSelectedNode(nodeId);
  };

  const onMouseMove = (e: React.MouseEvent) => {
    // Update connection drag preview
    if (connecting) {
      const rect = canvasRef.current!.getBoundingClientRect();
      setConnecting(prev => prev ? { ...prev, x: e.clientX - rect.left, y: e.clientY - rect.top } : null);
    }
    if (!dragging) return;
    const rect = canvasRef.current!.getBoundingClientRect();
    const x = Math.max(0, e.clientX - rect.left - dragging.ox);
    const y = Math.max(0, e.clientY - rect.top - dragging.oy);
    setNodes(prev => prev.map(n => n.id === dragging.id ? { ...n, x, y } : n));
  };

  const onMouseUp = () => setDragging(null);

  // Detect the dominant cloud provider from canvas nodes
  const detectProvider = useCallback((): string => {
    const counts: Record<string, number> = { aws: 0, google: 0, azurerm: 0 };
    nodes.forEach(n => {
      if (n.type.startsWith('aws_')) counts.aws++;
      else if (n.type.startsWith('google_')) counts.google++;
      else if (n.type.startsWith('azurerm_')) counts.azurerm++;
    });
    // Also check chat history for provider hints
    const chatText = chatMessages.map(m => m.text).join(' ').toLowerCase();
    if (chatText.includes('azure') || chatText.includes('azurerm')) counts.azurerm += 3;
    if (chatText.includes('gcp') || chatText.includes('google cloud')) counts.google += 3;
    if (chatText.includes('aws') || chatText.includes('amazon')) counts.aws += 3;

    const max = Math.max(counts.aws, counts.google, counts.azurerm);
    if (max === 0) return 'aws';
    if (counts.google === max) return 'google';
    if (counts.azurerm === max) return 'azurerm';
    return 'aws';
  }, [nodes, chatMessages]);

  // Fetch suggestions when canvas changes
  useEffect(() => {
    if (!tool) return;
    const provider = detectProvider();
    const canvas = nodes.map(n => ({ type: n.type, name: n.name }));
    api.suggest(tool, provider, canvas).then(setSuggestions).catch(() => {});
  }, [nodes, tool, detectProvider]);

  const handleChat = async () => {
    if (!chatInput.trim() || !tool) return;
    const input = chatInput;
    setChatInput('');
    setChatMessages(prev => [...prev, { role: 'user', text: input }]);
    setChatLoading(true);

    try {
      const provider = detectProvider();
      const result = await api.chat({
        message: input,
        tool,
        provider,
        history: chatMessages.map(m => ({ role: m.role === 'ai' ? 'ai' : 'user', content: m.text })),
        canvas: nodes.map(n => ({ type: n.type, name: n.name })),
      });
      setChatMessages(prev => [...prev, { role: 'ai', text: result.message }]);
      if (result.suggestions) setSuggestions(result.suggestions);
      if (result.resources) {
        result.resources.forEach(r => {
          const meta = catalogResources.find(def => def.type === r.type);
          addNode({
            type: r.type,
            label: meta?.label ?? r.type,
            icon: meta?.icon ?? '📦',
            defaults: r.properties,
          });
        });
      }
    } catch {
      setChatMessages(prev => [...prev, { role: 'ai', text: 'AI is unavailable. Make sure Ollama is running.' }]);
    }
    setChatLoading(false);
  };

  const runCmd = (command: string) => {
    if (!tool) return;
    // apply/destroy require explicit confirmation
    const needsApproval = command === 'apply' || command === 'destroy';
    if (needsApproval && !confirm(`Are you sure you want to run "${command}"? This will modify real infrastructure.`)) {
      return;
    }
    setTerminalOutput(prev => [...prev, `$ ${command}`, '']);
    api.runCommand(projectId, tool, command, needsApproval).catch(err => {
      setTerminalOutput(prev => [...prev, `Error: ${err.message}`]);
    });
  };

  const handleCreateProject = async (selectedTool: string) => {
    setTool(selectedTool);
    // Lock the project ID at creation time so renaming the display input
    // can't silently redirect API calls to a different directory.
    setProjectId(projectName);
    hasCreatedProject.current = true;
    try {
      await api.createProject(projectName, selectedTool);
    } catch {
      // Backend might not be running, continue with local-only mode
    }
  };

  // ─── Tool Selection ───
  if (!tool) {
    return (
      <div style={S.selectScreen}>
        <div style={S.selectBg} />
        <div style={S.selectContent}>
          <div style={S.logo}><span style={{ fontSize: 28, color: '#7B42F6' }}>◆</span> <span style={S.logoText}>IaC Studio</span></div>
          <h1 style={S.title}>Choose your IaC tool</h1>
          <p style={S.subtitle}>Visual infrastructure builder with AI-powered assistance</p>
          <div style={S.cardGrid}>
            {Object.entries(TOOLS).map(([key, t]) => {
              const detected = detectedTools.find(d => d.name === t.name);
              return (
                <button key={key} style={{ ...S.card, borderColor: t.color + '33' }}
                  onClick={() => handleCreateProject(key)}
                  onMouseEnter={e => { (e.currentTarget as any).style.borderColor = t.color; (e.currentTarget as any).style.transform = 'translateY(-4px)'; }}
                  onMouseLeave={e => { (e.currentTarget as any).style.borderColor = t.color + '33'; (e.currentTarget as any).style.transform = 'translateY(0)'; }}>
                  <span style={{ fontSize: 40 }}>{t.icon}</span>
                  <span style={{ fontSize: 18, fontWeight: 600, color: t.color }}>{t.name}</span>
                  <span style={{ fontSize: 12, color: '#555', fontFamily: 'JetBrains Mono' }}>{t.ext} files</span>
                  {detected && (
                    <span style={{ fontSize: 10, color: detected.available ? '#4ade80' : '#666', marginTop: 4 }}>
                      {detected.available ? `✓ ${detected.version?.slice(0, 30)}` : '✗ Not installed'}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
          <div style={S.features}>
            {['Visual drag-and-drop builder', 'AI chat to generate resources', 'Real-time code generation', 'Files editable on disk'].map(f => (
              <div key={f} style={{ fontSize: 13, color: '#555', display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ fontSize: 8, color: '#7B42F6' }}>●</span> {f}
              </div>
            ))}
          </div>

          {/* Project name & directory */}
          <div style={{ marginTop: 32, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 12 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <label style={{ fontSize: 12, color: '#555', fontFamily: 'JetBrains Mono' }}>Project:</label>
              <input style={{ background: '#111120', border: '1px solid #2a2a3e', borderRadius: 8, padding: '8px 14px', color: '#ccc', fontSize: 13, fontFamily: 'JetBrains Mono', outline: 'none', width: 200 }}
                value={projectName} onChange={e => setProjectName(e.target.value)} placeholder="my-infra-project" />
            </div>
            <div style={{ fontSize: 11, color: '#444', fontFamily: 'JetBrains Mono' }}>
              Creates ~/iac-projects/{projectName}/
            </div>

            {/* Import existing project */}
            <div style={{ marginTop: 12, display: 'flex', gap: 10 }}>
              <button style={{ background: '#1a1a2e', border: '1px solid #2a2a3e', borderRadius: 8, padding: '8px 16px', color: '#888', fontSize: 12, cursor: 'pointer', fontFamily: 'DM Sans' }}
                onClick={async () => {
                  const name = prompt('Enter existing project directory name (under ~/iac-projects/):');
                  if (!name) return;
                  setProjectName(name);
                  // Try to load and parse the existing project
                  const firstTool = detectedTools.find(t => t.available);
                  const toolKey = firstTool?.name === 'OpenTofu' ? 'opentofu' : firstTool?.name === 'Ansible' ? 'ansible' : 'terraform';
                  setProjectId(name);
                  setTool(toolKey);
                  hasCreatedProject.current = true;
                  try {
                    const resources = await api.getResources(name, toolKey);
                    if (resources && resources.length > 0) {
                      setNodes(resources.map((r, i) => ({
                        ...r,
                        x: 80 + (i % 4) * 200,
                        y: 80 + Math.floor(i / 4) * 120,
                        icon: catalogResources.find(c => c.type === r.type)?.icon ?? '📦',
                        label: catalogResources.find(c => c.type === r.type)?.label ?? r.type,
                      })));
                      setNotification(`Imported ${resources.length} resources from ${name}`);
                      setTimeout(() => setNotification(null), 4000);
                    }
                  } catch {
                    // Project might not exist yet, that's ok
                  }
                }}>
                📂 Import Existing Project
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  const ct = TOOLS[tool];
  const selected = nodes.find(n => n.id === selectedNode);
  const categories = [...new Set(catalogResources.map((r: any) => r.category))];

  // ─── Main UI ───
  return (
    <div style={S.app}>
      {/* Notification */}
      {notification && (
        <div style={S.notification}>{notification}</div>
      )}

      {/* Header */}
      <header style={{ ...S.header, borderBottomColor: ct.color + '44' }}>
        <div style={S.hLeft}>
          <button style={S.backBtn} onClick={() => { setTool(null); setNodes([]); setChatMessages([]); setTerminalOutput([]); }}>←</button>
          <span style={{ ...S.badge, background: ct.color + '22', color: ct.color }}>{ct.icon} {ct.name}</span>
          <input style={S.projInput} value={projectName} onChange={e => setProjectName(e.target.value)} />
          <span style={{ fontSize: 10, color: wsConnected ? '#4ade80' : '#ef4444' }}>{wsConnected ? '● live' : '● offline'}</span>
        </div>
        <div style={S.hRight}>
          <span style={S.count}>{nodes.length} resource{nodes.length !== 1 ? 's' : ''}</span>
          <button style={{ ...S.cmd, background: ct.color + '22', color: ct.color }}
            onClick={() => runCmd(tool === 'ansible' ? 'check' : 'init')}>
            {tool === 'ansible' ? '▶ Check' : '▶ Init'}
          </button>
          <button style={{ ...S.cmd, background: ct.color + '22', color: ct.color }}
            onClick={() => runCmd(tool === 'ansible' ? 'syntax' : 'plan')}>
            {tool === 'ansible' ? '▶ Syntax' : '▶ Plan'}
          </button>
          <button style={{ ...S.cmd, background: ct.color, color: '#0a0a0f' }}
            onClick={() => runCmd(tool === 'ansible' ? 'playbook' : 'apply')}>
            ▶ Apply
          </button>
        </div>
      </header>

      <div style={S.main}>
        {/* Sidebar — resizable */}
        <aside style={{ ...S.sidebar, width: sidebarWidth }}>
          <div style={S.tabs}>
            {[
              { key: 'palette', label: 'Resources' },
              { key: 'suggest', label: 'Next' },
              { key: 'guide', label: 'Guide' },
            ].map(t => (
              <button key={t.key} style={{ ...S.tab, ...(activePanel === t.key ? { color: ct.color, borderBottomColor: ct.color } : {}), fontSize: 10 }}
                onClick={() => setActivePanel(t.key)}>
                {t.label}
                {t.key === 'suggest' && suggestions.length > 0 && (
                  <span style={{ marginLeft: 4, background: ct.color + '33', color: ct.color, padding: '1px 5px', borderRadius: 8, fontSize: 9 }}>{suggestions.length}</span>
                )}
              </button>
            ))}
          </div>
          {activePanel === 'palette' && (
            <>
              {/* Search */}
              <div style={{ padding: '8px 10px', borderBottom: '1px solid #1a1a2e' }}>
                <input
                  style={{ ...S.finput, fontSize: 12, padding: '6px 10px', background: '#0a0a14' }}
                  placeholder="Search resources..."
                  value={searchQuery}
                  onChange={e => setSearchQuery(e.target.value)}
                />
                {searchQuery && (
                  <div style={{ fontSize: 10, color: '#555', marginTop: 4, fontFamily: 'JetBrains Mono' }}>
                    {filteredResources.length} result{filteredResources.length !== 1 ? 's' : ''}
                  </div>
                )}
              </div>
              <div style={S.palScroll}>
                {filteredCategories.map(cat => (
                  <div key={cat}>
                    <div style={S.catTitle}>{cat}</div>
                    {filteredResources.filter((r: any) => r.category === cat).map((r: any) => (
                      <button key={r.type} style={S.palItem} onClick={() => addNode(r)}
                        onMouseEnter={e => {
                          (e.currentTarget as any).style.background = '#1a1a2e';
                          const rect = e.currentTarget.getBoundingClientRect();
                          setHoverPos({ x: rect.right + 8, y: rect.top });
                          setHoveredResource(r);
                        }}
                        onMouseLeave={e => {
                          (e.currentTarget as any).style.background = 'transparent';
                          setHoveredResource(null);
                        }}>
                        <span>{r.icon}</span>
                        <span style={{ flex: 1 }}>{r.label}</span>
                        <span style={{ color: '#444' }}>+</span>
                      </button>
                    ))}
                  </div>
                ))}
                {filteredResources.length === 0 && searchQuery && (
                  <div style={{ padding: '20px 16px', color: '#444', fontSize: 12, textAlign: 'center' }}>
                    No resources matching "{searchQuery}"
                  </div>
                )}
              </div>
            </>
          )}
          {activePanel === 'files' && (
            <div style={{ padding: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12, fontFamily: 'JetBrains Mono' }}>📁 {projectName}/</div>
              {['main' + ct.ext, 'variables' + ct.ext, 'outputs' + ct.ext, '.gitignore'].map(f => (
                <div key={f} style={{ fontSize: 12, color: '#777', padding: '5px 0 5px 12px', fontFamily: 'JetBrains Mono', cursor: 'pointer' }}>📄 {f}</div>
              ))}
              <div style={{ marginTop: 24, padding: 12, background: '#111122', borderRadius: 8, fontSize: 11, color: '#555', lineHeight: 1.6 }}>
                Files sync to:<br /><code style={{ color: ct.color, fontFamily: 'JetBrains Mono' }}>~/{projectName}/</code>
              </div>
            </div>
          )}
          {/* Suggestions panel */}
          {activePanel === 'suggest' && (
            <div style={S.palScroll}>
              {suggestions.length === 0 ? (
                <div style={{ padding: 20, textAlign: 'center', color: '#555', fontSize: 12 }}>
                  Add resources to get smart suggestions based on IaC best practices.
                </div>
              ) : (
                suggestions.map(s => {
                  const meta = catalogResources.find(c => c.type === s.type);
                  return (
                    <button key={s.type} style={{ ...S.palItem, flexDirection: 'column' as const, alignItems: 'flex-start', gap: 4, padding: '10px 16px' }}
                      onClick={() => meta && addNode(meta)}
                      onMouseEnter={e => { (e.currentTarget as any).style.background = '#1a1a2e'; }}
                      onMouseLeave={e => { (e.currentTarget as any).style.background = 'transparent'; }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%' }}>
                        <span>{meta?.icon ?? '📦'}</span>
                        <span style={{ flex: 1, fontWeight: 600, color: '#ddd' }}>{s.label}</span>
                        <span style={{ color: s.priority === 1 ? ct.color : s.priority === 2 ? '#888' : '#555', fontSize: 9, fontFamily: 'JetBrains Mono' }}>
                          {s.priority === 1 ? 'NEXT' : s.priority === 2 ? 'RECOMMENDED' : 'OPTIONAL'}
                        </span>
                      </div>
                      <div style={{ fontSize: 11, color: '#666', lineHeight: 1.4, paddingLeft: 28 }}>{s.reason}</div>
                    </button>
                  );
                })
              )}
            </div>
          )}
          {/* Guide panel */}
          {activePanel === 'guide' && (
            <div style={{ ...S.palScroll, padding: 16 }}>
              <div style={{ fontSize: 14, fontWeight: 700, color: '#ddd', marginBottom: 16 }}>Getting Started</div>
              {[
                { step: '1', title: 'Add a foundation', desc: tool === 'ansible' ? 'Start with package installation (apt/yum)' : detectProvider() === 'google' ? 'Start with a VPC Network (google_compute_network)' : detectProvider() === 'azurerm' ? 'Start with a Resource Group (azurerm_resource_group)' : 'Start with a VPC (aws_vpc)' },
                { step: '2', title: 'Build networking', desc: tool === 'ansible' ? 'Configure services and users' : 'Add subnets, security groups, and routing' },
                { step: '3', title: 'Add compute', desc: tool === 'ansible' ? 'Deploy application files and templates' : 'Deploy VMs, containers, or serverless functions' },
                { step: '4', title: 'Add data layer', desc: tool === 'ansible' ? 'Configure databases and cron jobs' : 'Add databases, caches, and storage buckets' },
                { step: '5', title: 'Secure & monitor', desc: tool === 'ansible' ? 'Configure firewall and enable services' : 'Add IAM roles, encryption keys, and alarms' },
              ].map(g => (
                <div key={g.step} style={{ display: 'flex', gap: 12, marginBottom: 14 }}>
                  <div style={{ width: 24, height: 24, borderRadius: '50%', background: ct.color + '22', color: ct.color, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 700, flexShrink: 0 }}>{g.step}</div>
                  <div>
                    <div style={{ fontSize: 12, fontWeight: 600, color: '#bbb' }}>{g.title}</div>
                    <div style={{ fontSize: 11, color: '#666', marginTop: 2 }}>{g.desc}</div>
                  </div>
                </div>
              ))}
              <div style={{ marginTop: 20, padding: 12, background: '#111122', borderRadius: 8, fontSize: 11, color: '#666', lineHeight: 1.6 }}>
                <div style={{ fontWeight: 600, color: '#888', marginBottom: 6 }}>Tips</div>
                <div>Drag the <span style={{ color: ct.color }}>circle port</span> on a node to connect it to another resource.</div>
                <div style={{ marginTop: 4 }}>Use the <span style={{ color: ct.color }}>AI chat</span> below to describe what you need in plain language.</div>
                <div style={{ marginTop: 4 }}>Check the <span style={{ color: ct.color }}>Next</span> tab for smart suggestions based on what's on your canvas.</div>
                <div style={{ marginTop: 4 }}>The code preview on the right updates live as you build.</div>
              </div>
              <button style={{ ...S.cmd, background: ct.color + '22', color: ct.color, width: '100%', marginTop: 16, padding: '8px 0' }}
                onClick={() => setActivePanel('suggest')}>
                View Suggestions →
              </button>
            </div>
          )}
        </aside>
        {/* Sidebar resize handle */}
        <div style={{ width: 4, cursor: 'col-resize', background: resizing?.panel === 'sidebar' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
          onMouseDown={e => setResizing({ panel: 'sidebar', startPos: e.clientX, startSize: sidebarWidth })}
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = '#2a2a3e'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />

        {/* Canvas */}
        <main style={S.canvas} ref={canvasRef} onMouseMove={onMouseMove} onMouseUp={(e) => {
          onMouseUp(e);
          // Finish manual connection if dragging to empty space
          if (connecting) setConnecting(null);
        }} onMouseLeave={() => { onMouseUp(null as any); setConnecting(null); }}
          onClick={() => { setSelectedNode(null); setSelectedEdge(null); }}>
          <div style={S.grid} />

          {/* SVG layer for connection lines */}
          <svg style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', zIndex: 1 }}>
            <defs>
              <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
                <polygon points="0 0, 10 3.5, 0 7" fill={ct.color} opacity="0.6" />
              </marker>
              <marker id="arrowhead-hover" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
                <polygon points="0 0, 10 3.5, 0 7" fill={ct.color} />
              </marker>
            </defs>
            {edges.map(edge => {
              const fromNode = nodes.find(n => n.id === edge.from);
              const toNode = nodes.find(n => n.id === edge.to);
              if (!fromNode || !toNode) return null;
              const NODE_W = 180, NODE_H = 70;
              const x1 = fromNode.x + NODE_W / 2;
              const y1 = fromNode.y + NODE_H;
              const x2 = toNode.x + NODE_W / 2;
              const y2 = toNode.y;
              const mx = (x1 + x2) / 2;
              const my = (y1 + y2) / 2;
              const isSelected = selectedEdge === edge.id;
              // Bezier curve for smooth connections
              const path = `M ${x1} ${y1} C ${x1} ${y1 + 40}, ${x2} ${y2 - 40}, ${x2} ${y2}`;
              return (
                <g key={edge.id}>
                  {/* Invisible wider path for click target */}
                  <path d={path} fill="none" stroke="transparent" strokeWidth={12} style={{ pointerEvents: 'stroke', cursor: 'pointer' }}
                    onClick={(e) => { e.stopPropagation(); setSelectedEdge(edge.id); setSelectedNode(null); }} />
                  {/* Visible path */}
                  <path d={path} fill="none"
                    stroke={isSelected ? ct.color : `${ct.color}55`}
                    strokeWidth={isSelected ? 2.5 : 1.5}
                    strokeDasharray={isSelected ? 'none' : '6 4'}
                    markerEnd={isSelected ? 'url(#arrowhead-hover)' : 'url(#arrowhead)'}
                    style={{ transition: 'stroke 0.2s, stroke-width 0.2s' }} />
                  {/* Field label on the line */}
                  <text x={mx} y={my - 6} textAnchor="middle" fill={isSelected ? ct.color : '#555'}
                    fontSize={9} fontFamily="JetBrains Mono" style={{ pointerEvents: 'none' }}>
                    {edge.field}
                  </text>
                </g>
              );
            })}
            {/* Connection drag preview line */}
            {connecting && (
              <line x1={nodes.find(n => n.id === connecting.fromId)!.x + 90}
                y1={nodes.find(n => n.id === connecting.fromId)!.y + 70}
                x2={connecting.x} y2={connecting.y}
                stroke={ct.color} strokeWidth={2} strokeDasharray="4 4" opacity={0.6} />
            )}
          </svg>

          {nodes.length === 0 && (
            <div style={S.empty}>
              <div style={{ fontSize: 48, opacity: 0.3, marginBottom: 16 }}>◇</div>
              <div style={{ fontSize: 16, opacity: 0.4 }}>Drag resources from the palette</div>
              <div style={{ fontSize: 14, opacity: 0.3, marginTop: 4 }}>or use AI chat below</div>
            </div>
          )}
          {nodes.map(node => {
            const nodeEdges = edges.filter(e => e.from === node.id || e.to === node.id);
            const hasConnections = nodeEdges.length > 0;
            return (
            <div key={node.id}
              style={{ ...S.node, left: node.x, top: node.y, zIndex: 2,
                borderColor: selectedNode === node.id ? ct.color : hasConnections ? `${ct.color}44` : '#2a2a3e',
                boxShadow: selectedNode === node.id ? `0 0 20px ${ct.color}33` : '0 4px 12px rgba(0,0,0,0.3)' }}
              onMouseDown={e => onMouseDown(e, node.id)}
              onClick={e => { e.stopPropagation(); setSelectedNode(node.id); setSelectedEdge(null); }}
              onMouseUp={() => {
                // Complete manual connection
                if (connecting && connecting.fromId !== node.id) {
                  const fromNode = nodes.find(n => n.id === connecting.fromId);
                  if (fromNode) {
                    // Find a valid ConnectsVia field for this pair
                    const catEntry = catalogResources.find(c => c.type === fromNode.type);
                    let field = 'depends_on';
                    if (catEntry?.connects_via) {
                      const match = Object.entries(catEntry.connects_via).find(([, t]) => t === node.type);
                      if (match) field = match[0];
                    }
                    const newEdge: Edge = {
                      id: edgeId(connecting.fromId, node.id, field),
                      from: connecting.fromId, to: node.id,
                      fromType: fromNode.type, toType: node.type,
                      field, label: field.replace(/_/g, ' '),
                    };
                    setEdges(prev => {
                      if (prev.some(e => e.from === newEdge.from && e.to === newEdge.to && e.field === newEdge.field)) return prev;
                      return [...prev, newEdge];
                    });
                  }
                  setConnecting(null);
                }
              }}>
              <div style={S.nodeHead}>
                <span style={{ fontSize: 18 }}>{node.icon}</span>
                <span style={{ fontSize: 13, fontWeight: 600, color: '#ddd', flex: 1 }}>{node.label}</span>
                {hasConnections && <span style={{ fontSize: 9, color: ct.color, fontFamily: 'JetBrains Mono' }}>{nodeEdges.length}</span>}
                <button style={S.nodeDel} onClick={e => { e.stopPropagation(); removeNode(node.id); }}>×</button>
              </div>
              <div style={{ fontSize: 10, color: '#555', padding: '0 12px', fontFamily: 'JetBrains Mono' }}>{node.type}</div>
              <div style={{ display: 'flex', justifyContent: 'space-between', padding: '4px 12px 8px' }}>
                <span style={{ fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono' }}>{node.name}</span>
                {/* Connection port — drag from here to another node */}
                <div style={{ width: 14, height: 14, borderRadius: '50%', border: `2px solid ${ct.color}55`, background: '#12121e',
                  cursor: 'crosshair', flexShrink: 0 }}
                  title="Drag to connect"
                  onMouseDown={e => {
                    e.stopPropagation();
                    const rect = canvasRef.current!.getBoundingClientRect();
                    setConnecting({ fromId: node.id, x: e.clientX - rect.left, y: e.clientY - rect.top });
                  }} />
              </div>
            </div>
          );})}
        </main>

        {/* Right Panel */}
        {/* Right panel resize handle */}
        <div style={{ width: 4, cursor: 'col-resize', background: resizing?.panel === 'right' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
          onMouseDown={e => setResizing({ panel: 'right', startPos: e.clientX, startSize: rightWidth })}
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = '#2a2a3e'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />
        <aside style={{ ...S.right, width: rightWidth }}>
          {/* Selected edge info */}
          {selectedEdge && (() => {
            const edge = edges.find(e => e.id === selectedEdge);
            if (!edge) return null;
            const fromNode = nodes.find(n => n.id === edge.from);
            const toNode = nodes.find(n => n.id === edge.to);
            return (
              <div style={S.props}>
                <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12 }}>🔗 Connection</div>
                <div style={S.field}>
                  <label style={S.flabel}>From</label>
                  <div style={{ fontSize: 12, color: '#aaa', fontFamily: 'JetBrains Mono' }}>{fromNode?.icon} {fromNode?.type}.{fromNode?.name}</div>
                </div>
                <div style={S.field}>
                  <label style={S.flabel}>To</label>
                  <div style={{ fontSize: 12, color: '#aaa', fontFamily: 'JetBrains Mono' }}>{toNode?.icon} {toNode?.type}.{toNode?.name}</div>
                </div>
                <div style={S.field}>
                  <label style={S.flabel}>Via Field</label>
                  <div style={{ fontSize: 12, color: ct.color, fontFamily: 'JetBrains Mono' }}>{edge.field}</div>
                </div>
                <div style={S.field}>
                  <label style={S.flabel}>Generated Reference</label>
                  <div style={{ fontSize: 11, color: '#888', fontFamily: 'JetBrains Mono', background: '#111120', padding: '6px 8px', borderRadius: 4 }}>
                    {edge.field} = {toNode?.type}.{toNode?.name}.id
                  </div>
                </div>
                <button style={{ ...S.cmd, background: '#ef444433', color: '#ef4444', width: '100%', marginTop: 8 }}
                  onClick={() => { setEdges(prev => prev.filter(e => e.id !== selectedEdge)); setSelectedEdge(null); }}>
                  Delete Connection
                </button>
              </div>
            );
          })()}
          {/* Selected node properties */}
          {selected && !selectedEdge && (
            <div style={S.props}>
              <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12 }}>{selected.icon} Properties</div>
              <div style={S.field}>
                <label style={S.flabel}>Name</label>
                <input style={S.finput} value={selected.name} onChange={e => updateName(selected.id, e.target.value)} />
              </div>
              {Object.entries(selected.properties).map(([k, v]) => (
                <div key={k} style={S.field}>
                  <label style={S.flabel}>{k}</label>
                  {typeof v === 'boolean' ? (
                    <button style={{ ...S.ftoggle, background: v ? ct.color + '33' : '#1a1a2e', color: v ? ct.color : '#666' }}
                      onClick={() => updateProp(selected.id, k, !v)}>
                      {v ? 'true' : 'false'}
                    </button>
                  ) : (
                    <input style={S.finput} value={String(v)} onChange={e => updateProp(selected.id, k, e.target.value)} />
                  )}
                </div>
              ))}
              {/* Show connections for this node */}
              {(() => {
                const nodeEdges = edges.filter(e => e.from === selected.id || e.to === selected.id);
                if (nodeEdges.length === 0) return null;
                return (
                  <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid #1a1a2e' }}>
                    <label style={S.flabel}>Connections ({nodeEdges.length})</label>
                    {nodeEdges.map(e => {
                      const other = nodes.find(n => n.id === (e.from === selected.id ? e.to : e.from));
                      const direction = e.from === selected.id ? '→' : '←';
                      return (
                        <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 0', fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono', cursor: 'pointer' }}
                          onClick={() => setSelectedEdge(e.id)}>
                          <span>{direction}</span>
                          <span>{other?.icon}</span>
                          <span style={{ color: '#aaa' }}>{other?.name}</span>
                          <span style={{ color: ct.color, marginLeft: 'auto', fontSize: 9 }}>{e.field}</span>
                        </div>
                      );
                    })}
                  </div>
                );
              })()}
            </div>
          )}
          <div style={S.codePanel}>
            <div style={S.codeHead}>
              <span>📄 main{ct.ext}</span>
              <button style={{ ...S.copyBtn, color: ct.color }}
                onClick={() => navigator.clipboard?.writeText(syncCode)}>Copy</button>
            </div>
            <pre style={S.codePre}>{syncCode || '# Add resources to see generated code\n'}</pre>
          </div>
        </aside>
      </div>

      {/* Bottom: Chat + Terminal */}
      {/* Bottom panel resize handle */}
      <div style={{ height: 4, cursor: 'row-resize', background: resizing?.panel === 'bottom' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
        onMouseDown={e => setResizing({ panel: 'bottom', startPos: e.clientY, startSize: bottomHeight })}
        onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = '#2a2a3e'; }}
        onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />
      <div style={{ ...S.bottom, height: bottomHeight }}>
        <div style={S.chat}>
          <div style={S.chatHead}>
            <span style={{ fontSize: 14, color: '#7B42F6' }}>✦</span>
            <span>AI Assistant</span>
            <span style={S.chatBadge}>Ollama</span>
          </div>
          <div style={S.chatMsgs}>
            {chatMessages.length === 0 && (
              <div style={{ padding: '8px 0', color: '#888', fontSize: 13 }}>
                <p style={{ margin: 0 }}>Ask me to create infrastructure:</p>
                <p style={{ margin: '4px 0 0', color: '#555', fontSize: 12 }}>"Add a VPC" · "Create an RDS database" · "I need an S3 bucket"</p>
              </div>
            )}
            {chatMessages.map((m, i) => (
              <div key={i} style={{ padding: '6px 0', fontSize: 13, display: 'flex', gap: 8, color: m.role === 'ai' ? '#999' : '#ccc' }}>
                {m.role === 'ai' && <span style={{ color: ct.color, fontWeight: 700, flexShrink: 0 }}>✦</span>}
                <span>{m.text}</span>
              </div>
            ))}
            {chatLoading && <div style={{ padding: '6px 0', fontSize: 13, color: '#666' }}>✦ Thinking...</div>}
            <div ref={chatEndRef} />
          </div>
          <div style={S.chatInputRow}>
            <input style={S.chatInput} value={chatInput} onChange={e => setChatInput(e.target.value)}
              placeholder="Describe infrastructure you need..."
              onKeyDown={e => e.key === 'Enter' && handleChat()} disabled={chatLoading} />
            <button style={{ ...S.chatSend, background: ct.color }} onClick={handleChat} disabled={chatLoading}>↑</button>
          </div>
        </div>

        <div style={S.term}>
          <div style={S.termHead}>
            <span>⬛ Terminal</span>
            <button style={S.termClear} onClick={() => setTerminalOutput([])}>Clear</button>
          </div>
          <div style={S.termContent}>
            {terminalOutput.length === 0 && <span style={{ color: '#444' }}>Run init, plan, or apply to see output...</span>}
            {terminalOutput.map((line, i) => (
              <div key={i} style={{ color: line.startsWith('✓') || line.includes('Apply complete') ? '#4ade80' :
                line.startsWith('$') ? ct.color : line.startsWith('  +') ? '#60a5fa' :
                line.startsWith('Error') || line.startsWith('ERROR') ? '#ef4444' : '#999' }}>
                {line || '\u00A0'}
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Resource hover tooltip */}
      {hoveredResource && (
        <div style={{
          position: 'fixed', left: hoverPos.x, top: hoverPos.y,
          background: '#16162a', border: '1px solid #2a2a4e', borderRadius: 10,
          padding: '12px 16px', zIndex: 1000, maxWidth: 300, minWidth: 220,
          boxShadow: '0 8px 24px rgba(0,0,0,0.5)', pointerEvents: 'none',
          fontFamily: 'DM Sans',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <span style={{ fontSize: 20 }}>{hoveredResource.icon}</span>
            <div>
              <div style={{ fontSize: 13, fontWeight: 600, color: '#e0e0f0' }}>{hoveredResource.label}</div>
              <div style={{ fontSize: 10, color: '#666', fontFamily: 'JetBrains Mono' }}>{hoveredResource.type}</div>
            </div>
          </div>
          {hoveredResource.provider && (
            <div style={{ fontSize: 10, color: '#888', marginBottom: 6 }}>
              Provider: <span style={{ color: ct.color }}>{hoveredResource.provider}</span>
            </div>
          )}
          {hoveredResource.fields && hoveredResource.fields.length > 0 && (
            <div style={{ marginBottom: 6 }}>
              <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Fields</div>
              {hoveredResource.fields.slice(0, 6).map(f => (
                <div key={f.name} style={{ fontSize: 11, color: '#999', display: 'flex', gap: 4, lineHeight: 1.6, fontFamily: 'JetBrains Mono' }}>
                  <span style={{ color: f.required ? '#ef4444' : '#555' }}>{f.required ? '*' : ' '}</span>
                  <span style={{ color: '#aaa' }}>{f.name}</span>
                  <span style={{ color: '#555', marginLeft: 'auto' }}>{f.type}</span>
                </div>
              ))}
              {hoveredResource.fields.length > 6 && (
                <div style={{ fontSize: 10, color: '#444', marginTop: 2 }}>+{hoveredResource.fields.length - 6} more</div>
              )}
            </div>
          )}
          {hoveredResource.connects_via && Object.keys(hoveredResource.connects_via).length > 0 && (
            <div>
              <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Connects To</div>
              {Object.entries(hoveredResource.connects_via).map(([field, target]) => (
                <div key={field} style={{ fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono', lineHeight: 1.6 }}>
                  <span style={{ color: ct.color }}>{field}</span> → <span style={{ color: '#aaa' }}>{target}</span>
                </div>
              ))}
            </div>
          )}
          {hoveredResource.defaults && Object.keys(hoveredResource.defaults).length > 0 && (
            <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid #1e1e30' }}>
              <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Defaults</div>
              {Object.entries(hoveredResource.defaults).slice(0, 4).map(([k, v]) => (
                <div key={k} style={{ fontSize: 10, color: '#666', fontFamily: 'JetBrains Mono', lineHeight: 1.5 }}>
                  {k}: <span style={{ color: '#888' }}>{String(v)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// Local code generation (mirrors the Go backend for instant preview)
function generateLocalCode(tool: string, nodes: any[], edges: Edge[]): string {
  if (tool === 'ansible') {
    let c = '---\n- name: IaC Studio Playbook\n  hosts: all\n  become: true\n  tasks:\n';
    nodes.forEach(n => {
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

  // Determine provider from resource types
  const hasGCP = nodes.some(n => n.type.startsWith('google_'));
  const hasAzure = nodes.some(n => n.type.startsWith('azurerm_'));
  const hasAWS = nodes.some(n => n.type.startsWith('aws_'));

  let c = '';
  if (hasAWS) c += 'provider "aws" {\n  region = "us-east-1"\n}\n\n';
  if (hasGCP) c += 'provider "google" {\n  project = "my-project"\n  region  = "us-central1"\n}\n\n';
  if (hasAzure) c += 'provider "azurerm" {\n  features {}\n}\n\n';

  // Build edge lookup: nodeId -> outgoing edges
  const edgesByFrom = new Map<string, Edge[]>();
  edges.forEach(e => {
    const list = edgesByFrom.get(e.from) || [];
    list.push(e);
    edgesByFrom.set(e.from, list);
  });

  // Build node lookup by id
  const nodeById = new Map(nodes.map(n => [n.id, n]));

  nodes.forEach(n => {
    const name = n.name || n.type.replace(/^(aws_|google_|azurerm_|google_compute_|google_container_)/, '');
    c += `resource "${n.type}" "${name}" {\n`;

    // Emit connection references first (e.g., vpc_id = aws_vpc.main.id)
    const nodeEdges = edgesByFrom.get(n.id) || [];
    const emittedFields = new Set<string>();
    nodeEdges.forEach(edge => {
      const target = nodeById.get(edge.to);
      if (target && edge.field !== 'depends_on') {
        const targetName = target.name || target.type.replace(/^(aws_|google_|azurerm_|google_compute_|google_container_)/, '');
        c += `  ${edge.field} = ${target.type}.${targetName}.id\n`;
        emittedFields.add(edge.field);
      }
    });

    // Emit regular properties (skip fields already emitted as references)
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

// ─── Styles ───
const S: Record<string, React.CSSProperties> = {
  selectScreen: { width: '100vw', height: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#08080e', position: 'relative', overflow: 'hidden' },
  selectBg: { position: 'absolute', inset: 0, background: 'radial-gradient(ellipse at 50% 30%, #151530 0%, #08080e 70%)' },
  selectContent: { position: 'relative', zIndex: 1, textAlign: 'center', padding: 40 },
  logo: { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 10, marginBottom: 32 },
  logoText: { fontSize: 22, fontWeight: 700, color: '#e0e0f0', fontFamily: 'JetBrains Mono', letterSpacing: 1 },
  title: { fontSize: 36, fontWeight: 700, color: '#e8e8f0', margin: '0 0 12px', letterSpacing: -0.5 },
  subtitle: { fontSize: 16, color: '#666680', margin: '0 0 40px' },
  cardGrid: { display: 'flex', gap: 20, justifyContent: 'center', marginBottom: 48 },
  card: { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', gap: 12, padding: '32px 40px', background: '#0d0d18', border: '1.5px solid', borderRadius: 16, cursor: 'pointer', transition: 'all 0.3s', fontFamily: 'DM Sans' },
  features: { display: 'flex', gap: 24, justifyContent: 'center', flexWrap: 'wrap' as const },

  app: { width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' as const, background: '#0a0a12', overflow: 'hidden', position: 'relative' as const },
  notification: { position: 'absolute' as const, top: 60, left: '50%', transform: 'translateX(-50%)', zIndex: 100, background: '#1a1a2e', border: '1px solid #3a3a5e', borderRadius: 8, padding: '8px 20px', fontSize: 12, color: '#ddd', fontFamily: 'JetBrains Mono' },
  header: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0 16px', height: 52, borderBottom: '1px solid', flexShrink: 0, background: '#0d0d16' },
  hLeft: { display: 'flex', alignItems: 'center', gap: 12 },
  hRight: { display: 'flex', alignItems: 'center', gap: 8 },
  backBtn: { background: 'none', border: '1px solid #2a2a3e', color: '#888', borderRadius: 8, padding: '4px 10px', cursor: 'pointer', fontSize: 16, fontFamily: 'DM Sans' },
  badge: { padding: '4px 12px', borderRadius: 20, fontSize: 13, fontWeight: 600, fontFamily: 'JetBrains Mono' },
  projInput: { background: 'transparent', border: 'none', color: '#d0d0e0', fontSize: 14, fontFamily: 'JetBrains Mono', fontWeight: 500, outline: 'none', width: 180 },
  count: { fontSize: 12, color: '#666', fontFamily: 'JetBrains Mono', marginRight: 8 },
  cmd: { border: 'none', borderRadius: 8, padding: '6px 14px', cursor: 'pointer', fontSize: 12, fontWeight: 600, fontFamily: 'JetBrains Mono', transition: 'all 0.2s' },

  main: { display: 'flex', flex: 1, minHeight: 0 },
  sidebar: { width: 240, borderRight: '1px solid #1a1a2e', display: 'flex', flexDirection: 'column' as const, background: '#0c0c16', flexShrink: 0 },
  tabs: { display: 'flex', borderBottom: '1px solid #1a1a2e' },
  tab: { flex: 1, padding: '10px 0', background: 'none', border: 'none', borderBottom: '2px solid transparent', color: '#666', cursor: 'pointer', fontSize: 12, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase' as const, transition: 'all 0.2s', fontFamily: 'DM Sans' },
  palScroll: { flex: 1, overflowY: 'auto' as const, padding: '8px 0' },
  catTitle: { fontSize: 10, fontWeight: 700, color: '#444', textTransform: 'uppercase' as const, letterSpacing: 1.2, padding: '8px 16px 4px', fontFamily: 'JetBrains Mono' },
  palItem: { display: 'flex', alignItems: 'center', gap: 10, width: '100%', padding: '8px 16px', background: 'transparent', border: 'none', color: '#bbb', cursor: 'pointer', fontSize: 13, fontFamily: 'DM Sans', textAlign: 'left' as const, transition: 'background 0.15s' },

  canvas: { flex: 1, position: 'relative' as const, overflow: 'hidden', cursor: 'default' },
  grid: { position: 'absolute' as const, inset: 0, backgroundImage: 'radial-gradient(circle, #1a1a2e 1px, transparent 1px)', backgroundSize: '24px 24px', opacity: 0.5 },
  empty: { position: 'absolute' as const, top: '50%', left: '50%', transform: 'translate(-50%, -50%)', textAlign: 'center' as const, color: '#555' },
  node: { position: 'absolute' as const, width: 180, background: '#12121e', border: '1.5px solid', borderRadius: 12, cursor: 'grab', userSelect: 'none' as const, transition: 'border-color 0.2s, box-shadow 0.2s' },
  nodeHead: { display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px 4px' },
  nodeDel: { background: 'none', border: 'none', color: '#555', fontSize: 18, cursor: 'pointer', padding: 0, lineHeight: 1 },

  right: { width: 300, borderLeft: '1px solid #1a1a2e', display: 'flex', flexDirection: 'column' as const, background: '#0c0c16', flexShrink: 0 },
  props: { borderBottom: '1px solid #1a1a2e', padding: 16, maxHeight: '40%', overflowY: 'auto' as const },
  field: { marginBottom: 10 },
  flabel: { fontSize: 10, color: '#555', display: 'block', marginBottom: 4, fontFamily: 'JetBrains Mono', textTransform: 'uppercase' as const, letterSpacing: 0.5 },
  finput: { width: '100%', padding: '6px 10px', background: '#111120', border: '1px solid #1e1e30', borderRadius: 6, color: '#ccc', fontSize: 12, fontFamily: 'JetBrains Mono', outline: 'none', boxSizing: 'border-box' as const },
  ftoggle: { padding: '5px 12px', borderRadius: 6, border: '1px solid #1e1e30', cursor: 'pointer', fontSize: 12, fontFamily: 'JetBrains Mono', fontWeight: 500, width: '100%' },
  codePanel: { flex: 1, display: 'flex', flexDirection: 'column' as const, minHeight: 0 },
  codeHead: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '10px 16px', fontSize: 12, fontWeight: 600, color: '#777', borderBottom: '1px solid #1a1a2e', fontFamily: 'JetBrains Mono' },
  copyBtn: { background: 'none', border: 'none', fontSize: 11, cursor: 'pointer', fontFamily: 'JetBrains Mono', fontWeight: 600 },
  codePre: { flex: 1, margin: 0, padding: 16, fontSize: 11, lineHeight: 1.7, color: '#8888aa', fontFamily: 'JetBrains Mono', overflowY: 'auto' as const },

  bottom: { display: 'flex', height: 220, borderTop: '1px solid #1a1a2e', flexShrink: 0 },
  chat: { flex: 1, display: 'flex', flexDirection: 'column' as const, borderRight: '1px solid #1a1a2e' },
  chatHead: { display: 'flex', alignItems: 'center', gap: 8, padding: '8px 16px', fontSize: 12, fontWeight: 600, color: '#aaa', borderBottom: '1px solid #1a1a2e', background: '#0c0c16' },
  chatBadge: { fontSize: 9, background: '#1a1a2e', padding: '2px 8px', borderRadius: 10, color: '#666', marginLeft: 'auto', fontFamily: 'JetBrains Mono' },
  chatMsgs: { flex: 1, overflowY: 'auto' as const, padding: '8px 16px' },
  chatInputRow: { display: 'flex', gap: 8, padding: '8px 16px', borderTop: '1px solid #1a1a2e', background: '#0c0c16' },
  chatInput: { flex: 1, padding: '8px 12px', background: '#111120', border: '1px solid #1e1e30', borderRadius: 8, color: '#ccc', fontSize: 13, fontFamily: 'DM Sans', outline: 'none' },
  chatSend: { width: 36, height: 36, borderRadius: 8, border: 'none', color: '#000', fontSize: 16, fontWeight: 700, cursor: 'pointer' },

  term: { width: 380, display: 'flex', flexDirection: 'column' as const, background: '#09090f', flexShrink: 0 },
  termHead: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 16px', fontSize: 12, fontWeight: 600, color: '#666', borderBottom: '1px solid #1a1a2e' },
  termClear: { background: 'none', border: 'none', color: '#444', fontSize: 11, cursor: 'pointer', fontFamily: 'JetBrains Mono' },
  termContent: { flex: 1, padding: '8px 16px', fontSize: 11, fontFamily: 'JetBrains Mono', lineHeight: 1.8, overflowY: 'auto' as const },
};
