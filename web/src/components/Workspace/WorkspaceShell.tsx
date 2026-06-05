import type { Dispatch, MouseEvent, RefObject, SetStateAction } from 'react';
import type { AISettingsConfig } from '../AISettings';
import { AISettingsModal } from '../AISettings';
import { AppHeader } from '../AppHeader';
import { CanvasPanel, type CanvasMode } from '../Canvas';
import { ChatPanel } from '../Chat';
import { InspectorPanel, type RightPanelTab } from '../Inspector';
import { ResourceTooltip } from '../ResourceTooltip';
import { WorkspaceSidebar, type SidebarPanel } from '../Sidebar';
import { TerminalPanel } from '../Terminal';
import type { CatalogResource, CloudConnection, Suggestion } from '../../api';
import type { Edge } from '../../legacy';
import { S } from '../../styles';
import type { LayeredProject } from '../../types';
import type { AppResource, ChatMessage, ResizeState } from '../../app/types';
import { ResizeHandle } from './ResizeHandle';

interface ToolMeta {
  color: string;
  ext: string;
}

export interface WorkspaceShellProps {
  notification: string | null;
  tool: string;
  toolMeta: ToolMeta;
  projectName: string;
  projectId: string;
  nodes: AppResource[];
  edges: Edge[];
  wsConnected: boolean;
  canUndo: boolean;
  canRedo: boolean;
  showSettings: boolean;
  aiSettings: AISettingsConfig;
  sidebarWidth: number;
  rightWidth: number;
  bottomHeight: number;
  resizing: ResizeState | null;
  activePanel: SidebarPanel;
  rightTab: RightPanelTab;
  searchQuery: string;
  provider: string;
  catalogResources: CatalogResource[];
  suggestions: Suggestion[];
  selectedNode: string | null;
  selectedEdge: string | null;
  connecting: { fromId: string; x: number; y: number } | null;
  layeredProject: LayeredProject | null;
  showEnvironmentSelector: boolean;
  activeEnvironment: string | null;
  canvasMode: CanvasMode;
  canvasRef: RefObject<HTMLElement>;
  chatMessages: ChatMessage[];
  chatInput: string;
  chatLoading: boolean;
  chatEndRef: RefObject<HTMLDivElement>;
  terminalOutput: string[];
  lastCmdError: { command: string; output: string } | null;
  fixLoading: boolean;
  syncCode: string;
  codeFileLabel: string;
  codeEditorFilePath: string;
  codeSaving: boolean;
  activeEnv: string | null;
  selectedCloudConnection: CloudConnection | null;
  unresolvedHybridEnv: boolean;
  hoveredResource: CatalogResource | null;
  hoverPos: { x: number; y: number };
  onBack: () => void;
  onProjectNameChange: (_name: string) => void;
  onRevealProject: (_projectId: string) => void;
  onUndo: () => void;
  onRedo: () => void;
  onRunCommand: (_command: string) => void;
  onOpenSettings: () => void;
  onSettingsChange: Dispatch<SetStateAction<AISettingsConfig>>;
  onSettingsNotify: (_message: string, _duration?: number) => void;
  onCloseSettings: () => void;
  onResizeStart: (_state: ResizeState) => void;
  onActivePanelChange: (_panel: SidebarPanel) => void;
  onSearchQueryChange: (_query: string) => void;
  onAddResource: (_resource: CatalogResource) => void;
  onResourceHover: (_resource: CatalogResource, _position: { x: number; y: number }) => void;
  onResourceHoverEnd: () => void;
  onMouseMove: (_event: MouseEvent) => void;
  onDragEnd: () => void;
  onConnectionCancel: () => void;
  onNodeDragStart: (_event: MouseEvent, _nodeId: string) => void;
  onStartConnection: (_nodeId: string, _position: { x: number; y: number }) => void;
  onCompleteConnection: (_targetNodeId: string) => void;
  onSelectNode: (_nodeId: string) => void;
  onSelectEdge: (_edgeId: string) => void;
  onClearSelection: () => void;
  onDeleteNode: (_nodeId: string) => void;
  onActiveEnvironmentChange: (_env: string) => void;
  onCloudConnectionSelect: (_connection: CloudConnection | null) => void;
  onCanvasModeChange: (_mode: CanvasMode) => void;
  onRightTabChange: (_tab: RightPanelTab) => void;
  onDeleteEdge: (_edgeId: string) => void;
  onUpdateNodeName: (_nodeId: string, _name: string) => void;
  onUpdateNodeProp: (_nodeId: string, _key: string, _value: any) => void;
  onSyncCodeChange: (_value: string) => void;
  onSaveCode: (_value: string) => void;
  onChatInputChange: (_value: string) => void;
  onChatSubmit: () => void;
  onClearTerminal: () => void;
  onFixLastError?: () => void;
}

