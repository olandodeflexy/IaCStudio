import { UIButton } from '../../ui';
import { S } from '../../styles';

export interface AppHeaderTool {
  color: string;
  icon: string;
  name: string;
}

export interface AppHeaderProps {
  tool: string;
  toolMeta: AppHeaderTool;
  projectName: string;
  projectId: string;
  resourceCount: number;
  wsConnected: boolean;
  canUndo: boolean;
  canRedo: boolean;
  onBack: () => void | Promise<void>;
  onProjectNameChange: (_name: string) => void;
  onRevealProject: (_projectId: string) => void;
  onUndo: () => void;
  onRedo: () => void;
  onRunCommand: (_command: string) => void;
  onOpenSettings: () => void;
}

export function AppHeader({
  tool,
  toolMeta,
  projectName,
  projectId,
  resourceCount,
  wsConnected,
  canUndo,
  canRedo,
  onBack,
  onProjectNameChange,
  onRevealProject,
  onUndo,
  onRedo,
  onRunCommand,
  onOpenSettings,
}: AppHeaderProps) {
  const initCommand = tool === 'ansible' ? 'check' : 'init';
  const planCommand = tool === 'ansible' ? 'syntax' : 'plan';
  const applyCommand = tool === 'ansible' ? 'playbook' : 'apply';

  return (
    <header style={{ ...S.header, borderBottomColor: toolMeta.color + '44' }} className="iac-header">
      <div style={S.hLeft}>
        <button style={S.backBtn} onClick={onBack} aria-label="Back to projects">←</button>
        <span style={{ ...S.badge, background: toolMeta.color + '22', color: toolMeta.color }}>
          {toolMeta.icon} {toolMeta.name}
        </span>
        <input
          style={S.projInput}
          value={projectName}
          aria-label="Project name"
          onChange={event => onProjectNameChange(event.target.value)}
        />
        <button
          style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 10, fontFamily: 'JetBrains Mono', padding: '2px 6px', color: '#789187' }}
          title="Open in file manager"
          onClick={() => onRevealProject(projectId)}
        >
          OPEN
        </button>
        <span style={{ fontSize: 10, color: wsConnected ? '#4ade80' : '#ef4444' }}>
          {wsConnected ? '● live' : '● offline'}
        </span>
      </div>
      <div style={S.hRight}>
        <span style={S.count}>{resourceCount} resource{resourceCount !== 1 ? 's' : ''}</span>
        <button
          style={{ ...S.cmd, background: 'var(--bg-elev-2)', color: canUndo ? 'var(--text-main)' : '#4b5551' }}
          onClick={onUndo}
          disabled={!canUndo}
          title="Undo (Ctrl+Z)"
        >
          ↩
        </button>
        <button
          style={{ ...S.cmd, background: 'var(--bg-elev-2)', color: canRedo ? 'var(--text-main)' : '#4b5551' }}
          onClick={onRedo}
          disabled={!canRedo}
          title="Redo (Ctrl+Shift+Z)"
        >
          ↪
        </button>
        <button
          style={{ ...S.cmd, background: toolMeta.color + '22', color: toolMeta.color }}
          onClick={() => onRunCommand(initCommand)}
        >
          {tool === 'ansible' ? '▶ Check' : '▶ Init'}
        </button>
        <button
          style={{ ...S.cmd, background: toolMeta.color + '22', color: toolMeta.color }}
          onClick={() => onRunCommand(planCommand)}
        >
          {tool === 'ansible' ? '▶ Syntax' : '▶ Plan'}
        </button>
        <UIButton
          variant="primary"
          style={{ background: toolMeta.color, borderColor: toolMeta.color, color: '#0a0a0f' }}
          onClick={() => onRunCommand(applyCommand)}
        >
          ▶ Apply
        </UIButton>
        <UIButton onClick={onOpenSettings} title="AI Settings">
          SETTINGS
        </UIButton>
      </div>
    </header>
  );
}
