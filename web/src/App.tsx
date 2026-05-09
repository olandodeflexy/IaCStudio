import { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { api, ApiError, Resource, ToolInfo, CatalogResource, Suggestion, FileEntry, ImportResult, type PolicyFinding } from './api';
import { useWebSocket, WSMessage } from './useWebSocket';
import { useHistory } from './useHistory';
import { useKeyboardShortcuts } from './useKeyboardShortcuts';
import { AISettingsModal, type AISettingsConfig } from './components/AISettings';
import { AppHeader } from './components/AppHeader';
import { ChatPanel } from './components/Chat';
import { InspectorPanel, type RightPanelTab } from './components/Inspector';
import { ProjectLauncher, type SavedProject } from './components/ProjectLauncher';
import { WorkspaceSidebar, type SidebarPanel } from './components/Sidebar';
import { TerminalPanel } from './components/Terminal';
import { CanvasPanel, type CanvasMode } from './components/Canvas';
import { S } from './styles';
import type { LayeredProject, LayeredModule } from './types';
import { envForResourceLoad, envForTool, shouldParseResourcesFromDisk, toolForEnv } from './projectLoad';
import { errorMessage } from './lib/errors';
import {
  TOOLS,
  ALL_TOOLS,
  FALLBACK_RESOURCES,
  uid,
  edgeId,
  generateLocalCode,
  type Edge,
} from './legacy';

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

export const normalizeLayeredProject = (state: any): LayeredProject | null => {
  if (state?.layout !== 'layered-v1') return null;
  const environments = Array.isArray(state.environments)
    ? state.environments.filter((env: unknown): env is string => typeof env === 'string' && env.length > 0)
    : [];
  if (environments.length === 0) return null;
  const rawEnvironmentTools = state.environment_tools;
  let environmentTools: Record<string, string> | undefined;
  if (
    rawEnvironmentTools &&
    typeof rawEnvironmentTools === 'object' &&
    !Array.isArray(rawEnvironmentTools) &&
    [Object.prototype, null].includes(Object.getPrototypeOf(rawEnvironmentTools))
  ) {
    const normalizedEnvironmentTools = Object.create(null) as Record<string, string>;
    for (const [env, envTool] of Object.entries(rawEnvironmentTools)) {
      if (environments.includes(env) && typeof envTool === 'string' && envTool.length > 0) {
        normalizedEnvironmentTools[env] = envTool;
      }
    }
    if (Object.keys(normalizedEnvironmentTools).length > 0) {
      environmentTools = normalizedEnvironmentTools;
    }
  }

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

export const resourceEnv = (resource: { file?: string }) => {
  if (!resource.file) return null;
  const parts = resource.file.replace(/\\/g, '/').split('/');
  const envIdx = parts.indexOf('environments');
  return envIdx >= 0 && parts.length > envIdx + 1 ? parts[envIdx + 1] : null;
};

export const resourcesForEnv = <T extends { id: string; file?: string }>(resources: T[], env?: string) => {
  if (!env) return resources;
  return resources.filter(resource => {
    const envFromFile = resourceEnv(resource);
    return envFromFile === env;
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
  const [aiSettings, setAiSettings] = useState<AISettingsConfig>({ type: 'ollama', endpoint: '', model: '', api_key: '' });
  const [savedProjects, setSavedProjects] = useState<SavedProject[]>([]);
  const { state: nodes, set: setNodes, undo: undoNodes, redo: redoNodes, canUndo, canRedo, reset: resetNodes } = useHistory<(Resource & { x: number; y: number; icon: string; label: string })[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<{ fromId: string; x: number; y: number } | null>(null);
  const [chatMessages, setChatMessages] = useState<{ role: string; text: string }[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [activePanel, setActivePanel] = useState<SidebarPanel>('palette');
  const [rightTab, setRightTab] = useState<RightPanelTab>('inspect');
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

  const canvasRef = useRef<HTMLElement>(null);
  const chatEndRef = useRef<HTMLDivElement>(null);
  const isSyncing = useRef(false); // suppress file_changed echo from our own sync
  const notificationTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showEnvironmentSelector = Boolean(layeredProject && (tool === 'multi' || layeredProject.environmentTools));
  const activeEnv = envForTool(tool || '', layeredProject, activeEnvironment);
  const activeTool = toolForEnv(tool || '', layeredProject, activeEnv);
  const unresolvedHybridEnv = tool === 'multi' && Boolean(activeEnv) && !activeTool;
  const concreteTool = activeTool && activeTool !== 'multi'
    ? activeTool
    : (tool && tool !== 'multi' ? tool : 'terraform');
  const activeResourceFile = unresolvedHybridEnv
    ? undefined
    : concreteTool === 'pulumi'
      ? (activeEnv ? `environments/${activeEnv}/index.ts` : undefined)
      : (tool === 'multi' && activeEnv ? `environments/${activeEnv}/main${TOOLS[concreteTool]?.ext || '.tf'}` : undefined);

  const clearNotificationTimer = useCallback(() => {
    if (notificationTimer.current !== null) {
      clearTimeout(notificationTimer.current);
      notificationTimer.current = null;
    }
  }, []);

  const showNotification = useCallback((message: string, duration = 3000) => {
    clearNotificationTimer();
    setNotification(message);
    notificationTimer.current = setTimeout(() => {
      setNotification(null);
      notificationTimer.current = null;
    }, duration);
  }, [clearNotificationTimer]);

  const showPersistentNotification = useCallback((message: string) => {
    clearNotificationTimer();
    setNotification(message);
  }, [clearNotificationTimer]);

  const clearNotification = useCallback(() => {
    clearNotificationTimer();
    setNotification(null);
  }, [clearNotificationTimer]);

  useEffect(() => () => {
    clearNotificationTimer();
  }, [clearNotificationTimer]);

  const activeEnvNodes = useMemo(
    () => resourcesForEnv(nodes, (tool === 'multi' || tool === 'pulumi') ? activeEnv : undefined),
    [nodes, activeEnv, tool],
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
  const openProject = useCallback(async (proj: SavedProject) => {
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
      showNotification(`Opened project: ${proj.name}`);
    } catch {
      // No saved state — start fresh
      setProjectLayoutMeta(null);
      setLayeredProject(null);
      setActiveEnvironment(null);
      setCanvasMode('freeform');
    }
  }, [applyParsedResources, applyProjectState, showNotification]);

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
      showPersistentNotification(msg.message || 'AI is working...');
    }
    if (msg.type === 'ai_topology_result') {
      setImportLoading(false);
      clearNotification();
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
      showNotification(`File updated: ${msg.file?.split('/').pop()}`);
    }
  }, [clearNotification, showNotification, showPersistentNotification, topologyProvider]);

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
    if (!tool || unresolvedHybridEnv || !nodes.length) {
      setSyncCode('');
      return;
    }
    const code = generateLocalCode(concreteTool, activeEnvNodes, activeEnvEdges);
    setSyncCode(code);
  }, [activeEnvEdges, activeEnvNodes, concreteTool, tool, unresolvedHybridEnv]);

  // Sync to disk (debounced) — syncs even when nodes is empty so that
  // deleting the last resource clears the generated file on disk.
  const syncTimer = useRef<ReturnType<typeof setTimeout>>();
  const pendingSync = useRef<{ projectId: string; tool: string; nodes: Resource[]; edges: Edge[]; env?: string } | null>(null);
  const hasCreatedProject = useRef(false);
  const initialLoadDone = useRef(false);
  const syncScope = `${projectId}:${tool || ''}:${activeEnv || ''}`;
  const lastSyncScope = useRef('');
  const flushPendingSync = useCallback(() => {
    const snapshot = pendingSync.current;
    if (!snapshot) return;
    pendingSync.current = null;
    syncTimer.current = undefined;
    isSyncing.current = true;
    api.syncToDisk(snapshot.projectId, snapshot.tool, snapshot.nodes, snapshot.edges, snapshot.env).catch(() => {}).finally(() => {
      setTimeout(() => { isSyncing.current = false; }, 1500);
    });
  }, []);
  useEffect(() => {
    if (!tool || !hasCreatedProject.current || !projectId) return;
    if (unresolvedHybridEnv) {
      clearTimeout(syncTimer.current);
      syncTimer.current = undefined;
      pendingSync.current = null;
      return;
    }
    // Skip the first sync after opening a project — the restored state
    // doesn't need to be written back immediately (it came from disk).
    if (!initialLoadDone.current) {
      initialLoadDone.current = true;
      lastSyncScope.current = syncScope;
      return;
    }
    if (lastSyncScope.current && lastSyncScope.current !== syncScope) {
      clearTimeout(syncTimer.current);
      syncTimer.current = undefined;
      flushPendingSync();
      lastSyncScope.current = syncScope;
      return;
    }
    lastSyncScope.current = syncScope;
    clearTimeout(syncTimer.current);
    pendingSync.current = { projectId, tool, nodes: activeEnvNodes, edges: activeEnvEdges, env: activeEnv };
    syncTimer.current = setTimeout(() => {
      flushPendingSync();
    }, 2000);
  }, [activeEnvEdges, activeEnvNodes, flushPendingSync, projectId, activeEnv, syncScope, tool, unresolvedHybridEnv]);

  // ─── Handlers ───

  const addNode = useCallback((resourceDef: any) => {
    if (unresolvedHybridEnv) {
      showNotification(`Environment "${activeEnv}" has no configured IaC tool`, 4000);
      return;
    }
    const node = {
      id: uid(),
      type: resourceDef.type,
      name: resourceDef.type.replace(/^(aws_|google_|azurerm_)/, '').replace(/^compute_|^container_/, ''),
      label: resourceDef.label,
      icon: resourceDef.icon,
      properties: { ...(resourceDef.defaults || {}) },
      file: activeResourceFile,
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
  }, [activeEnv, activeResourceFile, catalogResources, showNotification, unresolvedHybridEnv]);

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

  const handleSelectNode = useCallback((id: string) => {
    setSelectedNode(id);
    setSelectedEdge(null);
  }, []);

  const handleSelectEdge = useCallback((id: string) => {
    setSelectedEdge(id);
    setSelectedNode(null);
  }, []);

  const handleClearCanvasSelection = useCallback(() => {
    setSelectedNode(null);
    setSelectedEdge(null);
  }, []);

  const handleStartConnection = useCallback((nodeId: string, position: { x: number; y: number }) => {
    setConnecting({ fromId: nodeId, ...position });
  }, []);

  const handleCancelConnection = useCallback(() => {
    setConnecting(null);
  }, []);

  const handleCompleteConnection = useCallback((targetNodeId: string) => {
    if (!connecting || connecting.fromId === targetNodeId) return;
    const fromNode = nodes.find(n => n.id === connecting.fromId);
    const toNode = nodes.find(n => n.id === targetNodeId);
    if (!fromNode || !toNode) return;

    const catEntry = catalogResources.find(c => c.type === fromNode.type);
    let field = 'depends_on';
    if (catEntry?.connects_via) {
      const match = Object.entries(catEntry.connects_via).find(([, t]) => t === toNode.type);
      if (match) field = match[0];
    }
    const newEdge: Edge = {
      id: edgeId(connecting.fromId, targetNodeId, field),
      from: connecting.fromId,
      to: targetNodeId,
      fromType: fromNode.type,
      toType: toNode.type,
      field,
      label: field.replace(/_/g, ' '),
    };
    setEdges(prev => {
      if (prev.some(e => e.from === newEdge.from && e.to === newEdge.to && e.field === newEdge.field)) return prev;
      return [...prev, newEdge];
    });
    setConnecting(null);
  }, [catalogResources, connecting, nodes]);

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
    if (unresolvedHybridEnv) {
      setTerminalOutput(prev => [...prev, `Error: environment "${activeEnv}" has no configured IaC tool`]);
      return;
    }
    // apply/destroy require explicit confirmation
    const needsApproval = command === 'apply' || command === 'destroy';
    if (needsApproval && !confirm(`Are you sure you want to run "${command}"? This will modify real infrastructure.`)) {
      return;
    }
    setTerminalOutput(prev => [...prev, `$ ${command}`, '']);
    api.runCommand(projectId, tool, command, {
      approved: needsApproval,
      env: activeEnv,
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
          env: activeEnv,
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

  const handleBrowseLoaded = useCallback((path: string, entries: FileEntry[], parent: string) => {
    setBrowsePath(path);
    setBrowseEntries(entries);
    setBrowseParent(parent);
  }, []);

  const loadBrowsePath = useCallback(async (path?: string) => {
    try {
      const result = await api.browse(path);
      handleBrowseLoaded(result.path, result.entries, result.parent);
    } catch {
      // Preserve local-only behavior when the backend browse endpoint is unavailable.
    }
  }, [handleBrowseLoaded]);

  const startImportBrowse = useCallback(() => {
    setImportTab('browse');
    setVisionImages([]);
    setVisionError(null);
    setShowImportWizard(true);
    loadBrowsePath();
  }, [loadBrowsePath]);

  const startTopologyBuilder = useCallback(() => {
    setImportTab('topology');
    setShowImportWizard(true);
  }, []);

  const handleDeleteSavedProject = useCallback(async (name: string) => {
    try {
      await api.deleteProject(name);
      setSavedProjects(prev => prev.filter(project => project.name !== name));
      showNotification(`Deleted project: ${name}`);
    } catch (err: unknown) {
      showNotification(`Failed to delete: ${errorMessage(err, 'Unable to delete project')}`, 4000);
    }
  }, [showNotification]);

  const handleGenerateTopology = async () => {
    const toolKey = detectedTools.find(t => t.available && t.name !== 'Ansible')?.name === 'OpenTofu' ? 'opentofu' : 'terraform';

    if (visionImages.length > 0) {
      setImportLoading(true);
      showPersistentNotification('AI is reading your diagram...');
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
        clearNotification();
      }
      return;
    }

    if (!topologyDesc.trim()) return;
    setImportLoading(true);
    showPersistentNotification('AI is designing your infrastructure...');
    try {
      // Fire and forget — result arrives via WebSocket
      await api.generateTopology(topologyDesc, toolKey, topologyProvider);
      // Don't setImportLoading(false) here — WebSocket handler does it
    } catch (e: any) {
      const message = e?.message || 'Generation failed';
      setImportPreview({ tool: 'unknown', provider: 'unknown', files: [], resources: [], edges: [], summary: message, warnings: [message] });
      setImportLoading(false);
      clearNotification();
    }
  };

  const handleImportToCanvas = useCallback(async (preview: ImportResult) => {
    const selectedTool = preview.tool === 'opentofu' ? 'opentofu' : preview.tool === 'ansible' ? 'ansible' : 'terraform';
    try {
      await api.createProject(projectName, selectedTool);
    } catch (err: unknown) {
      showNotification(`Import failed: ${errorMessage(err, 'Unable to create project')}`, 5000);
      return;
    }
    setTool(selectedTool);
    setProjectId(projectName);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    hasCreatedProject.current = true;
    initialLoadDone.current = true;

    const catalogByType = new Map(catalogResources.map(resource => [resource.type, resource]));
    const generatedIdPrefix = `imp_${Date.now()}`;
    const imported = preview.resources.map((resource, index) => {
      const id = resource.id || `${generatedIdPrefix}_${index}`;
      const meta = catalogByType.get(resource.type);
      const { file: _file, line: _line, ...rest } = resource;
      return {
        ...rest,
        id,
        x: 80 + (index % 5) * 200,
        y: 80 + Math.floor(index / 5) * 130,
        icon: meta?.icon ?? '📦',
        label: meta?.label ?? resource.type,
      };
    });
    resetNodes(imported);

    const nodeTypeById = new Map(imported.map(resource => [resource.id, resource.type]));
    const newEdges = preview.edges.flatMap(edge => {
      const from = edge.from_id;
      const to = edge.to_id;
      const fromType = nodeTypeById.get(from);
      const toType = nodeTypeById.get(to);
      if (!fromType || !toType) return [];
      return [{
        id: edgeId(from, to, edge.field),
        from,
        to,
        fromType,
        toType,
        field: edge.field,
        label: edge.field.replace(/_/g, ' '),
      }];
    });
    setEdges(newEdges);

    setShowImportWizard(false);
    setImportPreview(null);
    setVisionImages([]);
    setVisionError(null);
    showNotification(`Imported ${preview.resources.length} resources`, 4000);
  }, [catalogResources, projectName, resetNodes, showNotification]);

  const saveCodeToDisk = useCallback(async (value: string) => {
    if (!tool || !projectId) return;
    if (unresolvedHybridEnv) {
      showNotification(`Save failed: environment "${activeEnv}" has no configured IaC tool`, 5000);
      return;
    }
    if (!value.trim()) {
      showNotification('Nothing to save yet');
      return;
    }
    setCodeSaving(true);
    isSyncing.current = true;
    try {
      const fileName = concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || '.tf'}`;
      await api.syncCodeToDisk(projectId, tool, value, fileName, activeEnv);
      showNotification(`Saved ${fileName}`);
    } catch (err: any) {
      showNotification(`Save failed: ${err.message}`, 5000);
    } finally {
      setCodeSaving(false);
      setTimeout(() => { isSyncing.current = false; }, 1500);
    }
  }, [activeEnv, concreteTool, projectId, showNotification, tool, unresolvedHybridEnv]);

  const handleBackToProjectSelect = useCallback(async () => {
    if (projectId && hasCreatedProject.current) {
      await api.saveState(projectId, buildPersistedState()).catch(() => {});
    }
    api.listProjectStates().then(setSavedProjects).catch(() => {});
    setTool(null);
    resetNodes([]);
    setEdges([]);
    setChatMessages([]);
    setTerminalOutput([]);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    initialLoadDone.current = false;
    hasCreatedProject.current = false;
  }, [buildPersistedState, projectId, resetNodes]);

  const openAISettings = useCallback(() => {
    api.getAISettings().then(setAiSettings).catch(() => {});
    setShowSettings(true);
  }, []);

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
      <ProjectLauncher
        savedProjects={savedProjects}
        detectedTools={detectedTools}
        projectName={projectName}
        showImportWizard={showImportWizard}
        importTab={importTab}
        browsePath={browsePath}
        browseParent={browseParent}
        browseEntries={browseEntries}
        importPreview={importPreview}
        importLoading={importLoading}
        topologyDesc={topologyDesc}
        topologyProvider={topologyProvider}
        visionImages={visionImages}
        visionError={visionError}
        catalogResources={catalogResources}
        onProjectNameChange={setProjectName}
        onCreateProject={handleCreateProject}
        onOpenProject={openProject}
        onRevealProject={(name) => api.revealProject(name).catch(() => {})}
        onDeleteProject={handleDeleteSavedProject}
        onStartImportBrowse={startImportBrowse}
        onStartTopology={startTopologyBuilder}
        onImportTabChange={setImportTab}
        onBrowseLoaded={handleBrowseLoaded}
        onImportPreviewChange={setImportPreview}
        onImportLoadingChange={setImportLoading}
        onTopologyDescChange={setTopologyDesc}
        onTopologyProviderChange={setTopologyProvider}
        onVisionImagesChange={setVisionImages}
        onVisionErrorChange={setVisionError}
        onGenerateTopology={handleGenerateTopology}
        onImportToCanvas={handleImportToCanvas}
        onCloseImportWizard={closeImportWizard}
      />
    );
  }

  const ct = ALL_TOOLS[tool] || TOOLS[concreteTool] || TOOLS.terraform;
  const codeFileLabel = concreteTool === 'pulumi'
    ? (activeEnv ? `environments/${activeEnv}/index.ts` : 'index.ts')
    : (tool === 'multi' && activeEnv ? `environments/${activeEnv}/main${TOOLS[concreteTool]?.ext || '.tf'}` : `main${TOOLS[concreteTool]?.ext || ct.ext}`);
  const codeEditorFilePath = concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || ct.ext}`;

  // ─── Main UI ───
  return (
    <div style={S.app} className="iac-app">
      {/* Notification */}
      {notification && (
        <div style={S.notification}>{notification}</div>
      )}

      <AppHeader
        tool={tool}
        toolMeta={ct}
        projectName={projectName}
        projectId={projectId}
        resourceCount={nodes.length}
        wsConnected={wsConnected}
        canUndo={canUndo}
        canRedo={canRedo}
        onBack={handleBackToProjectSelect}
        onProjectNameChange={setProjectName}
        onRevealProject={(id) => api.revealProject(id).catch(() => {})}
        onUndo={undoNodes}
        onRedo={redoNodes}
        onRunCommand={runCmd}
        onOpenSettings={openAISettings}
      />

      {/* AI Settings Modal */}
      {showSettings && (
        <AISettingsModal
          settings={aiSettings}
          onSettingsChange={setAiSettings}
          onNotify={showNotification}
          onClose={() => setShowSettings(false)}
        />
      )}

      <div style={S.main}>
        {/* Sidebar — resizable */}
        <WorkspaceSidebar
          width={sidebarWidth}
          activePanel={activePanel}
          tool={tool}
          toolMeta={ct}
          projectName={projectName}
          provider={detectProvider()}
          resources={catalogResources}
          suggestions={suggestions}
          searchQuery={searchQuery}
          onActivePanelChange={setActivePanel}
          onSearchQueryChange={setSearchQuery}
          onAddResource={addNode}
          onResourceHover={(resource, position) => {
            setHoverPos(position);
            setHoveredResource(resource);
          }}
          onResourceHoverEnd={() => setHoveredResource(null)}
        />
        {/* Sidebar resize handle */}
        <div style={{ width: 4, cursor: 'col-resize', background: resizing?.panel === 'sidebar' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
          onMouseDown={e => setResizing({ panel: 'sidebar', startPos: e.clientX, startSize: sidebarWidth })}
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = 'var(--border-main)'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />

        <CanvasPanel
          canvasRef={canvasRef}
          nodes={nodes}
          edges={edges}
          selectedNodeId={selectedNode}
          selectedEdgeId={selectedEdge}
          connecting={connecting}
          layeredProject={layeredProject}
          showEnvironmentSelector={showEnvironmentSelector}
          activeEnvironment={activeEnvironment}
          canvasMode={canvasMode}
          toolMeta={ct}
          onMouseMove={onMouseMove}
          onDragEnd={onMouseUp}
          onConnectionCancel={handleCancelConnection}
          onNodeDragStart={onMouseDown}
          onStartConnection={handleStartConnection}
          onCompleteConnection={handleCompleteConnection}
          onSelectNode={handleSelectNode}
          onSelectEdge={handleSelectEdge}
          onClearSelection={handleClearCanvasSelection}
          onDeleteNode={removeNode}
          onActiveEnvironmentChange={setActiveEnvironment}
          onCanvasModeChange={setCanvasMode}
        />

        {/* Right Panel */}
        {/* Right panel resize handle */}
        <div style={{ width: 4, cursor: 'col-resize', background: resizing?.panel === 'right' ? ct.color + '44' : 'transparent', flexShrink: 0, transition: 'background 0.15s' }}
          onMouseDown={e => setResizing({ panel: 'right', startPos: e.clientX, startSize: rightWidth })}
          onMouseEnter={e => { if (!resizing) (e.currentTarget as any).style.background = 'var(--border-main)'; }}
          onMouseLeave={e => { if (!resizing) (e.currentTarget as any).style.background = 'transparent'; }} />
        <InspectorPanel
          width={rightWidth}
          activeTab={rightTab}
          tool={tool}
          toolMeta={ct}
          activeEnv={activeEnv}
          projectId={projectId}
          nodes={nodes}
          edges={edges}
          selectedNodeId={selectedNode}
          selectedEdgeId={selectedEdge}
          syncCode={syncCode}
          codeFileLabel={codeFileLabel}
          codeEditorFilePath={codeEditorFilePath}
          codeSaving={codeSaving}
          unresolvedHybridEnv={unresolvedHybridEnv}
          onTabChange={setRightTab}
          onDeleteEdge={(edgeId) => {
            setEdges(prev => prev.filter(edge => edge.id !== edgeId));
            setSelectedEdge(null);
          }}
          onSelectEdge={setSelectedEdge}
          onUpdateNodeName={updateName}
          onUpdateNodeProp={updateProp}
          onSyncCodeChange={setSyncCode}
          onSaveCode={saveCodeToDisk}
        />
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