export function WorkspaceShell({
  notification,
  tool,
  toolMeta,
  projectName,
  projectId,
  nodes,
  edges,
  wsConnected,
  canUndo,
  canRedo,
  showSettings,
  aiSettings,
  sidebarWidth,
  rightWidth,
  bottomHeight,
  resizing,
  activePanel,
  rightTab,
  searchQuery,
  provider,
  catalogResources,
  suggestions,
  selectedNode,
  selectedEdge,
  connecting,
  layeredProject,
  showEnvironmentSelector,
  activeEnvironment,
  canvasMode,
  canvasRef,
  chatMessages,
  chatInput,
  chatLoading,
  chatEndRef,
  terminalOutput,
  lastCmdError,
  fixLoading,
  syncCode,
  codeFileLabel,
  codeEditorFilePath,
  codeSaving,
  activeEnv,
  selectedCloudConnection,
  unresolvedHybridEnv,
  hoveredResource,
  hoverPos,
  onBack,
  onProjectNameChange,
  onRevealProject,
  onUndo,
  onRedo,
  onRunCommand,
  onOpenSettings,
  onSettingsChange,
  onSettingsNotify,
  onCloseSettings,
  onResizeStart,
  onActivePanelChange,
  onSearchQueryChange,
  onAddResource,
  onResourceHover,
  onResourceHoverEnd,
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
  onCloudConnectionSelect,
  onCanvasModeChange,
  onRightTabChange,
  onDeleteEdge,
  onUpdateNodeName,
  onUpdateNodeProp,
  onSyncCodeChange,
  onSaveCode,
  onChatInputChange,
  onChatSubmit,
  onClearTerminal,
  onFixLastError,
}: WorkspaceShellProps) {
  return (
    <div style={S.app} className="iac-app">
      {notification && <div style={S.notification}>{notification}</div>}

      <AppHeader
        tool={tool}
        toolMeta={toolMeta}
        projectName={projectName}
        projectId={projectId}
        resourceCount={nodes.length}
        wsConnected={wsConnected}
        canUndo={canUndo}
        canRedo={canRedo}
        onBack={onBack}
        onProjectNameChange={onProjectNameChange}
        onRevealProject={onRevealProject}
        onUndo={onUndo}
        onRedo={onRedo}
        onRunCommand={onRunCommand}
        onOpenSettings={onOpenSettings}
        selectedCloudConnection={selectedCloudConnection}
      />

      {showSettings && (
        <AISettingsModal
          settings={aiSettings}
          onSettingsChange={onSettingsChange}
          onNotify={onSettingsNotify}
          onClose={onCloseSettings}
        />
      )}

      <div style={S.main}>
        <WorkspaceSidebar
          width={sidebarWidth}
          activePanel={activePanel}
          tool={tool}
          toolMeta={toolMeta}
          projectName={projectName}
          provider={provider}
          resources={catalogResources}
          suggestions={suggestions}
          searchQuery={searchQuery}
          onActivePanelChange={onActivePanelChange}
          onSearchQueryChange={onSearchQueryChange}
          onAddResource={onAddResource}
          onResourceHover={onResourceHover}
          onResourceHoverEnd={onResourceHoverEnd}
        />
        <ResizeHandle
          direction="col"
          panel="sidebar"
          activePanel={resizing?.panel}
          color={toolMeta.color}
          startSize={sidebarWidth}
          onResizeStart={onResizeStart}
        />

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
          toolMeta={toolMeta}
          onMouseMove={onMouseMove}
          onDragEnd={onDragEnd}
          onConnectionCancel={onConnectionCancel}
          onNodeDragStart={onNodeDragStart}
          onStartConnection={onStartConnection}
          onCompleteConnection={onCompleteConnection}
          onSelectNode={onSelectNode}
          onSelectEdge={onSelectEdge}
          onClearSelection={onClearSelection}
          onDeleteNode={onDeleteNode}
          onActiveEnvironmentChange={onActiveEnvironmentChange}
          onCanvasModeChange={onCanvasModeChange}
        />

        <ResizeHandle
          direction="col"
          panel="right"
          activePanel={resizing?.panel}
          color={toolMeta.color}
          startSize={rightWidth}
          onResizeStart={onResizeStart}
        />
        <InspectorPanel
          width={rightWidth}
          activeTab={rightTab}
          tool={tool}
          toolMeta={toolMeta}
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
          selectedCloudConnection={selectedCloudConnection}
          onTabChange={onRightTabChange}
          onDeleteEdge={onDeleteEdge}
          onSelectEdge={onSelectEdge}
          onUpdateNodeName={onUpdateNodeName}
          onUpdateNodeProp={onUpdateNodeProp}
          onSyncCodeChange={onSyncCodeChange}
          onSaveCode={onSaveCode}
          onCloudConnectionSelect={onCloudConnectionSelect}
        />
      </div>

      <ResizeHandle
        direction="row"
        panel="bottom"
        activePanel={resizing?.panel}
        color={toolMeta.color}
        startSize={bottomHeight}
        onResizeStart={onResizeStart}
      />
      <div style={{ ...S.bottom, height: bottomHeight }}>
        <ChatPanel
          messages={chatMessages}
          input={chatInput}
          onInputChange={onChatInputChange}
          onSubmit={onChatSubmit}
          loading={chatLoading}
          toolColor={toolMeta.color}
          scrollAnchorRef={chatEndRef}
        />
        <TerminalPanel
          lines={terminalOutput}
          onClear={onClearTerminal}
          lastError={lastCmdError}
          fixLoading={fixLoading}
          toolColor={toolMeta.color}
          onFix={lastCmdError ? onFixLastError : undefined}
        />
      </div>

      {hoveredResource && (
        <ResourceTooltip resource={hoveredResource} position={hoverPos} toolColor={toolMeta.color} />
      )}
    </div>
  );
}
