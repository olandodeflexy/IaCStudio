import { useEffect, useMemo, useState } from 'react';
import type { CSSProperties, KeyboardEvent, RefObject } from 'react';

import { S } from '../../styles';

export interface ChatMessage {
  role: string;
  text: string;
  id?: string;
}

export interface ChatPanelProps {
  messages: ChatMessage[];
  input: string;
  onInputChange: (_value: string) => void;
  onSubmit: () => void;
  loading: boolean;
  // Tool colour is used for the AI bullet glyph and the send button
  // background so the panel picks up the same accent as the rest of
  // the app for whichever tool (terraform / opentofu / ansible) is
  // active.
  toolColor: string;
  // Scroll anchor the parent pins to the bottom of the list when new
  // messages arrive; we accept it as a ref so the "scroll into view"
  // behaviour stays in one place.
  scrollAnchorRef?: RefObject<HTMLDivElement>;
  // Provider name shown in the header badge. Defaults to "Ollama".
  providerLabel?: string;
}

type AgentHubTab = 'chat' | 'codex' | 'claude' | 'gemini' | 'copilot' | 'local' | 'mcp' | 'runs';
type ProviderState = 'available' | 'setup' | 'planned' | 'guarded';

const AGENT_TABS: { key: AgentHubTab; label: string }[] = [
  { key: 'chat', label: 'Chat' },
  { key: 'codex', label: 'Codex' },
  { key: 'claude', label: 'Claude Code' },
  { key: 'gemini', label: 'Gemini' },
  { key: 'copilot', label: 'Copilot' },
  { key: 'local', label: 'Local' },
  { key: 'mcp', label: 'MCP' },
  { key: 'runs', label: 'Runs' },
];

const TASK_MODES = [
  'Review project',
  'Generate IaC',
  'Explain plan',
  'Fix policy',
  'Prepare deploy',
];

const PROVIDER_GROUPS: Record<Exclude<AgentHubTab, 'chat' | 'runs'>, {
  title: string;
  summary: string;
  providers: { name: string; lane: string; state: ProviderState; note: string }[];
}> = {
  codex: {
    title: 'Codex',
    summary: 'Use local Codex login first, then API or enterprise routing when teams need central controls.',
    providers: [
      { name: 'Codex CLI', lane: 'Local agent', state: 'planned', note: 'Use the official CLI session the user already owns.' },
      { name: 'OpenAI API', lane: 'API', state: 'setup', note: 'Usage is billed through the Platform API account.' },
      { name: 'Managed Codex token', lane: 'Enterprise', state: 'guarded', note: 'For workspace policy, audit, and non-interactive runs.' },
    ],
  },
  claude: {
    title: 'Claude Code',
    summary: 'Prefer Claude Code CLI for subscription-backed local work, with API and enterprise paths as explicit choices.',
    providers: [
      { name: 'Claude Code CLI', lane: 'Local agent', state: 'planned', note: 'Runs through the official local Claude Code login.' },
      { name: 'Anthropic API', lane: 'API', state: 'setup', note: 'Separate API billing for automation and hosted use.' },
      { name: 'Claude Team or Enterprise', lane: 'Enterprise', state: 'guarded', note: 'For managed access, policy, and audit controls.' },
    ],
  },
  gemini: {
    title: 'Gemini',
    summary: 'Support Gemini CLI and API paths without forcing users into a separate hosted account flow first.',
    providers: [
      { name: 'Gemini CLI', lane: 'Local agent', state: 'planned', note: 'Use the local Gemini session when present.' },
      { name: 'Gemini API', lane: 'API', state: 'setup', note: 'Explicit API billing path for automation and hosted workflows.' },
      { name: 'Google Cloud enterprise controls', lane: 'Enterprise', state: 'guarded', note: 'For governed workspace use through organization policy.' },
    ],
  },
  copilot: {
    title: 'GitHub Copilot',
    summary: 'Expose Copilot as a first-class assistant lane for teams already signed in through GitHub.',
    providers: [
      { name: 'GitHub Copilot CLI', lane: 'Local agent', state: 'planned', note: 'Use the local GitHub auth session and Copilot entitlement.' },
      { name: 'Copilot coding agent', lane: 'Collaboration', state: 'guarded', note: 'Route issue and PR work through auditable GitHub workflows.' },
      { name: 'Copilot Business or Enterprise', lane: 'Enterprise', state: 'setup', note: 'Use organization-managed access and policy controls.' },
    ],
  },
  local: {
    title: 'Local models',
    summary: 'Keep offline and private model workflows first-class for demos, sensitive reviews, and no-token-cost usage.',
    providers: [
      { name: 'Ollama', lane: 'Local model', state: 'available', note: 'Current default local model path.' },
      { name: 'LM Studio / vLLM', lane: 'OpenAI-compatible', state: 'planned', note: 'Use local endpoints without cloud egress.' },
      { name: 'llama.cpp', lane: 'Offline', state: 'planned', note: 'Small local reviews and explain-only workflows.' },
    ],
  },
  mcp: {
    title: 'MCP tools',
    summary: 'Route AWS, Terraform, GitHub, and future tools through approvals instead of raw unrestricted agent access.',
    providers: [
      { name: 'AWS MCP', lane: 'Cloud tools', state: 'guarded', note: 'Read-only by default with explicit write approval.' },
      { name: 'Terraform MCP', lane: 'IaC tools', state: 'guarded', note: 'Plan and state context behind MCP Airlock.' },
      { name: 'GitHub MCP', lane: 'Collaboration', state: 'planned', note: 'Issue, PR, and review workflows with audit trails.' },
    ],
  },
};

