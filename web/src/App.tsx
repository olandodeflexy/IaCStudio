import { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { api, ApiError, Resource, ToolInfo, CatalogResource, Suggestion, FileEntry, ImportResult, type PolicyFinding } from './api';
import { useWebSocket, WSMessage } from './useWebSocket';
import { useHistory } from './useHistory';
import { useKeyboardShortcuts } from './useKeyboardShortcuts';
import { UIButton, UIInput, UIKicker, UILabel, UIModal, UIPanel, UITextArea } from './ui';
import { CodeEditor } from './components/CodeEditor';
import { ChatPanel } from './components/Chat';
import { TerminalPanel } from './components/Terminal';
import { ModuleRegistryPanel } from './components/ModuleRegistry';
import { PolicyStudioPanel } from './components/PolicyStudio';
import { ScanPanel } from './components/ScanPanel';
import { SwimlaneCanvas } from './components/Canvas';
import { VisionDropzone } from './components/VisionDropzone';
import { S } from './styles';
import type { LayeredProject, LayeredModule } from './types';
import { envForResourceLoad, envForTool, shouldParseResourcesFromDisk, toolForEnv } from './projectLoad';
import {
  TOOLS,
  FALLBACK_RESOURCES,
  uid,
  edgeId,
  fileGlyph,
  generateLocalCode,
  type Edge,
} from './legacy';

type CanvasMode = 'freeform' | 'swimlane';
const EMPTY_CODE_PLACEHOLDER = 'Add resources from the palette or write code here';

const summarizePolicyFindings = (findings: PolicyFinding[]) => {
  if (findings.length === 0) return 'No finding details were returned.';
  const shown = findings.slice(0, 5).map((f, i) => {
    const target = f.resource ? ` on ${f.resource}` : '';
    return `${i + 1}. [${f.engine}] ${f.policy_id}${target}: ${f.message}`;
  });
  if (findings.length > shown.length) {
    shown.push(`...and ${findings.length - shown.length} more.`);
  }
  return shown.join('\n');
};

const isPolicyBlockedError = (err: unknown): err is ApiError => (
  err instanceof ApiError && err.status === 409 && err.payload?.error === 'policy_blocked'
);

const extractLayoutMeta = (state: any) => {
  if (!state?.layout) return null;
  const meta: Record<string, any> = {};
  for (const key of ['layout', 'blueprint', 'project_name', 'cloud', 'environments', 'environment_tools', 'modules', 'tags']) {
    if (state[key] !== undefined) meta[key] = state[key];
  }
  return meta;
};

const normalizeLayeredProject = (state: any): LayeredProject | null => {
  if (state?.layout !== 'layered-v1') return null;
  const environments = Array.isArray(state.environments)
    ? state.environments.filter((env: unknown): env is string => typeof env === 'string' && env.length > 0)
    : [];
  if (environments.length === 0) return null;
  const environmentTools = state.environment_tools && typeof state.environment_tools === 'object'
    ? Object.fromEntries(
        Object.entries(state.environment_tools)
          .filter(([env, envTool]) => environments.includes(env) && typeof envTool === 'string' && envTool.length > 0)
      ) as Record<string, string>
    : undefined;

  const rawModules = Array.isArray(state.modules) ? state.modules : [];
  const modules: LayeredModule[] = rawModules
    .map((mod: unknown) => {
      if (typeof mod === 'string') {
        return { name: mod, path: `modules/${mod}`, environments };
      }
      if (mod && typeof mod === 'object') {
        const candidate = mod as Partial<LayeredModule>;
        if (!candidate.name) return null;
        return {
          name: candidate.name,
          path: candidate.path || `modules/${candidate.name}`,
          source: candidate.source,
          environments: Array.isArray(candidate.environments) && candidate.environments.length > 0
            ? candidate.environments
            : environments,
        };
      }
      return null;
    })
    .filter((mod): mod is LayeredModule => Boolean(mod?.name));

  if (!modules.some((mod) => mod.name === 'root')) {
    modules.unshift({ name: 'root', path: 'environments', environments });
  }

  return { layout: 'layered-v1', environments, environmentTools, modules };
};

const resourceEnv = (resource: { file?: string }) => {
  if (!resource.file) return null;
  const parts = resource.file.split('/');
  const envIdx = parts.indexOf('environments');
  return envIdx >= 0 && parts.length > envIdx + 1 ? parts[envIdx + 1] : null;
};

const resourcesForEnv = <T extends { id: string; file?: string }>(resources: T[], env?: string) => {
  if (!env) return resources;
  return resources.filter(resource => {
    const envFromFile = resourceEnv(resource);
    return !envFromFile || envFromFile === env;
  });
};

const edgesForResources = (allEdges: Edge[], resources: { id: string }[]) => {
  const ids = new Set(resources.map(resource => resource.id));
  return allEdges.filter(edge => ids.has(edge.from) && ids.has(edge.to));
};

export default function App() {
  // Restore active project from localStorage on mount
  const saved = useRef((() => {
    try {
      const raw = localStorage.getItem('iac-studio-session');
      return raw ? JSON.parse(raw) : null;
    } catch { return null; }
  })());

  const [tool, setTool] = useState<string | null>(saved.current?.tool || null);
  const [detectedTools, setDetectedTools] = useState<ToolInfo[]>([]);
  const [projectName, setProjectName] = useState(saved.current?.projectName || 'my-infra-project');
  const [catalogResources, setCatalogResources] = useState<CatalogResource[]>([]);
  const [projectId, setProjectId] = useState(saved.current?.projectId || '');
  const [showImportWizard, setShowImportWizard] = useState(false);
  const [importTab, setImportTab] = useState<'browse' | 'topology'>('browse');
  const [browsePath, setBrowsePath] = useState('');
  const [browseEntries, setBrowseEntries] = useState<FileEntry[]>([]);
  const [browseParent, setBrowseParent] = useState('');
  const [importPreview, setImportPreview] = useState<ImportResult | null>(null);
  const [importLoading, setImportLoading] = useState(false);
  const [topologyDesc, setTopologyDesc] = useState('');
  const [topologyProvider, setTopologyProvider] = useState('aws');
  const [visionImages, setVisionImages] = useState<File[]>([]);
  const [visionError, setVisionError] = useState<string | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [aiSettings, setAiSettings] = useState({ type: 'ollama', endpoint: '', model: '', api_key: '' });
  const [savedProjects, setSavedProjects] = useState<any[]>([]);
  const { state: nodes, set: setNodes, undo: undoNodes, redo: redoNodes, canUndo, canRedo, reset: resetNodes } = useHistory<(Resource & { x: number; y: number; icon: string; label: string })[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<{ fromId: string; x: number; y: number } | null>(null);
  const [chatMessages, setChatMessages] = useState<{ role: string; text: string }[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [activePanel, setActivePanel] = useState('palette');
  const [rightTab, setRightTab] = useState<'inspect' | 'policy' | 'scan' | 'modules'>('inspect');
  const [projectLayoutMeta, setProjectLayoutMeta] = useState<Record<string, any> | null>(null);
  const [layeredProject, setLayeredProject] = useState<LayeredProject | null>(null);
  const [activeEnvironment, setActiveEnvironment] = useState<string | null>(null);
  const [canvasMode, setCanvasMode] = useState<CanvasMode>('freeform');

  const [terminalOutput, setTerminalOutput] = useState<string[]>([]);
  const [dragging, setDragging] = useState<{ id: string; ox: number; oy: number } | null>(null);
  const [wsConnected, setWsConnected] = useState(false);
  const [syncCode, setSyncCode] = useState('');
  const [codeSaving, setCodeSaving] = useState(false);
  const [notification, setNotification] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [lastCmdError, setLastCmdError] = useState<{ command: string; output: string } | null>(null);
  const [fixLoading, setFixLoading] = useState(false);
  const [hoveredResource, setHoveredResource] = useState<CatalogResource | null>(null);
  const [hoverPos, setHoverPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 });
  // Resizable panel sizes
  const [sidebarWidth, setSidebarWidth] = useState(240);
  const [rightWidth, setRightWidth] = useState(300);
  const [bottomHeight, setBottomHeight] = useState(220);
  const [resizing, setResizing] = useState<{ panel: string; startPos: number; startSize: number } | null>(null);

  const canvasRef = useRef<HTMLDivElement>(null);
  const chatEndRef = useRef<HTMLDivElement>(null);
  const isSyncing = useRef(false); // suppress file_changed echo from our own sync
  const isSwimlaneMode = Boolean(layeredProject && canvasMode === 'swimlane');
  const pulumiEnv = envForTool(tool || '', layeredProject, activeEnvironment);
  const activeTool = toolForEnv(tool || '', layeredProject, pulumiEnv);
  const concreteTool = activeTool && activeTool !== 'multi'
    ? activeTool
    : (tool && tool !== 'multi' ? tool : 'terraform');
  const activeEnvNodes = useMemo(
    () => resourcesForEnv(nodes, tool === 'multi' ? pulumiEnv : undefined),
    [nodes, pulumiEnv, tool],
  );
  const activeEnvEdges = useMemo(
    () => edgesForResources(edges, activeEnvNodes),
    [activeEnvNodes, edges],
  );

  const buildPersistedState = useCallback(() => ({
    ...(projectLayoutMeta || {}),
    tool,
    resources: nodes.map(n => ({
      id: n.id, type: n.type, name: n.name, label: n.label, icon: n.icon,
      properties: n.properties, file: n.file, line: n.line, x: n.x, y: n.y,
      connections: edges.filter(e => e.from === n.id).map(e => ({
        target_id: e.to, field: e.field, label: e.label,
      })),
    })),
  }), [projectLayoutMeta, tool, nodes, edges]);

  const applyProjectState = useCallback((state: any) => {
    const meta = extractLayoutMeta(state);
    const layered = normalizeLayeredProject(meta);
    setProjectLayoutMeta(meta);
    setLayeredProject(layered);
    setActiveEnvironment(current => {
      if (!layered) return null;
      if (current && layered.environments.includes(current)) return current;
      return layered.environments[0];
    });
    setCanvasMode(layered ? 'swimlane' : 'freeform');

    if (state?.resources?.length > 0) {
      resetNodes(state.resources.map((n: any) => ({
        id: n.id || `res_${Math.random().toString(36).slice(2)}`,
        type: n.type, name: n.name,
        label: n.label || n.type, icon: n.icon || '📦',
        properties: n.properties || {},
        file: n.file,
        line: n.line,
        x: n.x ?? 80 + Math.random() * 300,
        y: n.y ?? 80 + Math.random() * 200,
      })));
      const restoredEdges: Edge[] = [];
      for (const n of state.resources) {
        if (n.connections) {
          for (const c of n.connections) {
            restoredEdges.push({
              id: `${n.id}->${c.target_id}:${c.field}`,
              from: n.id, to: c.target_id,
              fromType: n.type,
              toType: state.resources.find((r: any) => r.id === c.target_id)?.type || '',
              field: c.field, label: c.label || c.field,
            });
          }
        }
      }
      setEdges(restoredEdges);
    } else {
      resetNodes([]);
      setEdges([]);
    }
  }, [resetNodes]);

  const applyParsedResources = useCallback((resources: Resource[]) => {
    resetNodes(resources.map((r, i) => {
      return {
        ...r,
        id: r.id || `${r.type}.${r.name || i}`,
        label: r.label || r.type,
        icon: r.icon || '📦',
        properties: r.properties || {},
        x: r.x ?? 80 + (i % 5) * 200,
        y: r.y ?? 80 + Math.floor(i / 5) * 130,
      };
    }));
    setEdges([]);
  }, [resetNodes]);

  // Detect tools and load saved projects on mount
  useEffect(() => {
    api.detectTools().then(setDetectedTools).catch(() => {});
    api.listProjectStates().then(setSavedProjects).catch(() => {});
    // Restore active project if we had one before reload
    if (saved.current?.projectId && saved.current?.tool) {
      hasCreatedProject.current = true;
      initialLoadDone.current = false;
      api.loadState(saved.current.projectId).then(state => {
        applyProjectState(state);
        const selectedTool = state?.tool || saved.current.tool;
        const selectedLayered = normalizeLayeredProject(extractLayoutMeta(state));
        setTool(selectedTool);
        if (shouldParseResourcesFromDisk(state)) {
          api.getResources(saved.current.projectId, selectedTool, envForResourceLoad(selectedTool, selectedLayered)).then(applyParsedResources).catch(() => {});
        }
      }).catch(() => {});
    }
  }, [applyParsedResources, applyProjectState]);

  // Persist active session to localStorage so page reload restores it
  useEffect(() => {
    if (tool && projectId) {
      localStorage.setItem('iac-studio-session', JSON.stringify({ tool, projectId, projectName }));
    } else {
      localStorage.removeItem('iac-studio-session');
    }
  }, [tool, projectId, projectName]);

  // Auto-save project state whenever canvas changes (debounced)
  const saveTimer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => {
    if (!tool || !projectId || !hasCreatedProject.current) return;
    clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      api.saveState(projectId, buildPersistedState()).catch(() => {});
    }, 2000);
  }, [buildPersistedState, tool, projectId]);

  // Open a saved project
  const openProject = useCallback(async (proj: any) => {
    setProjectName(proj.name);
    setProjectId(proj.name);
    setTool(proj.tool || 'terraform');
    hasCreatedProject.current = true;
    try {
      const state = await api.loadState(proj.name);
      applyProjectState(state);
      const selectedTool = state?.tool || proj.tool || 'terraform';
      const selectedLayered = normalizeLayeredProject(extractLayoutMeta(state));
      setTool(selectedTool);
      if (shouldParseResourcesFromDisk(state)) {
        const parsed = await api.getResources(proj.name, selectedTool, envForResourceLoad(selectedTool, selectedLayered));
        applyParsedResources(parsed);
      }
      setNotification(`Opened project: ${proj.name}`);
      setTimeout(() => setNotification(null), 3000);
    } catch {
      // No saved state — start fresh
      setProjectLayoutMeta(null);
      setLayeredProject(null);
      setActiveEnvironment(null);
      setCanvasMode('freeform');
    }
  }, [applyParsedResources, applyProjectState]);

  useEffect(() => {
    if (tool === 'ansible' && rightTab === 'modules') {
      setRightTab('inspect');
    }
  }, [tool, rightTab]);

  // WebSocket for live sync
  const handleWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type === 'terminal' && msg.output) {
      setTerminalOutput(prev => [...prev, ...msg.output!.split('\n')]);
      if (msg.error) {
        setTerminalOutput(prev => [...prev, `ERROR: ${msg.error}`]);
        // Capture the error for "Fix with AI" — include last command output
        setLastCmdError({ command: (msg as any).status || 'unknown', output: msg.output + '\n' + msg.error });
      } else {
        setLastCmdError(null); // Clear on success
      }
    }
    if (msg.type === 'ai_progress') {
      setNotification(msg.message || 'AI is working...');
    }
    if (msg.type === 'ai_topology_result') {
      setImportLoading(false);
      setNotification(null);
      if (msg.error) {
        setImportPreview({ tool: 'unknown', provider: 'unknown', files: [], resources: [], edges: [], summary: msg.error, warnings: [msg.error] });
      } else if (msg.resources) {
        setImportPreview({
          tool: 'terraform',
          provider: topologyProvider,
          files: [],
          resources: msg.resources,
          edges: [],
          summary: msg.message || 'Infrastructure generated',
        });
      }
    }
    if (msg.type === 'file_changed') {
      // Skip ALL file_changed events from our own operations:
      // - Our sync writes (isSyncing flag)
      // - Scaffold creation (hasCreatedProject is true but we just started)
      // - Any change while the canvas has content (user is actively editing)
      if (isSyncing.current) return;
      // Only show notification, don't re-parse. The canvas is the source of
      // truth — if the user wants to import external changes, they can
      // re-open the project or use the import feature.
      setNotification(`File updated: ${msg.file?.split('/').pop()}`);
      setTimeout(() => setNotification(null), 3000);
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

  // Keyboard shortcuts: undo, redo, delete, escape
  useKeyboardShortcuts({
    'ctrl+z': undoNodes,
    'ctrl+shift+z': redoNodes,
    'ctrl+y': redoNodes,
    'delete': () => {
      if (selectedEdge) {
        setEdges(prev => prev.filter(e => e.id !== selectedEdge));
        setSelectedEdge(null);
      } else if (selectedNode) {
        removeNode(selectedNode);
      }
    },
    'backspace': () => {
      if (selectedEdge) {
        setEdges(prev => prev.filter(e => e.id !== selectedEdge));
        setSelectedEdge(null);
      } else if (selectedNode) {
        removeNode(selectedNode);
      }
    },
    'escape': () => {
      setSelectedNode(null);
      setSelectedEdge(null);
      setConnecting(null);
    },
  });

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
    api.getCatalog(concreteTool).then(cat => {
      setCatalogResources(cat.resources || []);
    }).catch(() => {
      setCatalogResources(FALLBACK_RESOURCES);
    });
  }, [concreteTool, tool]);

  // Generate code preview whenever nodes change
  useEffect(() => {
    if (!tool || !nodes.length) {
      setSyncCode('');
      return;
    }
    const code = generateLocalCode(concreteTool, activeEnvNodes, activeEnvEdges);
    setSyncCode(code);
  }, [activeEnvEdges, activeEnvNodes, concreteTool, tool]);

  // Sync to disk (debounced) — syncs even when nodes is empty so that
  // deleting the last resource clears the generated file on disk.
  const syncTimer = useRef<ReturnType<typeof setTimeout>>();
  const hasCreatedProject = useRef(false);
  const initialLoadDone = useRef(false);
  useEffect(() => {
    if (!tool || !hasCreatedProject.current || !projectId) return;
    // Skip the first sync after opening a project — the restored state
    // doesn't need to be written back immediately (it came from disk).
    if (!initialLoadDone.current) {
      initialLoadDone.current = true;
      return;
    }
    clearTimeout(syncTimer.current);
    syncTimer.current = setTimeout(() => {
      isSyncing.current = true;
      api.syncToDisk(projectId, tool, activeEnvNodes, activeEnvEdges, pulumiEnv).catch(() => {}).finally(() => {
        setTimeout(() => { isSyncing.current = false; }, 1500);
      });
    }, 2000);
  }, [activeEnvEdges, activeEnvNodes, tool, projectId, pulumiEnv]);

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

  const chatInFlightRef = useRef(false);

  const handleChat = async () => {
    if (chatLoading || chatInFlightRef.current) return;
    if (!chatInput.trim() || !tool) return;

    chatInFlightRef.current = true;
    setChatLoading(true);

    const input = chatInput;
    setChatInput('');
    // Append the user turn and a placeholder AI bubble that will be filled in
    // by the streaming deltas below. Track the assistant bubble by a stable id
    // assigned before enqueueing state so later patches do not depend on
    // updater side-effects or a mutable array index.
    const aiMessageId = `ai-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    let pendingAiText = '';
    const updateAiMessageText = (text: string) => {
      setChatMessages(prev => {
        const nextAiIndex = prev.findIndex(message => (message as { id?: string }).id === aiMessageId);
        if (nextAiIndex < 0) return prev;
        const next = [...prev];
        next[nextAiIndex] = { ...next[nextAiIndex], text };
        return next;
      });
    };

    setChatMessages(prev => [
      ...prev,
      { role: 'user' as const, text: input },
      { role: 'ai' as const, text: pendingAiText, id: aiMessageId } as (typeof prev)[number],
    ]);

    try {
      const provider = detectProvider();
      const history = chatMessages.map(m => ({ role: m.role === 'ai' ? 'ai' : 'user', content: m.text }));
      const canvas = nodes.map(n => ({ type: n.type, name: n.name }));

      const result = await api.chatStream(
        { message: input, tool, provider, history, canvas },
        (delta: string) => {
          // Append raw tokens to the live assistant bubble. The server parses
          // the final JSON in the "complete" event, so we still get clean
          // message + resources at the end — this just shows progress.
          pendingAiText += delta;
          updateAiMessageText(pendingAiText);
        },
      );

      // Replace the streamed raw text with the parsed clean message.
      pendingAiText = result.message;
      updateAiMessageText(pendingAiText);
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
      pendingAiText = 'AI is unavailable. Make sure your provider is reachable.';
      updateAiMessageText(pendingAiText);
    } finally {
      chatInFlightRef.current = false;
      setChatLoading(false);
    }
  };

  const runCmd = (command: string) => {
    if (!tool) return;
    // apply/destroy require explicit confirmation
    const needsApproval = command === 'apply' || command === 'destroy';
    if (needsApproval && !confirm(`Are you sure you want to run "${command}"? This will modify real infrastructure.`)) {
      return;
    }
    setTerminalOutput(prev => [...prev, `$ ${command}`, '']);
    api.runCommand(projectId, tool, command, {
      approved: needsApproval,
      env: pulumiEnv,
    }).catch(err => {
      if (needsApproval && isPolicyBlockedError(err)) {
        const findings = err.payload?.findings ?? [];
        const blockingCount = findings.filter(f => f.severity === 'error').length;
        const summary = summarizePolicyFindings(findings);
        const summaryLines = summary.split('\n');
        setTerminalOutput(prev => [
          ...prev,
          `Policy blocked ${command}: ${blockingCount} blocking finding${blockingCount === 1 ? '' : 's'}`,
          ...summaryLines,
        ]);
        if (!confirm(`Policy checks blocked "${command}".\n\n${summary}\n\nRun it anyway and acknowledge these findings?`)) {
          return;
        }
        setTerminalOutput(prev => [...prev, `$ ${command} --acknowledged`, '']);
        api.runCommand(projectId, tool, command, {
          approved: true,
          env: pulumiEnv,
          acknowledged: true,
        }).catch(overrideErr => {
          setTerminalOutput(prev => [...prev, `Error: ${overrideErr.message}`]);
        });
        return;
      }
      setTerminalOutput(prev => [...prev, `Error: ${err.message}`]);
    });
  };

  const closeImportWizard = () => {
    setShowImportWizard(false);
    setImportPreview(null);
    setVisionImages([]);
    setVisionError(null);
  };

  const handleGenerateTopology = async () => {
    const toolKey = detectedTools.find(t => t.available && t.name !== 'Ansible')?.name === 'OpenTofu' ? 'opentofu' : 'terraform';

    if (visionImages.length > 0) {
      setImportLoading(true);
      setNotification('AI is reading your diagram...');
      try {
        const result = await api.generateTopologyFromImages({
          description: topologyDesc,
          tool: toolKey,
          provider: topologyProvider,
          images: visionImages,
        });
        if (result.message) {
          setChatMessages(prev => [...prev, { role: 'ai', text: `Diagram analysis: ${result.message}` }]);
        }
        setImportPreview({
          tool: toolKey,
          provider: topologyProvider,
          files: visionImages.map(file => ({ path: file.name, name: file.name, type: file.type, size: file.size })),
          resources: result.resources || [],
          edges: [],
          summary: result.message || 'Infrastructure generated from diagram',
        });
      } catch (e: any) {
        setImportPreview({
          tool: 'unknown',
          provider: topologyProvider,
          files: [],
          resources: [],
          edges: [],
          summary: e.message || 'Diagram analysis failed',
          warnings: [e.message || 'Diagram analysis failed'],
        });
      } finally {
        setImportLoading(false);
        setNotification(null);
      }
      return;
    }

    if (!topologyDesc.trim()) return;
    setImportLoading(true);
    setNotification('AI is designing your infrastructure...');
    try {
      // Fire and forget — result arrives via WebSocket
      await api.generateTopology(topologyDesc, toolKey, topologyProvider);
      // Don't setImportLoading(false) here — WebSocket handler does it
    } catch (e: any) {
      const message = e?.message || 'Generation failed';
      setImportPreview({ tool: 'unknown', provider: 'unknown', files: [], resources: [], edges: [], summary: message, warnings: [message] });
      setImportLoading(false);
      setNotification(null);
    }
  };

  const saveCodeToDisk = useCallback(async (value: string) => {
    if (!tool || !projectId) return;
    if (!value.trim()) {
      setNotification('Nothing to save yet');
      setTimeout(() => setNotification(null), 3000);
      return;
    }
    setCodeSaving(true);
    isSyncing.current = true;
    try {
      const fileName = concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || '.tf'}`;
      await api.syncCodeToDisk(projectId, tool, value, fileName, pulumiEnv);
      setNotification(`Saved ${fileName}`);
      setTimeout(() => setNotification(null), 3000);
    } catch (err: any) {
      setNotification(`Save failed: ${err.message}`);
      setTimeout(() => setNotification(null), 5000);
    } finally {
      setCodeSaving(false);
      setTimeout(() => { isSyncing.current = false; }, 1500);
    }
  }, [concreteTool, projectId, pulumiEnv, tool]);

  const handleCreateProject = async (selectedTool: string) => {
    setTool(selectedTool);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    // Lock the project ID at creation time so renaming the display input
    // can't silently redirect API calls to a different directory.
    setProjectId(projectName);
    hasCreatedProject.current = true;
    initialLoadDone.current = true; // new projects sync immediately
    try {
      await api.createProject(projectName, selectedTool);
    } catch {
      // Backend might not be running, continue with local-only mode
    }
  };

  // ─── Tool Selection ───
  if (!tool) {
    return (
      <div style={S.selectScreen} className="select-screen">
        <div className="ambient-orb ambient-orb-a" />
        <div className="ambient-orb ambient-orb-b" />
        <div style={S.selectBg} />
        <div style={S.selectContent}>
          <div style={S.logo}><span style={{ fontSize: 28, color: 'var(--accent-action)' }}>◆</span> <span style={S.logoText}>IaC Studio</span></div>
          {/* Saved projects */}
          {savedProjects.length > 0 && (
            <div style={{ marginBottom: 32, width: '100%', maxWidth: 600 }}>
              <UIKicker style={{ marginBottom: 12 }}>Recent Projects</UIKicker>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {savedProjects.filter(p => p.tool).slice(0, 5).map(p => {
                  const t = TOOLS[p.tool] || TOOLS.terraform;
                  const count = p.resources?.length || 0;
                  return (
                    <button key={p.name} className="tool-card" style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 16px', background: 'var(--bg-elev-1)', border: '1px solid var(--border-main)', borderRadius: 10, cursor: 'pointer', textAlign: 'left', transition: 'border-color 0.2s' }}
                      onClick={() => openProject(p)}
                      onMouseEnter={e => { (e.currentTarget as any).style.borderColor = t.color; }}
                      onMouseLeave={e => { (e.currentTarget as any).style.borderColor = 'var(--border-main)'; }}>
                      <span style={{ fontSize: 20 }}>{t.icon}</span>
                      <div style={{ flex: 1 }}>
                        <div style={{ fontSize: 14, fontWeight: 600, color: '#ccc', fontFamily: 'JetBrains Mono' }}>{p.name}</div>
                        <div style={{ fontSize: 11, color: '#555' }}>{t.name} · {count} resource{count !== 1 ? 's' : ''}{p.updated_at ? ' · ' + new Date(p.updated_at).toLocaleDateString() : ''}</div>
                      </div>
                      <span style={{ fontSize: 11, color: '#555', cursor: 'pointer', fontFamily: 'JetBrains Mono' }}
                        title="Open in file manager"
                        onClick={(e) => { e.stopPropagation(); api.revealProject(p.name).catch(() => {}); }}>OPEN</span>
                      <span style={{ fontSize: 12, color: '#555', cursor: 'pointer', padding: '0 8px' }}
                        title="Delete project"
                        onClick={async (e) => {
                          e.stopPropagation();
                          if (!confirm(`Delete project "${p.name}"?\n\nThis will permanently remove the project directory and all its files.\n\nThis cannot be undone.`)) return;
                          try {
                            await api.deleteProject(p.name);
                            setSavedProjects(prev => prev.filter(sp => sp.name !== p.name));
                            setNotification(`Deleted project: ${p.name}`);
                            setTimeout(() => setNotification(null), 3000);
                          } catch (err: any) {
                            setNotification(`Failed to delete: ${err.message}`);
                            setTimeout(() => setNotification(null), 4000);
                          }
                        }}>✕</span>
                      <span style={{ fontSize: 11, color: t.color, fontWeight: 700, fontFamily: 'JetBrains Mono' }}>OPEN</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          <h1 style={S.title}>{savedProjects.length > 0 ? 'New Project' : 'Choose your IaC tool'}</h1>
          <p style={S.subtitle}>Visual infrastructure builder with AI-powered assistance</p>
          <div style={S.cardGrid}>
            {Object.entries(TOOLS).map(([key, t]) => {
              const detected = detectedTools.find(d => d.name === t.name);
              return (
                <button key={key} className="tool-card panel-reveal" style={{ ...S.card, borderColor: t.color + '33' }}
                  onClick={() => handleCreateProject(key)}
                  onMouseEnter={e => { (e.currentTarget as any).style.borderColor = t.color; (e.currentTarget as any).style.transform = 'translateY(-4px)'; }}
                  onMouseLeave={e => { (e.currentTarget as any).style.borderColor = t.color + '33'; (e.currentTarget as any).style.transform = 'translateY(0)'; }}>
                  <span style={{ fontSize: 26, fontWeight: 700, letterSpacing: 0.8, fontFamily: 'JetBrains Mono', color: t.color }}>{t.icon}</span>
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
                <span style={{ fontSize: 8, color: 'var(--accent-action)' }}>●</span> {f}
              </div>
            ))}
          </div>

          {/* Project name & directory */}
          <UIPanel style={{ marginTop: 32, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10, padding: '16px 24px', width: '100%', maxWidth: 480 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', width: '100%' }}>
              <UILabel style={{ whiteSpace: 'nowrap' }}>Project name:</UILabel>
              <UIInput style={{ flex: 1, minWidth: 0 }}
                value={projectName} onChange={e => setProjectName(e.target.value)} placeholder="my-infra-project" />
            </div>
            <div className="ui-path">
              ROOT ~/iac-projects/<span style={{ color: 'var(--accent-action)' }}>{projectName}</span>/
            </div>

            {/* Import / Topology buttons */}
            <div style={{ marginTop: 12, display: 'flex', gap: 10 }}>
              <UIButton
                onClick={() => { setImportTab('browse'); setVisionImages([]); setVisionError(null); setShowImportWizard(true); api.browse().then(r => { setBrowsePath(r.path); setBrowseEntries(r.entries); setBrowseParent(r.parent); }).catch(() => {}); }}>
                Import Existing Project
              </UIButton>
              <UIButton variant="primary"
                onClick={() => { setImportTab('topology'); setShowImportWizard(true); }}>
                Build with AI
              </UIButton>
            </div>
          </UIPanel>

          {/* ─── Import Wizard Modal ─── */}
          {showImportWizard && (
            <UIModal onClose={closeImportWizard}>
              {/* Wizard header */}
              <div className="ui-modal-header">
                <div style={{ display: 'flex', gap: 12 }}>
                    {(['browse', 'topology'] as const).map(t => (
                      <UIButton key={t} variant="tab" active={importTab === t}
                        onClick={() => { setImportTab(t); setImportPreview(null); if (t === 'browse') { setVisionImages([]); setVisionError(null); } }}>
                        {t === 'browse' ? 'Browse Files' : 'AI Topology'}
                      </UIButton>
                    ))}
                </div>
                <button className="ui-close" onClick={closeImportWizard}>×</button>
              </div>

                {/* Browse tab */}
                {importTab === 'browse' && !importPreview && (
                  <div style={{ flex: 1, overflow: 'auto', minHeight: 300 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 20px', borderBottom: '1px solid var(--border-soft)', background: 'var(--bg-elev-1)' }}>
                      <UIButton
                        onClick={() => { api.browse(browseParent).then(r => { setBrowsePath(r.path); setBrowseEntries(r.entries); setBrowseParent(r.parent); }).catch(() => {}); }}>
                        ↑
                      </UIButton>
                      <span style={{ fontSize: 11, color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{browsePath}</span>
                      <UIButton variant="primary"
                        disabled={importLoading}
                        onClick={async () => {
                          setImportLoading(true);
                          try {
                            const result = await api.importProject(browsePath);
                            setImportPreview(result);
                          } catch (e: any) {
                            setImportPreview({ tool: 'unknown', provider: 'unknown', files: [], resources: [], edges: [], summary: e.message || 'Import failed', warnings: [e.message] });
                          }
                          setImportLoading(false);
                        }}>
                        {importLoading ? 'Scanning...' : 'Import this folder'}
                      </UIButton>
                    </div>
                    <div style={{ padding: '4px 0' }}>
                      {browseEntries.map(entry => (
                        <div key={entry.path} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 20px', cursor: entry.is_dir ? 'pointer' : 'default', fontSize: 13, color: entry.is_dir ? '#ccc' : '#777', fontFamily: 'JetBrains Mono' }}
                          onClick={() => {
                            if (entry.is_dir) {
                              api.browse(entry.path).then(r => { setBrowsePath(r.path); setBrowseEntries(r.entries); setBrowseParent(r.parent); }).catch(() => {});
                            }
                          }}
                          onMouseEnter={e => { if (entry.is_dir) (e.currentTarget as any).style.background = 'var(--bg-elev-2)'; }}
                          onMouseLeave={e => { (e.currentTarget as any).style.background = 'transparent'; }}>
                          <span style={{ fontSize: 10, fontFamily: 'JetBrains Mono', color: '#7b8d84', minWidth: 30 }}>{fileGlyph(entry)}</span>
                          <span style={{ flex: 1 }}>{entry.name}</span>
                          {entry.is_dir && entry.children !== undefined && <span style={{ color: '#444', fontSize: 10 }}>{entry.children} items</span>}
                          {!entry.is_dir && <span style={{ color: '#444', fontSize: 10 }}>{entry.size > 1024 ? Math.round(entry.size / 1024) + 'KB' : entry.size + 'B'}</span>}
                        </div>
                      ))}
                      {browseEntries.length === 0 && <div style={{ padding: 20, textAlign: 'center', color: '#444' }}>Empty directory</div>}
                    </div>
                  </div>
                )}

                {/* Topology tab */}
                {importTab === 'topology' && !importPreview && (
                  <div style={{ flex: 1, padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
                    <div style={{ fontSize: 14, color: 'var(--text-main)', fontWeight: 600 }}>Describe your infrastructure</div>
                    <div className="ui-note">
                      Tell us what you want to build in plain language, or upload a diagram for vision analysis.
                    </div>
                    <VisionDropzone
                      files={visionImages}
                      onFilesChange={setVisionImages}
                      onError={setVisionError}
                      error={visionError}
                      disabled={importLoading}
                    />
                    <UITextArea style={{ flex: 1, minHeight: 120 }}
                      value={topologyDesc} onChange={e => setTopologyDesc(e.target.value)}
                      placeholder={"Optional context:\nA three-tier web app with VPC, ALB, auto-scaling EC2, RDS PostgreSQL, and S3 for static assets\nA GKE cluster with Cloud SQL, Redis cache, and Cloud Storage for a microservices platform"} />
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                      <UILabel>Provider:</UILabel>
                      {['aws', 'google', 'azurerm'].map(p => (
                        <UIButton key={p} variant="tab" active={topologyProvider === p}
                          onClick={() => setTopologyProvider(p)}>
                          {p === 'aws' ? 'AWS' : p === 'google' ? 'GCP' : 'Azure'}
                        </UIButton>
                      ))}
                    </div>
                    <UIButton variant="primary"
                      disabled={(!topologyDesc.trim() && visionImages.length === 0) || Boolean(visionError) || importLoading}
                      onClick={handleGenerateTopology}>
                      {importLoading ? 'Generating... (this may take a minute)' : visionImages.length > 0 ? 'Generate from Diagram' : 'Generate Infrastructure'}
                    </UIButton>
                  </div>
                )}

                {/* Preview panel — shown after scan or generation */}
                {importPreview && (
                  <div style={{ flex: 1, overflow: 'auto', padding: 20, display: 'flex', flexDirection: 'column', gap: 12 }}>
                    <div style={{ fontSize: 14, fontWeight: 600, color: '#bbb' }}>
                      {importPreview.tool === 'unknown' ? 'Import Failed' : 'Preview'}
                    </div>
                    <div className="ui-note">{importPreview.summary}</div>

                    {importPreview.warnings && importPreview.warnings.length > 0 && (
                      <div style={{ background: '#ef444411', border: '1px solid #ef444433', borderRadius: 8, padding: 10 }}>
                        {importPreview.warnings.map((w, i) => (
                          <div key={i} style={{ fontSize: 11, color: '#ef4444', fontFamily: 'JetBrains Mono' }}>{w}</div>
                        ))}
                      </div>
                    )}

                    {importPreview.resources.length > 0 && (
                      <>
                        <div style={{ fontSize: 11, color: '#555', fontFamily: 'JetBrains Mono', textTransform: 'uppercase', letterSpacing: 1 }}>
                          {importPreview.resources.length} Resources
                        </div>
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                          {importPreview.resources.map((r, i) => {
                            const meta = catalogResources.find(c => c.type === r.type);
                            return (
                              <span key={i} style={{ background: 'var(--bg-elev-2)', borderRadius: 6, padding: '4px 10px', fontSize: 11, color: 'var(--text-main)', fontFamily: 'JetBrains Mono' }}>
                                {meta?.icon ?? '📦'} {r.type}.{r.name}
                              </span>
                            );
                          })}
                        </div>
                        {importPreview.edges.length > 0 && (
                          <div style={{ fontSize: 11, color: '#555', fontFamily: 'JetBrains Mono' }}>
                            {importPreview.edges.length} connections detected
                          </div>
                        )}
                      </>
                    )}

                    <div style={{ display: 'flex', gap: 10, marginTop: 8 }}>
                      <UIButton onClick={() => setImportPreview(null)}>
                        ← Back
                      </UIButton>
                      {importPreview.resources.length > 0 && (
                        <UIButton variant="primary"
                          onClick={async () => {
                            const t = importPreview!.tool === 'opentofu' ? 'opentofu' : importPreview!.tool === 'ansible' ? 'ansible' : 'terraform';
                            try {
                              await api.createProject(projectName, t);
                            } catch (e: any) {
                              setNotification(`Import failed: ${e.message}`);
                              setTimeout(() => setNotification(null), 5000);
                              return;
                            }
                            setTool(t);
                            setProjectId(projectName);
                            setProjectLayoutMeta(null);
                            setLayeredProject(null);
                            setActiveEnvironment(null);
                            setCanvasMode('freeform');
                            hasCreatedProject.current = true;
                            initialLoadDone.current = true;
                            // Place resources on canvas in a grid layout
                            const imported = importPreview!.resources.map((r, i) => ({
                              ...(() => {
                                const { file: _file, line: _line, ...rest } = r;
                                return rest;
                              })(),
                              id: r.id || `imp_${i}_${Date.now()}`,
                              x: 80 + (i % 5) * 200,
                              y: 80 + Math.floor(i / 5) * 130,
                              icon: catalogResources.find(c => c.type === r.type)?.icon ?? '📦',
                              label: catalogResources.find(c => c.type === r.type)?.label ?? r.type,
                            }));
                            resetNodes(imported);
                            // Create edges from detected connections
                            if (importPreview!.edges.length > 0) {
                              const newEdges = importPreview!.edges.map(e => ({
                                id: `${e.from_id}->${e.to_id}:${e.field}`,
                                from: e.from_id,
                                to: e.to_id,
                                fromType: importPreview!.resources.find(r => r.id === e.from_id)?.type || '',
                                toType: importPreview!.resources.find(r => r.id === e.to_id)?.type || '',
                                field: e.field,
                                label: e.field.replace(/_/g, ' '),
                              }));
                              setEdges(newEdges);
                            }
                            setShowImportWizard(false);
                            setImportPreview(null);
                            setVisionImages([]);
                            setVisionError(null);
                            setNotification(`Imported ${importPreview!.resources.length} resources`);
                            setTimeout(() => setNotification(null), 4000);
                          }}>
                          Import to Canvas
                        </UIButton>
                      )}
                    </div>
                  </div>
                )}
            </UIModal>
          )}
        </div>
      </div>
    );
  }

  const ct = TOOLS[tool] || TOOLS[concreteTool] || TOOLS.terraform;
  const selected = nodes.find(n => n.id === selectedNode);
  const codeFileLabel = concreteTool === 'pulumi'
    ? (pulumiEnv ? `environments/${pulumiEnv}/index.ts` : 'index.ts')
    : (tool === 'multi' && pulumiEnv ? `environments/${pulumiEnv}/main${TOOLS[concreteTool]?.ext || '.tf'}` : `main${TOOLS[concreteTool]?.ext || ct.ext}`);

  // ─── Main UI ───
  return (
    <div style={S.app} className="iac-app">
      {/* Notification */}
      {notification && (
        <div style={S.notification}>{notification}</div>
      )}

      {/* Header */}
      <header style={{ ...S.header, borderBottomColor: ct.color + '44' }} className="iac-header">
        <div style={S.hLeft}>
          <button style={S.backBtn} onClick={async () => {
            // Save state before navigating away
            if (projectId && hasCreatedProject.current) {
              await api.saveState(projectId, buildPersistedState()).catch(() => {});
            }
            // Refresh saved projects list
            api.listProjectStates().then(setSavedProjects).catch(() => {});
            setTool(null); resetNodes([]); setEdges([]); setChatMessages([]); setTerminalOutput([]);
            setProjectLayoutMeta(null); setLayeredProject(null); setActiveEnvironment(null); setCanvasMode('freeform');
            initialLoadDone.current = false; hasCreatedProject.current = false;
          }}>←</button>
          <span style={{ ...S.badge, background: ct.color + '22', color: ct.color }}>{ct.icon} {ct.name}</span>
          <input style={S.projInput} value={projectName} onChange={e => setProjectName(e.target.value)} />
          <button style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 10, fontFamily: 'JetBrains Mono', padding: '2px 6px', color: '#789187' }}
            title="Open in file manager"
            onClick={() => api.revealProject(projectId).catch(() => {})}>OPEN</button>
          <span style={{ fontSize: 10, color: wsConnected ? '#4ade80' : '#ef4444' }}>{wsConnected ? '● live' : '● offline'}</span>
        </div>
        <div style={S.hRight}>
          <span style={S.count}>{nodes.length} resource{nodes.length !== 1 ? 's' : ''}</span>
          <button style={{ ...S.cmd, background: 'var(--bg-elev-2)', color: canUndo ? 'var(--text-main)' : '#4b5551' }} onClick={undoNodes} disabled={!canUndo} title="Undo (Ctrl+Z)">↩</button>
          <button style={{ ...S.cmd, background: 'var(--bg-elev-2)', color: canRedo ? 'var(--text-main)' : '#4b5551' }} onClick={redoNodes} disabled={!canRedo} title="Redo (Ctrl+Shift+Z)">↪</button>
          <button style={{ ...S.cmd, background: ct.color + '22', color: ct.color }}
            onClick={() => runCmd(tool === 'ansible' ? 'check' : 'init')}>
            {tool === 'ansible' ? '▶ Check' : '▶ Init'}
          </button>
          <button style={{ ...S.cmd, background: ct.color + '22', color: ct.color }}
            onClick={() => runCmd(tool === 'ansible' ? 'syntax' : 'plan')}>
            {tool === 'ansible' ? '▶ Syntax' : '▶ Plan'}
          </button>
          <UIButton variant="primary" style={{ background: ct.color, borderColor: ct.color, color: '#0a0a0f' }}
            onClick={() => runCmd(tool === 'ansible' ? 'playbook' : 'apply')}>
            ▶ Apply
          </UIButton>
          <UIButton
            onClick={() => { api.getAISettings().then(setAiSettings).catch(() => {}); setShowSettings(true); }}
            title="AI Settings">
            SETTINGS
          </UIButton>
        </div>
      </header>

      {/* AI Settings Modal */}
      {showSettings && (
        <UIModal onClose={() => setShowSettings(false)} width={480} className="ui-panel--raised">
          <div style={{ padding: 24 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
              <span className="ui-modal-title">AI Settings</span>
              <button className="ui-close" onClick={() => setShowSettings(false)}>×</button>
            </div>

            <div style={{ marginBottom: 16 }}>
              <UILabel>Provider Type</UILabel>
              <div className="ui-choice-grid" style={{ marginTop: 8 }}>
                {[
                  { key: 'ollama', label: 'Ollama (Local)', desc: 'Free, private, runs on your machine' },
                  { key: 'openai', label: 'OpenAI API', desc: 'GPT-4o, GPT-4-turbo' },
                  { key: 'anthropic', label: 'Anthropic', desc: 'Claude Opus, Claude Haiku' },
                  { key: 'custom', label: 'Custom API', desc: 'Any OpenAI-compatible endpoint' },
                ].map(p => (
                  <button key={p.key} className={aiSettings.type === p.key ? 'ui-choice-card is-active' : 'ui-choice-card'}
                    onClick={() => {
                      if (p.key === 'ollama') setAiSettings(s => ({ ...s, type: 'ollama', endpoint: 'http://localhost:11434', api_key: '' }));
                      else if (p.key === 'openai') setAiSettings(s => ({ ...s, type: 'openai', endpoint: 'https://api.openai.com/v1', model: 'gpt-4o' }));
                      else if (p.key === 'anthropic') setAiSettings(s => ({ ...s, type: 'anthropic', endpoint: '', model: 'claude-haiku-4-5', api_key: '' }));
                      else setAiSettings(s => ({ ...s, type: 'custom' }));
                    }}>
                    <div className="ui-choice-title">{p.label}</div>
                    <div className="ui-choice-desc">{p.desc}</div>
                  </button>
                ))}
              </div>
            </div>

            <div style={{ marginBottom: 12 }}>
              <UILabel>Endpoint</UILabel>
              <UIInput value={aiSettings.endpoint} onChange={e => setAiSettings(s => ({ ...s, endpoint: e.target.value }))}
                placeholder={aiSettings.type === 'ollama' ? 'http://localhost:11434' : aiSettings.type === 'anthropic' ? 'https://api.anthropic.com (optional)' : 'https://api.openai.com/v1'} />
            </div>

            <div style={{ marginBottom: 12 }}>
              <UILabel>Model</UILabel>
              <UIInput value={aiSettings.model} onChange={e => setAiSettings(s => ({ ...s, model: e.target.value }))}
                placeholder={aiSettings.type === 'ollama' ? 'gemma4' : aiSettings.type === 'anthropic' ? 'claude-haiku-4-5' : 'gpt-4o'} />
            </div>

            {aiSettings.type !== 'ollama' && (
              <div style={{ marginBottom: 12 }}>
                <UILabel>API Key</UILabel>
                <UIInput type="password" value={aiSettings.api_key} onChange={e => setAiSettings(s => ({ ...s, api_key: e.target.value }))}
                  placeholder="sk-..." />
                <div className="ui-note ui-note--small" style={{ marginTop: 4 }}>Your key is sent to the backend only — never stored on disk or sent to third parties.</div>
              </div>
            )}

            <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
              <UIButton block onClick={() => setShowSettings(false)}>Cancel</UIButton>
              <UIButton block variant="primary"
                onClick={async () => {
                  try {
                    await api.updateAISettings(aiSettings);
                    setNotification('AI settings updated');
                    setTimeout(() => setNotification(null), 3000);
                    setShowSettings(false);
                  } catch (e: any) {
                    setNotification(`Failed: ${e.message}`);
                    setTimeout(() => setNotification(null), 4000);
                  }
                }}>Save</UIButton>
            </div>
          </div>
        </UIModal>
      )}

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
              <div style={{ padding: '8px 10px', borderBottom: '1px solid var(--border-soft)' }}>
                <input
                  style={{ ...S.finput, fontSize: 12, padding: '6px 10px', background: 'var(--bg-app)' }}
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
                          (e.currentTarget as any).style.background = 'var(--bg-elev-2)';
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
              <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12, fontFamily: 'JetBrains Mono' }}>DIR {projectName}/</div>
              {['main' + ct.ext, 'variables' + ct.ext, 'outputs' + ct.ext, '.gitignore'].map(f => (
                <div key={f} style={{ fontSize: 12, color: '#777', padding: '5px 0 5px 12px', fontFamily: 'JetBrains Mono', cursor: 'pointer' }}>FILE {f}</div>
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
                      onMouseEnter={e => { (e.currentTarget as any).style.background = 'var(--bg-elev-2)'; }}
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
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = 'var(--border-main)'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />

        {/* Canvas */}
        <main style={S.canvas} ref={canvasRef} onMouseMove={isSwimlaneMode ? undefined : onMouseMove} onMouseUp={(e) => {
          if (isSwimlaneMode) return;
          onMouseUp(e);
          // Finish manual connection if dragging to empty space
          if (connecting) setConnecting(null);
        }} onMouseLeave={() => { if (!isSwimlaneMode) { onMouseUp(null as any); setConnecting(null); } }}
          onClick={() => { setSelectedNode(null); setSelectedEdge(null); }}>
          {layeredProject && (
            <div style={{ position: 'absolute', top: 12, right: 12, zIndex: 10, display: 'flex', gap: 6, padding: 4, background: 'var(--bg-elev-1)', border: '1px solid var(--border-main)', borderRadius: 8 }}>
              {layeredProject.environments.map(env => {
                const envTool = layeredProject.environmentTools?.[env];
                return (
                  <button
                    key={env}
                    style={{ ...S.cmd, background: activeEnvironment === env ? ct.color + '22' : 'transparent', color: activeEnvironment === env ? ct.color : 'var(--text-muted)', padding: '5px 9px' }}
                    onClick={(e) => { e.stopPropagation(); setActiveEnvironment(env); }}
                    title={envTool ? `${env} uses ${envTool}` : env}
                  >
                    {envTool ? `${env} (${envTool})` : env}
                  </button>
                );
              })}
              {(['swimlane', 'freeform'] as const).map(mode => (
                <button
                  key={mode}
                  style={{ ...S.cmd, background: canvasMode === mode ? ct.color + '22' : 'transparent', color: canvasMode === mode ? ct.color : 'var(--text-muted)', padding: '5px 9px' }}
                  onClick={(e) => { e.stopPropagation(); setCanvasMode(mode); }}
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
              onSelectResource={(id) => { setSelectedNode(id); setSelectedEdge(null); }}
            />
          ) : (
            <>
          <div style={S.grid} className="iac-canvas-grid" />

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
              <div style={{ fontSize: 20, opacity: 0.5, marginBottom: 16, fontFamily: 'JetBrains Mono', letterSpacing: 1.5 }}>CANVAS</div>
              <div style={{ fontSize: 16, opacity: 0.4 }}>Drag resources from the palette</div>
              <div style={{ fontSize: 14, opacity: 0.3, marginTop: 4 }}>or use AI chat below</div>
            </div>
          )}
          {nodes.map(node => {
            const nodeEdges = edges.filter(e => e.from === node.id || e.to === node.id);
            const hasConnections = nodeEdges.length > 0;
            return (
            <div key={node.id} className="node-shell"
              style={{ ...S.node, left: node.x, top: node.y, zIndex: 2,
                borderColor: selectedNode === node.id ? ct.color : hasConnections ? `${ct.color}44` : 'var(--border-main)',
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
                <div style={{ width: 14, height: 14, borderRadius: '50%', border: `2px solid ${ct.color}55`, background: 'var(--bg-elev-2)',
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
            </>
          )}
        </main>

        {/* Right Panel */}
        {/* Right panel resize handle */}
        <div style={{ width: 4, cursor: 'col-resize', background: resizing?.panel === 'right' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
          onMouseDown={e => setResizing({ panel: 'right', startPos: e.clientX, startSize: rightWidth })}
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = 'var(--border-main)'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />
        <aside style={{ ...S.right, width: rightWidth }}>
          <div style={S.tabs}>
            {[
              { key: 'inspect', label: selected || selectedEdge ? 'Inspect' : 'Code' },
              { key: 'policy', label: 'Policy' },
              { key: 'scan', label: 'Scan' },
              ...(tool === 'ansible' ? [] : [{ key: 'modules', label: 'Modules' }]),
            ].map(t => (
              <button
                key={t.key}
                style={{
                  ...S.tab,
                  ...(rightTab === t.key ? { color: ct.color, borderBottomColor: ct.color } : {}),
                  fontSize: 10,
                }}
                onClick={() => setRightTab(t.key as typeof rightTab)}
              >
                {t.label}
              </button>
            ))}
          </div>
          {rightTab === 'inspect' && (
            <>
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
                      <button style={{ ...S.ftoggle, background: v ? ct.color + '33' : 'var(--bg-elev-2)', color: v ? ct.color : 'var(--text-muted)' }}
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
                  <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--border-soft)' }}>
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
              <span>FILE {codeFileLabel}</span>
              <button
                style={{ ...S.copyBtn, color: codeSaving || !syncCode.trim() ? '#555' : ct.color }}
                disabled={codeSaving || !syncCode.trim()}
                title="Save editor buffer to disk"
                onClick={() => saveCodeToDisk(syncCode)}
              >
                {codeSaving ? 'Saving...' : 'Save'}
              </button>
              <button style={{ ...S.copyBtn, color: ct.color }}
                onClick={() => navigator.clipboard?.writeText(syncCode)}>Copy</button>
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
                  filePath={concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || ct.ext}`}
                  readOnly={false}
                  onChange={setSyncCode}
                  onSave={saveCodeToDisk}
                />
              </div>
            </div>
          </div>
            </>
          )}
          {rightTab === 'policy' && (
            <div style={{ flex: 1, minHeight: 0 }}>
              <PolicyStudioPanel projectName={projectId} tool={tool} env={pulumiEnv} />
            </div>
          )}
          {rightTab === 'scan' && (
            <div style={{ flex: 1, minHeight: 0 }}>
              <ScanPanel projectName={projectId} tool={tool} />
            </div>
          )}
          {rightTab === 'modules' && tool !== 'ansible' && (
            <div style={{ flex: 1, minHeight: 0 }}>
              <ModuleRegistryPanel initialQuery="vpc" />
            </div>
          )}
        </aside>
      </div>

      {/* Bottom: Chat + Terminal */}
      {/* Bottom panel resize handle */}
      <div style={{ height: 4, cursor: 'row-resize', background: resizing?.panel === 'bottom' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
        onMouseDown={e => setResizing({ panel: 'bottom', startPos: e.clientY, startSize: bottomHeight })}
        onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = 'var(--border-main)'; }}
        onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />
      <div style={{ ...S.bottom, height: bottomHeight }}>
        <ChatPanel
          messages={chatMessages}
          input={chatInput}
          onInputChange={setChatInput}
          onSubmit={handleChat}
          loading={chatLoading}
          toolColor={ct.color}
          scrollAnchorRef={chatEndRef}
        />
        <TerminalPanel
          lines={terminalOutput}
          onClear={() => { setTerminalOutput([]); setLastCmdError(null); }}
          lastError={lastCmdError}
          fixLoading={fixLoading}
          toolColor={ct.color}
          onFix={lastCmdError ? async () => {
            setFixLoading(true);
            try {
              const provider = detectProvider();
              const result = await api.analyzePlan({
                tool: tool!,
                provider,
                command: lastCmdError.command,
                output: lastCmdError.output,
                exit_code: 1,
                canvas: nodes.map(n => ({ type: n.type, name: n.name })),
              });
              setTerminalOutput(prev => [...prev, '', `✦ AI Diagnosis: ${result.message}`]);
              if (result.fixes?.length > 0) {
                setTerminalOutput(prev => [...prev, `✦ Suggested fixes:`]);
                result.fixes.forEach(fix => {
                  setTerminalOutput(prev => [...prev, `  → ${fix.resource_type}.${fix.resource_name}: ${fix.field} = "${fix.new_value}" (${fix.reason})`]);
                  setNodes(prev => prev.map(n => {
                    if (n.type === fix.resource_type && n.name === fix.resource_name) {
                      return { ...n, properties: { ...n.properties, [fix.field]: fix.new_value } };
                    }
                    return n;
                  }));
                });
                setTerminalOutput(prev => [...prev, `✦ Fixes applied to canvas. Run plan again to verify.`]);
              }
              if (result.new_resources?.length > 0) {
                setTerminalOutput(prev => [...prev, `✦ Adding missing resources:`]);
                result.new_resources.forEach(r => {
                  setTerminalOutput(prev => [...prev, `  + ${r.type}.${r.name}`]);
                  const meta = catalogResources.find(c => c.type === r.type);
                  addNode({
                    type: r.type,
                    label: meta?.label ?? r.type,
                    icon: meta?.icon ?? '📦',
                    defaults: r.properties,
                  });
                });
              }
              setChatMessages(prev => [...prev, { role: 'ai', text: `Plan fix: ${result.message}` }]);
              setLastCmdError(null);
            } catch {
              setTerminalOutput(prev => [...prev, '✦ AI fix analysis failed. Check that Ollama is running.']);
            }
            setFixLoading(false);
          } : undefined}
        />
      </div>

      {/* Resource hover tooltip */}
      {hoveredResource && (
        <div style={{
          position: 'fixed', left: hoverPos.x, top: hoverPos.y,
          background: 'var(--bg-elev-2)', border: '1px solid var(--border-main)', borderRadius: 10,
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
            <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--border-soft)' }}>
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
