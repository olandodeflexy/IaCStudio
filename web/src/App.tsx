import { useState, useCallback, useRef, useEffect, useMemo } from 'react';
import { api, Resource, ToolInfo, CatalogResource, Suggestion, type CloudConnection } from './api';
import { useWebSocket, WSMessage } from './useWebSocket';
import { useHistory } from './useHistory';
import { useKeyboardShortcuts } from './useKeyboardShortcuts';
import type { AISettingsConfig } from './components/AISettings';
import type { RightPanelTab } from './components/Inspector';
import { ProjectLauncher, type SavedProject } from './components/ProjectLauncher';
import type { SidebarPanel } from './components/Sidebar';
import type { CanvasMode } from './components/Canvas';
import { WorkspaceShell } from './components/Workspace';
import type { LayeredProject } from './types';
import { envForTool, toolForEnv } from './projectLoad';
import { errorMessage } from './lib/errors';
import {
  TOOLS,
  ALL_TOOLS,
  FALLBACK_RESOURCES,
  generateLocalCode,
  type Edge,
} from './legacy';
import { edgesForResources, resourcesForEnv } from './app/layered';
import { useImportWorkflow } from './app/useImportWorkflow';
import { useProjectLifecycle } from './app/useProjectLifecycle';
import { useWorkspaceActions } from './app/useWorkspaceActions';
import type { AppResource, ChatMessage, ResizeState } from './app/types';

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
  const [showSettings, setShowSettings] = useState(false);
  const [aiSettings, setAiSettings] = useState<AISettingsConfig>({ type: 'ollama', endpoint: '', model: '', api_key: '' });
  const [savedProjects, setSavedProjects] = useState<SavedProject[]>([]);
  const { state: nodes, set: setNodes, undo: undoNodes, redo: redoNodes, canUndo, canRedo, reset: resetNodes } = useHistory<AppResource[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<{ fromId: string; x: number; y: number } | null>(null);
  const [chatMessages, setChatMessages] = useState<ChatMessage[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [activePanel, setActivePanel] = useState<SidebarPanel>('palette');
  const [rightTab, setRightTab] = useState<RightPanelTab>('inspect');
  const [projectLayoutMeta, setProjectLayoutMeta] = useState<Record<string, any> | null>(null);
  const [layeredProject, setLayeredProject] = useState<LayeredProject | null>(null);
  const [activeEnvironment, setActiveEnvironment] = useState<string | null>(null);
  const [canvasMode, setCanvasMode] = useState<CanvasMode>('freeform');
  const [selectedCloudConnection, setSelectedCloudConnection] = useState<CloudConnection | null>(null);

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
  const [resizing, setResizing] = useState<ResizeState | null>(null);

  const canvasRef = useRef<HTMLElement>(null);
  const chatEndRef = useRef<HTMLDivElement>(null);
  const isSyncing = useRef(false); // suppress file_changed echo from our own sync
  const hasCreatedProject = useRef(false);
  const initialLoadDone = useRef(false);
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

  const importWorkflow = useImportWorkflow({
    detectedTools,
    projectName,
    catalogResources,
    resetNodes,
    setEdges,
    setTool,
    setProjectId,
    setProjectLayoutMeta,
    setLayeredProject,
    setActiveEnvironment,
    setCanvasMode,
    setChatMessages,
    hasCreatedProject,
    initialLoadDone,
    showNotification,
    showPersistentNotification,
    clearNotification,
  });

  const activeEnvNodes = useMemo(
    () => resourcesForEnv(nodes, (tool === 'multi' || tool === 'pulumi') ? activeEnv : undefined),
    [nodes, activeEnv, tool],
  );
  const activeEnvEdges = useMemo(
    () => edgesForResources(edges, activeEnvNodes),
    [activeEnvNodes, edges],
  );

  const actions = useWorkspaceActions({
    tool,
    concreteTool,
    projectId,
    activeEnv,
    selectedCloudConnection,
    activeResourceFile,
    unresolvedHybridEnv,
    nodes,
    edges,
    catalogResources,
    chatMessages,
    chatInput,
    chatLoading,
    connecting,
    dragging,
    lastCmdError,
    canvasRef,
    setNodes,
    setEdges,
    setSelectedNode,
    setSelectedEdge,
    setConnecting,
    setDragging,
    setChatMessages,
    setChatInput,
    setChatLoading,
    setSuggestions,
    setTerminalOutput,
    setLastCmdError,
    setFixLoading,
    showNotification,
  });
  const { detectProvider, saveCodeToDisk: saveCodeWithContext } = actions;

  const saveCodeToDisk = useCallback((value: string) => {
    saveCodeWithContext(value, { setCodeSaving, isSyncing, showNotification });
  }, [saveCodeWithContext, showNotification]);

  const {
    setImportLoading,
    setImportPreview,
    topologyProvider,
  } = importWorkflow;

  const projectLifecycle = useProjectLifecycle({
    savedProject: saved.current,
    tool,
    projectId,
    projectName,
    projectLayoutMeta,
    nodes,
    edges,
    hasCreatedProject,
    initialLoadDone,
    resetNodes,
    setDetectedTools,
    setSavedProjects,
    setProjectName,
    setProjectId,
    setTool,
    setEdges,
    setChatMessages,
    setTerminalOutput,
    setProjectLayoutMeta,
    setLayeredProject,
    setActiveEnvironment,
    setCanvasMode,
    showNotification,
  });

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
  }, [clearNotification, setImportLoading, setImportPreview, showNotification, showPersistentNotification, topologyProvider]);

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
        actions.removeNode(selectedNode);
      }
    },
    'backspace': () => {
      if (selectedEdge) {
        setEdges(prev => prev.filter(e => e.id !== selectedEdge));
        setSelectedEdge(null);
      } else if (selectedNode) {
        actions.removeNode(selectedNode);
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

  // Fetch suggestions when canvas changes
  useEffect(() => {
    if (!tool) return;
    const provider = detectProvider();
    const canvas = nodes.map(n => ({ type: n.type, name: n.name }));
    api.suggest(tool, provider, canvas).then(setSuggestions).catch(() => setSuggestions([]));
  }, [detectProvider, nodes, tool]);

  const handleDeleteSavedProject = useCallback(async (name: string) => {
    try {
      await api.deleteProject(name);
      setSavedProjects(prev => prev.filter(project => project.name !== name));
      showNotification(`Deleted project: ${name}`);
    } catch (err: unknown) {
      showNotification(`Failed to delete: ${errorMessage(err, 'Unable to delete project')}`, 4000);
    }
  }, [showNotification]);

  const openAISettings = useCallback(() => {
    api.getAISettings().then(setAiSettings).catch(() => {});
    setShowSettings(true);
  }, []);

  // ─── Tool Selection ───
  if (!tool) {
    const iw = importWorkflow;
    return (
      <ProjectLauncher
        savedProjects={savedProjects}
        detectedTools={detectedTools}
        projectName={projectName}
        showImportWizard={iw.showImportWizard}
        importTab={iw.importTab}
        browsePath={iw.browsePath}
        browseParent={iw.browseParent}
        browseEntries={iw.browseEntries}
        importPreview={iw.importPreview}
        importLoading={iw.importLoading}
        topologyDesc={iw.topologyDesc}
        topologyProvider={iw.topologyProvider}
        visionImages={iw.visionImages}
        visionError={iw.visionError}
        catalogResources={catalogResources}
        onProjectNameChange={setProjectName}
        onCreateProject={projectLifecycle.handleCreateProject}
        onOpenProject={projectLifecycle.openProject}
        onRevealProject={(name) => api.revealProject(name).catch(() => {})}
        onDeleteProject={handleDeleteSavedProject}
        onStartImportBrowse={iw.startImportBrowse}
        onStartTopology={iw.startTopologyBuilder}
        onImportTabChange={iw.setImportTab}
        onBrowseLoaded={iw.handleBrowseLoaded}
        onImportPreviewChange={iw.setImportPreview}
        onImportLoadingChange={iw.setImportLoading}
        onTopologyDescChange={iw.setTopologyDesc}
        onTopologyProviderChange={iw.setTopologyProvider}
        onVisionImagesChange={iw.setVisionImages}
        onVisionErrorChange={iw.setVisionError}
        onGenerateTopology={iw.handleGenerateTopology}
        onImportToCanvas={iw.handleImportToCanvas}
        onCloseImportWizard={iw.closeImportWizard}
      />
    );
  }

  const ct = ALL_TOOLS[tool] || TOOLS[concreteTool] || TOOLS.terraform;
  const codeFileLabel = concreteTool === 'pulumi'
    ? (activeEnv ? `environments/${activeEnv}/index.ts` : 'index.ts')
    : (tool === 'multi' && activeEnv ? `environments/${activeEnv}/main${TOOLS[concreteTool]?.ext || '.tf'}` : `main${TOOLS[concreteTool]?.ext || ct.ext}`);
  const codeEditorFilePath = concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || ct.ext}`;

  return (
    <WorkspaceShell
      notification={notification}
      tool={tool}
      toolMeta={ct}
      projectName={projectName}
      projectId={projectId}
      nodes={nodes}
      edges={edges}
      wsConnected={wsConnected}
      canUndo={canUndo}
      canRedo={canRedo}
      showSettings={showSettings}
      aiSettings={aiSettings}
      sidebarWidth={sidebarWidth}
      rightWidth={rightWidth}
      bottomHeight={bottomHeight}
      resizing={resizing}
      activePanel={activePanel}
      rightTab={rightTab}
      searchQuery={searchQuery}
      provider={detectProvider()}
      catalogResources={catalogResources}
      suggestions={suggestions}
      selectedNode={selectedNode}
      selectedEdge={selectedEdge}
      connecting={connecting}
      layeredProject={layeredProject}
      showEnvironmentSelector={showEnvironmentSelector}
      activeEnvironment={activeEnvironment}
      canvasMode={canvasMode}
      canvasRef={canvasRef}
      chatMessages={chatMessages}
      chatInput={chatInput}
      chatLoading={chatLoading}
      chatEndRef={chatEndRef}
      terminalOutput={terminalOutput}
      lastCmdError={lastCmdError}
      fixLoading={fixLoading}
      syncCode={syncCode}
      codeFileLabel={codeFileLabel}
      codeEditorFilePath={codeEditorFilePath}
      codeSaving={codeSaving}
      activeEnv={activeEnv}
      selectedCloudConnection={selectedCloudConnection}
      unresolvedHybridEnv={unresolvedHybridEnv}
      hoveredResource={hoveredResource}
      hoverPos={hoverPos}
      onBack={projectLifecycle.handleBackToProjectSelect}
      onProjectNameChange={setProjectName}
      onRevealProject={(id) => api.revealProject(id).catch(() => {})}
      onUndo={undoNodes}
      onRedo={redoNodes}
      onRunCommand={actions.runCmd}
      onOpenSettings={openAISettings}
      onSettingsChange={setAiSettings}
      onSettingsNotify={showNotification}
      onCloseSettings={() => setShowSettings(false)}
      onResizeStart={setResizing}
      onActivePanelChange={setActivePanel}
      onSearchQueryChange={setSearchQuery}
      onAddResource={actions.addNode}
      onResourceHover={(resource, position) => {
        setHoverPos(position);
        setHoveredResource(resource);
      }}
      onResourceHoverEnd={() => setHoveredResource(null)}
      onMouseMove={actions.onMouseMove}
      onDragEnd={actions.onMouseUp}
      onConnectionCancel={actions.handleCancelConnection}
      onNodeDragStart={actions.onMouseDown}
      onStartConnection={actions.handleStartConnection}
      onCompleteConnection={actions.handleCompleteConnection}
      onSelectNode={actions.handleSelectNode}
      onSelectEdge={actions.handleSelectEdge}
      onClearSelection={actions.handleClearCanvasSelection}
      onDeleteNode={actions.removeNode}
      onActiveEnvironmentChange={setActiveEnvironment}
      onCloudConnectionSelect={setSelectedCloudConnection}
      onCanvasModeChange={setCanvasMode}
      onRightTabChange={setRightTab}
      onDeleteEdge={(edgeId) => {
        setEdges(prev => prev.filter(edge => edge.id !== edgeId));
        setSelectedEdge(null);
      }}
      onUpdateNodeName={actions.updateName}
      onUpdateNodeProp={actions.updateProp}
      onSyncCodeChange={setSyncCode}
      onSaveCode={saveCodeToDisk}
      onChatInputChange={setChatInput}
      onChatSubmit={actions.handleChat}
      onClearTerminal={() => { setTerminalOutput([]); setLastCmdError(null); }}
      onFixLastError={actions.fixLastError}
    />
  );
}