const stateLabels: Record<ProviderState, string> = {
  available: 'Available',
  setup: 'Setup',
  planned: 'Next',
  guarded: 'Guarded',
};

const stateColors: Record<ProviderState, string> = {
  available: 'var(--accent-action)',
  setup: 'var(--accent-warn)',
  planned: 'var(--text-muted)',
  guarded: '#8aa7ff',
};

const stateBackgrounds: Record<ProviderState, string> = {
  available: 'rgba(84, 184, 169, 0.16)',
  setup: 'rgba(217, 177, 92, 0.16)',
  planned: 'rgba(147, 163, 154, 0.14)',
  guarded: 'rgba(138, 167, 255, 0.16)',
};

const tabId = (tab: AgentHubTab) => `agent-hub-tab-${tab}`;
const panelId = (tab: AgentHubTab) => `agent-hub-panel-${tab}`;

const hubStyles: Record<string, CSSProperties> = {
  shell: { flex: 1, display: 'flex', minWidth: 0, minHeight: 0 },
  rail: { width: 176, borderRight: '1px solid var(--border-soft)', background: 'rgba(23, 29, 27, 0.76)', display: 'flex', flexDirection: 'column', flexShrink: 0, overflowY: 'auto' },
  tabList: { display: 'flex', flexDirection: 'column', padding: 8, gap: 4 },
  tabButton: { width: '100%', border: 0, borderRadius: 6, background: 'transparent', color: 'var(--text-muted)', cursor: 'pointer', fontSize: 11, fontWeight: 700, fontFamily: 'DM Sans', textAlign: 'left', padding: '7px 8px', textTransform: 'uppercase', letterSpacing: 0.4 },
  taskList: { borderTop: '1px solid var(--border-soft)', padding: '8px 8px 10px', display: 'flex', flexDirection: 'column', gap: 5, minHeight: 0 },
  taskButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-2)', color: 'var(--text-muted)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', padding: '6px 7px', textAlign: 'left', whiteSpace: 'normal' },
  content: { flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 },
  tabPanel: { flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 },
  hiddenTabPanel: { display: 'none' },
  posture: { display: 'flex', alignItems: 'center', gap: 6, padding: '7px 12px', borderBottom: '1px solid var(--border-soft)', color: 'var(--text-muted)', fontSize: 11, fontFamily: 'JetBrains Mono', flexWrap: 'wrap' },
  badge: { padding: '2px 7px', borderRadius: 999, background: 'var(--bg-elev-3)', color: 'var(--text-muted)', fontSize: 10, fontFamily: 'JetBrains Mono', whiteSpace: 'nowrap' },
  providerPanel: { flex: 1, minHeight: 0, overflowY: 'auto', padding: '10px 12px', display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(190px, 1fr))', gap: 8 },
  providerIntro: { gridColumn: '1 / -1', color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.45, marginBottom: 2 },
  providerCard: { border: '1px solid var(--border-main)', borderRadius: 8, background: 'var(--bg-elev-2)', padding: 10, minWidth: 0 },
  providerHead: { display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 },
  providerName: { color: 'var(--text-main)', fontWeight: 700, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  providerLane: { color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', fontSize: 10, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 5 },
  providerNote: { color: '#77847d', fontSize: 11, lineHeight: 1.4 },
  runsEmpty: { flex: 1, minHeight: 0, padding: 16, color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.55 },
};

function providerStatus(state: ProviderState) {
  return (
    <span style={{ ...hubStyles.badge, color: stateColors[state], background: stateBackgrounds[state] }}>
      {stateLabels[state]}
    </span>
  );
}

function ProviderGroup({ tab, active }: { tab: Exclude<AgentHubTab, 'chat' | 'runs'>; active: boolean }) {
  const group = PROVIDER_GROUPS[tab];
  return (
    <div
      role="tabpanel"
      id={panelId(tab)}
      aria-labelledby={tabId(tab)}
      hidden={!active}
      style={active ? hubStyles.providerPanel : hubStyles.hiddenTabPanel}
    >
      <div style={hubStyles.providerIntro}>
        <strong style={{ color: 'var(--text-main)' }}>{group.title}</strong>
        <span> - {group.summary}</span>
      </div>
      {group.providers.map(provider => (
        <div key={provider.name} style={hubStyles.providerCard}>
          <div style={hubStyles.providerHead}>
            <span style={{ ...hubStyles.providerName, flex: 1 }}>{provider.name}</span>
            {providerStatus(provider.state)}
          </div>
          <div style={hubStyles.providerLane}>{provider.lane}</div>
          <div style={hubStyles.providerNote}>{provider.note}</div>
        </div>
      ))}
    </div>
  );
}

// Self-contained Agent Hub panel: AI message stream + provider/task shell.
// The parent still owns messages and provider calls, so this stays UI-only.
export function ChatPanel({
  messages,
  input,
  onInputChange,
  onSubmit,
  loading,
  toolColor,
  scrollAnchorRef,
  providerLabel = 'Ollama',
}: ChatPanelProps) {
  const [activeTab, setActiveTab] = useState<AgentHubTab>('chat');
  const [activeTask, setActiveTask] = useState(TASK_MODES[0]);

  const activeProviderLabel = useMemo(() => {
    if (activeTab === 'codex') return 'Codex CLI';
    if (activeTab === 'claude') return 'Claude Code';
    if (activeTab === 'gemini') return 'Gemini';
    if (activeTab === 'copilot') return 'GitHub Copilot';
    if (activeTab === 'local') return providerLabel;
    if (activeTab === 'mcp') return 'MCP Airlock';
    if (activeTab === 'runs') return 'Run history';
    return providerLabel;
  }, [activeTab, providerLabel]);

  const panelStyle = (tab: AgentHubTab) => (
    activeTab === tab ? hubStyles.tabPanel : hubStyles.hiddenTabPanel
  );

  const focusSelectedTab = (tab: AgentHubTab) => {
    if (typeof document === 'undefined') return;
    window.setTimeout(() => document.getElementById(tabId(tab))?.focus(), 0);
  };

  const selectTab = (tab: AgentHubTab, focus = false) => {
    setActiveTab(tab);
    if (focus) focusSelectedTab(tab);
  };

  useEffect(() => {
    if (activeTab !== 'chat') return;
    scrollAnchorRef?.current?.scrollIntoView?.({ block: 'nearest' });
  }, [activeTab, scrollAnchorRef]);

  const handleTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>, currentIndex: number) => {
    const lastIndex = AGENT_TABS.length - 1;
    const moveToIndex = (index: number) => {
      event.preventDefault();
      selectTab(AGENT_TABS[index].key, true);
    };

    if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
      moveToIndex(currentIndex === lastIndex ? 0 : currentIndex + 1);
      return;
    }
    if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
      moveToIndex(currentIndex === 0 ? lastIndex : currentIndex - 1);
      return;
    }
    if (event.key === 'Home') {
      moveToIndex(0);
      return;
    }
    if (event.key === 'End') {
      moveToIndex(lastIndex);
    }
  };

  return (
    <div style={S.chat}>
      <div style={S.chatHead}>
        <span style={{ fontSize: 14, color: 'var(--accent-action)' }}>✦</span>
        <span>Agent Hub</span>
        <span style={S.chatBadge}>{activeProviderLabel}</span>
      </div>

      <div style={hubStyles.shell}>
        <div style={hubStyles.rail}>
          <div
            style={hubStyles.tabList}
            role="tablist"
            aria-label="Agent Hub providers"
            aria-orientation="vertical"
          >
            {AGENT_TABS.map((tab, index) => (
              <button
                key={tab.key}
                id={tabId(tab.key)}
                type="button"
                role="tab"
                aria-selected={activeTab === tab.key}
                aria-controls={panelId(tab.key)}
                tabIndex={activeTab === tab.key ? 0 : -1}
                style={{
                  ...hubStyles.tabButton,
                  ...(activeTab === tab.key
                    ? { background: 'var(--accent-action-soft)', color: 'var(--accent-action)' }
                    : {}),
                }}
                onClick={() => selectTab(tab.key)}
                onKeyDown={(event) => handleTabKeyDown(event, index)}
              >
                {tab.label}
              </button>
            ))}
          </div>
          <div style={hubStyles.taskList} role="group" aria-label="Agent task modes">
            {TASK_MODES.map(task => (
              <button
                key={task}
                type="button"
                aria-pressed={activeTask === task}
                style={{
                  ...hubStyles.taskButton,
                  ...(activeTask === task
                    ? { borderColor: toolColor, color: 'var(--text-main)', background: `${toolColor}1f` }
                    : {}),
                }}
                onClick={() => setActiveTask(task)}
              >
                {task}
              </button>
            ))}
          </div>
        </div>

        <div style={hubStyles.content}>
          <div style={hubStyles.posture}>
            <span>{activeTask}</span>
            <span style={hubStyles.badge}>Read-only default</span>
            <span style={hubStyles.badge}>Diff before writes</span>
            <span style={hubStyles.badge}>Approve deploy</span>
            <span style={hubStyles.badge}>No secret prompts</span>
          </div>

          <div
            role="tabpanel"
            id={panelId('chat')}
            aria-labelledby={tabId('chat')}
            hidden={activeTab !== 'chat'}
            style={panelStyle('chat')}
          >
            <div style={S.chatMsgs}>
              {messages.length === 0 && (
                <div style={{ padding: '8px 0', color: '#888', fontSize: 13 }}>
                  <p style={{ margin: 0 }}>Ask me to create infrastructure:</p>
                  <p style={{ margin: '4px 0 0', color: '#555', fontSize: 12 }}>
                    "Add a VPC" · "Create an RDS database" · "I need an S3 bucket"
                  </p>
                </div>
              )}
              {messages.map((m, i) => (
                <div
                  key={m.id ?? i}
                  style={{
                    padding: '6px 0',
                    fontSize: 13,
                    display: 'flex',
                    gap: 8,
                    color: m.role === 'ai' ? '#999' : '#ccc',
                  }}
                >
                  {m.role === 'ai' && (
                    <span style={{ color: toolColor, fontWeight: 700, flexShrink: 0 }}>✦</span>
                  )}
                  <span>{m.text}</span>
                </div>
              ))}
              {loading && (
                <div style={{ padding: '6px 0', fontSize: 13, color: '#666' }}>✦ Thinking...</div>
              )}
              <div ref={scrollAnchorRef} />
            </div>
          </div>

          {(['codex', 'claude', 'gemini', 'copilot', 'local', 'mcp'] as const).map(tab => (
            <ProviderGroup key={tab} tab={tab} active={activeTab === tab} />
          ))}

          <div
            role="tabpanel"
            id={panelId('runs')}
            aria-labelledby={tabId('runs')}
            hidden={activeTab !== 'runs'}
            style={panelStyle('runs')}
          >
            <div style={hubStyles.runsEmpty}>
              <strong style={{ color: 'var(--text-main)' }}>No agent runs yet.</strong>
              <div style={{ marginTop: 6 }}>
                Future runs will show provider, task mode, approvals, proposed patches, and deployment actions in one audit trail.
              </div>
              <div style={{ marginTop: 10, color: '#77847d' }}>
                This shell is UI-only; execution and approval gates land in the secure run lifecycle slice.
              </div>
            </div>
          </div>
        </div>
      </div>

      <div style={S.chatInputRow}>
        <input
          style={S.chatInput}
          value={input}
          onChange={(e) => onInputChange(e.target.value)}
          placeholder="Describe infrastructure you need..."
          onKeyDown={(e) => e.key === 'Enter' && onSubmit()}
          disabled={loading}
        />
        <button
          style={{ ...S.chatSend, background: toolColor }}
          onClick={onSubmit}
          disabled={loading}
          aria-label="Send message"
        >
          ↑
        </button>
      </div>
    </div>
  );
}
