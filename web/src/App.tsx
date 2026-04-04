import { useState, useCallback, useRef, useEffect } from 'react';
import { api, Resource, ToolInfo } from './api';
import { useWebSocket, WSMessage } from './useWebSocket';

// ─── Tool Definitions (UI metadata) ───
const TOOLS: Record<string, { name: string; icon: string; color: string; ext: string; resources: any[] }> = {
  terraform: {
    name: 'Terraform', icon: '⬡', color: '#7B42F6', ext: '.tf',
    resources: [
      { type: 'aws_vpc', label: 'VPC', icon: '🌐', category: 'Networking' },
      { type: 'aws_subnet', label: 'Subnet', icon: '📡', category: 'Networking' },
      { type: 'aws_instance', label: 'EC2 Instance', icon: '🖥️', category: 'Compute' },
      { type: 'aws_s3_bucket', label: 'S3 Bucket', icon: '🪣', category: 'Storage' },
      { type: 'aws_security_group', label: 'Security Group', icon: '🛡️', category: 'Security' },
      { type: 'aws_rds_instance', label: 'RDS Database', icon: '🗄️', category: 'Database' },
      { type: 'aws_lambda_function', label: 'Lambda', icon: '⚡', category: 'Compute' },
      { type: 'aws_iam_role', label: 'IAM Role', icon: '🔑', category: 'Security' },
    ],
  },
  opentofu: {
    name: 'OpenTofu', icon: '🟢', color: '#FFDA18', ext: '.tf',
    resources: [
      { type: 'aws_vpc', label: 'VPC', icon: '🌐', category: 'Networking' },
      { type: 'aws_subnet', label: 'Subnet', icon: '📡', category: 'Networking' },
      { type: 'aws_instance', label: 'EC2 Instance', icon: '🖥️', category: 'Compute' },
      { type: 'aws_s3_bucket', label: 'S3 Bucket', icon: '🪣', category: 'Storage' },
      { type: 'aws_security_group', label: 'Security Group', icon: '🛡️', category: 'Security' },
      { type: 'aws_rds_instance', label: 'RDS Database', icon: '🗄️', category: 'Database' },
    ],
  },
  ansible: {
    name: 'Ansible', icon: '🅰️', color: '#EE0000', ext: '.yml',
    resources: [
      { type: 'apt', label: 'Install Package', icon: '📦', category: 'Packages' },
      { type: 'service', label: 'Manage Service', icon: '⚙️', category: 'System' },
      { type: 'copy', label: 'Copy File', icon: '📄', category: 'Files' },
      { type: 'template', label: 'Template', icon: '📝', category: 'Files' },
      { type: 'docker_container', label: 'Docker Container', icon: '🐳', category: 'Containers' },
      { type: 'user', label: 'User Account', icon: '👤', category: 'System' },
    ],
  },
};

let _id = 0;
const uid = () => `node_${++_id}_${Date.now()}`;

export default function App() {
  const [tool, setTool] = useState<string | null>(null);
  const [detectedTools, setDetectedTools] = useState<ToolInfo[]>([]);
  const [projectName, setProjectName] = useState('my-infra-project');
  const [projectId, setProjectId] = useState(''); // immutable after creation — used for API calls
  const [nodes, setNodes] = useState<(Resource & { x: number; y: number; icon: string; label: string })[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [chatMessages, setChatMessages] = useState<{ role: string; text: string }[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [activePanel, setActivePanel] = useState('palette');
  const [terminalOutput, setTerminalOutput] = useState<string[]>([]);
  const [dragging, setDragging] = useState<{ id: string; ox: number; oy: number } | null>(null);
  const [wsConnected, setWsConnected] = useState(false);
  const [syncCode, setSyncCode] = useState('');
  const [notification, setNotification] = useState<string | null>(null);

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

  // Generate code preview whenever nodes change
  useEffect(() => {
    if (!tool || !nodes.length) {
      setSyncCode(tool ? `# Add resources from the palette or use AI chat\n` : '');
      return;
    }
    const code = generateLocalCode(tool, nodes);
    setSyncCode(code);
  }, [nodes, tool]);

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
      name: resourceDef.type.replace('aws_', ''),
      label: resourceDef.label,
      icon: resourceDef.icon,
      properties: {},
      x: 100 + Math.random() * 280,
      y: 80 + Math.random() * 180,
    };
    setNodes(prev => [...prev, node]);
    setSelectedNode(node.id);
  }, []);

  const removeNode = useCallback((id: string) => {
    setNodes(prev => prev.filter(n => n.id !== id));
    setSelectedNode(prev => prev === id ? null : prev);
  }, []);

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
    if (!dragging) return;
    const rect = canvasRef.current!.getBoundingClientRect();
    const x = Math.max(0, e.clientX - rect.left - dragging.ox);
    const y = Math.max(0, e.clientY - rect.top - dragging.oy);
    setNodes(prev => prev.map(n => n.id === dragging.id ? { ...n, x, y } : n));
  };

  const onMouseUp = () => setDragging(null);

  const handleChat = async () => {
    if (!chatInput.trim() || !tool) return;
    const input = chatInput;
    setChatInput('');
    setChatMessages(prev => [...prev, { role: 'user', text: input }]);
    setChatLoading(true);

    try {
      const result = await api.chat(input, tool);
      setChatMessages(prev => [...prev, { role: 'ai', text: result.message }]);
      if (result.resources) {
        result.resources.forEach(r => {
          const meta = TOOLS[tool]?.resources.find(def => def.type === r.type);
          const node = {
            ...r,
            id: uid(),
            icon: meta?.icon ?? '📦',
            label: meta?.label ?? r.type,
            x: 100 + Math.random() * 280,
            y: 80 + Math.random() * 180,
          };
          setNodes(prev => [...prev, node]);
        });
      }
    } catch {
      setChatMessages(prev => [...prev, { role: 'ai', text: 'AI is unavailable. Make sure Ollama is running.' }]);
    }
    setChatLoading(false);
  };

  const runCmd = (command: string) => {
    if (!tool) return;
    setTerminalOutput(prev => [...prev, `$ ${command}`, '']);
    api.runCommand(projectId, tool, command).catch(err => {
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
        </div>
      </div>
    );
  }

  const ct = TOOLS[tool];
  const selected = nodes.find(n => n.id === selectedNode);
  const categories = [...new Set(ct.resources.map((r: any) => r.category))];

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
        {/* Sidebar */}
        <aside style={S.sidebar}>
          <div style={S.tabs}>
            {['palette', 'files'].map(t => (
              <button key={t} style={{ ...S.tab, ...(activePanel === t ? { color: ct.color, borderBottomColor: ct.color } : {}) }}
                onClick={() => setActivePanel(t)}>
                {t === 'palette' ? 'Resources' : 'Files'}
              </button>
            ))}
          </div>
          {activePanel === 'palette' && (
            <div style={S.palScroll}>
              {categories.map(cat => (
                <div key={cat}>
                  <div style={S.catTitle}>{cat}</div>
                  {ct.resources.filter((r: any) => r.category === cat).map((r: any) => (
                    <button key={r.type} style={S.palItem} onClick={() => addNode(r)}
                      onMouseEnter={e => { (e.currentTarget as any).style.background = '#1a1a2e'; }}
                      onMouseLeave={e => { (e.currentTarget as any).style.background = 'transparent'; }}>
                      <span>{r.icon}</span>
                      <span style={{ flex: 1 }}>{r.label}</span>
                      <span style={{ color: '#444' }}>+</span>
                    </button>
                  ))}
                </div>
              ))}
            </div>
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
        </aside>

        {/* Canvas */}
        <main style={S.canvas} ref={canvasRef} onMouseMove={onMouseMove} onMouseUp={onMouseUp} onMouseLeave={onMouseUp}
          onClick={() => setSelectedNode(null)}>
          <div style={S.grid} />
          {nodes.length === 0 && (
            <div style={S.empty}>
              <div style={{ fontSize: 48, opacity: 0.3, marginBottom: 16 }}>◇</div>
              <div style={{ fontSize: 16, opacity: 0.4 }}>Drag resources from the palette</div>
              <div style={{ fontSize: 14, opacity: 0.3, marginTop: 4 }}>or use AI chat below</div>
            </div>
          )}
          {nodes.map(node => (
            <div key={node.id}
              style={{ ...S.node, left: node.x, top: node.y,
                borderColor: selectedNode === node.id ? ct.color : '#2a2a3e',
                boxShadow: selectedNode === node.id ? `0 0 20px ${ct.color}33` : '0 4px 12px rgba(0,0,0,0.3)' }}
              onMouseDown={e => onMouseDown(e, node.id)}
              onClick={e => { e.stopPropagation(); setSelectedNode(node.id); }}>
              <div style={S.nodeHead}>
                <span style={{ fontSize: 18 }}>{node.icon}</span>
                <span style={{ fontSize: 13, fontWeight: 600, color: '#ddd', flex: 1 }}>{node.label}</span>
                <button style={S.nodeDel} onClick={e => { e.stopPropagation(); removeNode(node.id); }}>×</button>
              </div>
              <div style={{ fontSize: 10, color: '#555', padding: '0 12px', fontFamily: 'JetBrains Mono' }}>{node.type}</div>
              <div style={{ fontSize: 11, color: '#777', padding: '4px 12px 10px', fontFamily: 'JetBrains Mono' }}>{node.name}</div>
            </div>
          ))}
        </main>

        {/* Right Panel */}
        <aside style={S.right}>
          {selected && (
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
      <div style={S.bottom}>
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
    </div>
  );
}

// Local code generation (mirrors the Go backend for instant preview)
function generateLocalCode(tool: string, nodes: any[]): string {
  if (tool === 'ansible') {
    let c = '---\n- name: IaC Studio Playbook\n  hosts: all\n  become: true\n  tasks:\n';
    nodes.forEach(n => {
      c += `    - name: ${n.name || n.type}\n      ${n.type}:\n`;
      Object.entries(n.properties).forEach(([k, v]) => {
        c += `        ${k}: ${typeof v === 'boolean' ? (v ? 'yes' : 'no') : `"${v}"`}\n`;
      });
      c += '\n';
    });
    return c;
  }
  let c = 'provider "aws" {\n  region = "us-east-1"\n}\n\n';
  nodes.forEach(n => {
    const name = n.name || n.type.replace('aws_', '');
    c += `resource "${n.type}" "${name}" {\n`;
    Object.entries(n.properties).forEach(([k, v]) => {
      c += typeof v === 'boolean' ? `  ${k} = ${v}\n` : `  ${k} = "${v}"\n`;
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
