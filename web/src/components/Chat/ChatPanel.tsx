import { useEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, KeyboardEvent, ReactNode, RefObject } from 'react';

import { api, type AgentRun, type AgentRunApprovalDecision, type AgentRunMode, type AgentRunSummary, type LocalAgentProviderStatus } from '../../api';
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
  // Current project used to fetch agent run audit summaries.
  projectName?: string;
}

type AgentHubTab = 'chat' | 'codex' | 'claude' | 'gemini' | 'copilot' | 'local' | 'mcp' | 'runs';
type ProviderState = 'available' | 'setup' | 'planned' | 'guarded';
type ProviderTab = Exclude<AgentHubTab, 'chat' | 'runs'>;
type ProviderLane =
  | 'API'
  | 'Cloud tools'
  | 'Collaboration'
  | 'Enterprise'
  | 'IaC tools'
  | 'Local agent'
  | 'Local model'
  | 'Offline'
  | 'OpenAI-compatible';
type ProviderActionLabel = 'Configure API' | 'Use enterprise policy' | 'Use local CLI';
type ProviderDefinition = { name: string; lane: ProviderLane; state: ProviderState; note: string; localProviderId?: string };

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
] as const;

const AGENT_HUB_STORAGE_KEYS = {
  activeTab: 'iac-studio.agentHub.activeTab',
  activeTask: 'iac-studio.agentHub.activeTask',
};

const PROVIDER_GROUPS: Record<ProviderTab, {
  title: string;
  summary: string;
  providers: ProviderDefinition[];
}> = {
  codex: {
    title: 'Codex',
    summary: 'Use local Codex login first, then API or enterprise routing when teams need central controls.',
    providers: [
      { name: 'Codex CLI', lane: 'Local agent', state: 'planned', note: 'Use the official CLI session the user already owns.', localProviderId: 'codex' },
      { name: 'OpenAI API', lane: 'API', state: 'setup', note: 'Usage is billed through the Platform API account.' },
      { name: 'Managed Codex token', lane: 'Enterprise', state: 'guarded', note: 'For workspace policy, audit, and non-interactive runs.' },
    ],
  },
  claude: {
    title: 'Claude Code',
    summary: 'Prefer Claude Code CLI for subscription-backed local work, with API and enterprise paths as explicit choices.',
    providers: [
      { name: 'Claude Code CLI', lane: 'Local agent', state: 'planned', note: 'Runs through the official local Claude Code login.', localProviderId: 'claude' },
      { name: 'Anthropic API', lane: 'API', state: 'setup', note: 'Separate API billing for automation and hosted use.' },
      { name: 'Claude Team or Enterprise', lane: 'Enterprise', state: 'guarded', note: 'For managed access, policy, and audit controls.' },
    ],
  },
  gemini: {
    title: 'Gemini',
    summary: 'Support Gemini CLI and API paths without forcing users into a separate hosted account flow first.',
    providers: [
      { name: 'Gemini CLI', lane: 'Local agent', state: 'planned', note: 'Use the local Gemini session when present.', localProviderId: 'gemini' },
      { name: 'Gemini API', lane: 'API', state: 'setup', note: 'Explicit API billing path for automation and hosted workflows.' },
      { name: 'Google Cloud enterprise controls', lane: 'Enterprise', state: 'guarded', note: 'For governed workspace use through organization policy.' },
    ],
  },
  copilot: {
    title: 'GitHub Copilot',
    summary: 'Expose Copilot as a first-class assistant lane for teams already signed in through GitHub.',
    providers: [
      { name: 'GitHub Copilot CLI', lane: 'Local agent', state: 'planned', note: 'Use the local GitHub auth session and Copilot entitlement.', localProviderId: 'copilot' },
      { name: 'Copilot coding agent', lane: 'Collaboration', state: 'guarded', note: 'Route issue and PR work through auditable GitHub workflows.' },
      { name: 'Copilot Business or Enterprise', lane: 'Enterprise', state: 'setup', note: 'Use organization-managed access and policy controls.' },
    ],
  },
  local: {
    title: 'Local models',
    summary: 'Keep offline and private model workflows first-class for demos, sensitive reviews, and no-token-cost usage.',
    providers: [
      { name: 'Ollama', lane: 'Local model', state: 'available', note: 'Current default local model path.', localProviderId: 'ollama' },
      { name: 'LM Studio / vLLM', lane: 'OpenAI-compatible', state: 'planned', note: 'Use local endpoints without cloud egress.', localProviderId: 'openai-compatible-local' },
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

const providerActionByLane: Record<ProviderLane, ProviderActionLabel> = {
  API: 'Configure API',
  'Cloud tools': 'Use enterprise policy',
  Collaboration: 'Use enterprise policy',
  Enterprise: 'Use enterprise policy',
  'IaC tools': 'Use enterprise policy',
  'Local agent': 'Use local CLI',
  'Local model': 'Use local CLI',
  Offline: 'Use local CLI',
  'OpenAI-compatible': 'Configure API',
};

const tabId = (tab: AgentHubTab) => `agent-hub-tab-${tab}`;
const panelId = (tab: AgentHubTab) => `agent-hub-panel-${tab}`;
const isAgentHubTab = (value: string | null): value is AgentHubTab => (
  AGENT_TABS.some(tab => tab.key === value)
);
const isTaskMode = (value: string | null): value is typeof TASK_MODES[number] => (
  TASK_MODES.some(task => task === value)
);

function readStoredAgentHubTab(): AgentHubTab {
  if (typeof window === 'undefined') return 'chat';
  try {
    const storedTab = window.localStorage.getItem(AGENT_HUB_STORAGE_KEYS.activeTab);
    return isAgentHubTab(storedTab) ? storedTab : 'chat';
  } catch {
    return 'chat';
  }
}

function readStoredTaskMode(): typeof TASK_MODES[number] {
  if (typeof window === 'undefined') return TASK_MODES[0];
  try {
    const storedTask = window.localStorage.getItem(AGENT_HUB_STORAGE_KEYS.activeTask);
    return isTaskMode(storedTask) ? storedTask : TASK_MODES[0];
  } catch {
    return TASK_MODES[0];
  }
}

function writeStoredAgentHubValue(key: string, value: string) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Browser privacy settings can disable localStorage; the UI should remain usable.
  }
}

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
  providerCard: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 8, background: 'var(--bg-elev-2)', padding: 10, minWidth: 0, color: 'inherit', cursor: 'pointer', textAlign: 'left', font: 'inherit' },
  providerHead: { display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 },
  providerName: { color: 'var(--text-main)', fontWeight: 700, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  providerLane: { color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', fontSize: 10, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 5 },
  providerNote: { color: '#77847d', fontSize: 11, lineHeight: 1.4 },
  providerMeta: { display: 'flex', gap: 5, flexWrap: 'wrap', marginTop: 8 },
  providerCapabilityList: { display: 'flex', gap: 5, flexWrap: 'wrap', marginTop: 6 },
  providerDetails: { gridColumn: '1 / -1', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 8, background: 'rgba(23, 29, 27, 0.84)', padding: 10, minWidth: 0 },
  providerDetailsGrid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 8, marginTop: 8 },
  providerDetailLabel: { color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', fontSize: 10, textTransform: 'uppercase', letterSpacing: 0.5 },
  providerDetailValue: { color: 'var(--text-main)', fontSize: 12, marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  providerActions: { display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginTop: 10 },
  providerActionButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-3)', color: 'var(--text-muted)', cursor: 'not-allowed', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '6px 9px', opacity: 0.72 },
  providerActionHint: { color: '#77847d', fontSize: 11 },
  runsPanel: { flex: 1, minHeight: 0, overflowY: 'auto', padding: 12, display: 'flex', flexDirection: 'column', gap: 8 },
  runsEmpty: { flex: 1, minHeight: 0, padding: 16, color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.55 },
  runQueueCard: { borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(84, 184, 169, 0.32)', borderRadius: 8, background: 'rgba(84, 184, 169, 0.08)', padding: 10, minWidth: 0 },
  runQueueHead: { display: 'flex', alignItems: 'center', gap: 8, minWidth: 0, flexWrap: 'wrap' },
  runQueueText: { color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.45, marginTop: 7, overflowWrap: 'anywhere' },
  runQueueButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(84, 184, 169, 0.48)', borderRadius: 6, background: 'rgba(84, 184, 169, 0.18)', color: 'var(--accent-action)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  runCard: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 8, background: 'var(--bg-elev-2)', padding: 10, minWidth: 0 },
  runCardHead: { display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 },
  runTitle: { flex: 1, minWidth: 0, color: 'var(--text-main)', fontWeight: 700, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  runPreview: { color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.45, marginTop: 7, overflowWrap: 'anywhere' },
  runMeta: { display: 'flex', gap: 5, flexWrap: 'wrap', marginTop: 8 },
  runActions: { display: 'flex', justifyContent: 'flex-end', gap: 6, flexWrap: 'wrap', marginTop: 8 },
  runDetailsButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(84, 184, 169, 0.4)', borderRadius: 6, background: 'rgba(84, 184, 169, 0.12)', color: 'var(--accent-action)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  runCancelButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-3)', color: 'var(--text-muted)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  runDetailPanel: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 8, background: 'rgba(23, 29, 27, 0.9)', padding: 10, minWidth: 0 },
  runDetailHead: { display: 'flex', alignItems: 'center', gap: 8, minWidth: 0, marginBottom: 8 },
  runDetailSection: { borderTop: '1px solid var(--border-soft)', paddingTop: 8, marginTop: 8 },
  runDetailSectionTitle: { color: 'var(--text-main)', fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.4, marginBottom: 6 },
  runDetailList: { display: 'flex', flexDirection: 'column', gap: 6 },
  runDetailItem: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-2)', padding: 8, minWidth: 0 },
  runDetailMessage: { color: 'var(--text-muted)', fontSize: 11, lineHeight: 1.4, overflowWrap: 'anywhere' },
  runDetailDiff: { margin: '6px 0 0', maxHeight: 160, overflow: 'auto', borderRadius: 6, background: 'rgba(5, 8, 8, 0.72)', color: 'var(--text-muted)', fontSize: 10, lineHeight: 1.45, padding: 8, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  runDetailCloseButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-3)', color: 'var(--text-muted)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  pendingGateList: { borderTop: '1px solid var(--border-soft)', marginTop: 9, paddingTop: 8, display: 'flex', flexDirection: 'column', gap: 7 },
  pendingGateRow: { display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: 8, alignItems: 'center' },
  pendingGateSummary: { color: 'var(--text-muted)', fontSize: 11, lineHeight: 1.35, overflowWrap: 'anywhere' },
  pendingGateActions: { display: 'flex', gap: 5, flexWrap: 'wrap', justifyContent: 'flex-end' },
  approveButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(84, 184, 169, 0.45)', borderRadius: 6, background: 'rgba(84, 184, 169, 0.16)', color: 'var(--accent-action)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  rejectButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(232, 111, 111, 0.42)', borderRadius: 6, background: 'rgba(232, 111, 111, 0.14)', color: 'var(--accent-danger)', cursor: 'pointer', fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
};

function providerStatus(state: ProviderState) {
  return (
    <span style={{ ...hubStyles.badge, color: stateColors[state], background: stateBackgrounds[state] }}>
      {stateLabels[state]}
    </span>
  );
}

function credentialLabel(mode: string) {
  if (mode === 'none') return 'No credentials';
  if (mode === 'external_login') return 'External login';
  return mode;
}

function providerDisplay(provider: Pick<ProviderDefinition, 'state' | 'note'>, status?: LocalAgentProviderStatus) {
  if (!status) {
    return { state: provider.state, note: provider.note };
  }
  const state: ProviderState = status.installed ? 'available' : 'setup';
  return {
    state,
    note: status.installed ? status.auth_hint : status.install_hint || status.auth_hint,
  };
}

function localStatusDetails(localProviderId?: string, status?: LocalAgentProviderStatus) {
  if (!status) {
    if (!localProviderId) {
      return {
        credential: 'Provider managed',
        entrypoint: 'Not applicable',
        version: 'Not applicable',
        capabilities: [] as string[],
      };
    }
    return {
      credential: 'Not connected',
      entrypoint: 'Pending',
      version: 'Unknown',
      capabilities: [] as string[],
    };
  }
  return {
    credential: credentialLabel(status.credential_mode),
    entrypoint: status.command || status.entrypoint || 'Detected',
    version: status.version || 'Unknown',
    capabilities: status.capabilities,
  };
}

function providerActionLabel(provider: ProviderDefinition) {
  return providerActionByLane[provider.lane];
}

function ProviderDetails({
  provider,
  displayState,
  displayNote,
  localStatus,
}: {
  provider: ProviderDefinition;
  displayState: ProviderState;
  displayNote: string;
  localStatus?: LocalAgentProviderStatus;
}) {
  const details = localStatusDetails(provider.localProviderId, localStatus);
  return (
    <div
      role="region"
      style={hubStyles.providerDetails}
      aria-label={`${provider.name} details`}
      aria-live="polite"
    >
      <div style={hubStyles.providerHead}>
        <span style={{ ...hubStyles.providerName, flex: 1 }}>{provider.name}</span>
        {providerStatus(displayState)}
      </div>
      <div style={hubStyles.providerNote}>{displayNote}</div>
      <div style={hubStyles.providerDetailsGrid}>
        <div>
          <div style={hubStyles.providerDetailLabel}>Lane</div>
          <div style={hubStyles.providerDetailValue}>{provider.lane}</div>
        </div>
        <div>
          <div style={hubStyles.providerDetailLabel}>Credential</div>
          <div style={hubStyles.providerDetailValue}>{details.credential}</div>
        </div>
        <div>
          <div style={hubStyles.providerDetailLabel}>Entrypoint</div>
          <div style={hubStyles.providerDetailValue}>{details.entrypoint}</div>
        </div>
        <div>
          <div style={hubStyles.providerDetailLabel}>Version</div>
          <div style={hubStyles.providerDetailValue}>{details.version}</div>
        </div>
      </div>
      {details.capabilities.length > 0 && (
        <div style={hubStyles.providerCapabilityList} aria-label={`${provider.name} selected capabilities`}>
          {details.capabilities.map(capability => (
            <span key={capability} style={hubStyles.badge}>{capability.replaceAll('_', ' ')}</span>
          ))}
        </div>
      )}
      <div role="group" style={hubStyles.providerActions} aria-label={`${provider.name} actions`}>
        <button
          type="button"
          disabled
          style={hubStyles.providerActionButton}
        >
          {providerActionLabel(provider)}
        </button>
        <span style={hubStyles.providerActionHint}>Preview only</span>
      </div>
    </div>
  );
}

function ProviderGroup({
  tab,
  active,
  localProviders,
  selectedProviderName,
  onSelectProvider,
}: {
  tab: ProviderTab;
  active: boolean;
  localProviders: Record<string, LocalAgentProviderStatus>;
  selectedProviderName?: string;
  onSelectProvider: (tab: ProviderTab, providerName: string) => void;
}) {
  const group = PROVIDER_GROUPS[tab];
  const selectedProvider = group.providers.find(provider => provider.name === selectedProviderName) ?? group.providers[0];
  const selectedLocalStatus = selectedProvider.localProviderId ? localProviders[selectedProvider.localProviderId] : undefined;
  const selectedDisplay = providerDisplay(selectedProvider, selectedLocalStatus);
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
      {group.providers.map(provider => {
        const localStatus = provider.localProviderId ? localProviders[provider.localProviderId] : undefined;
        const display = providerDisplay(provider, localStatus);
        const selected = provider.name === selectedProvider.name;
        return (
          <button
            key={provider.name}
            type="button"
            aria-pressed={selected}
            style={{
              ...hubStyles.providerCard,
              ...(selected ? { borderColor: stateColors[display.state], boxShadow: `inset 0 0 0 1px ${stateColors[display.state]}` } : {}),
            }}
            onClick={() => onSelectProvider(tab, provider.name)}
          >
            <div style={hubStyles.providerHead}>
              <span style={{ ...hubStyles.providerName, flex: 1 }}>{provider.name}</span>
              {providerStatus(display.state)}
            </div>
            <div style={hubStyles.providerLane}>{provider.lane}</div>
            <div style={hubStyles.providerNote}>{display.note}</div>
            {localStatus && (
              <>
                <div style={hubStyles.providerMeta}>
                  <span style={hubStyles.badge}>
                    {localStatus.installed ? `Detected: ${localStatus.command || localStatus.entrypoint}` : 'Not installed'}
                  </span>
                  <span style={hubStyles.badge}>{credentialLabel(localStatus.credential_mode)}</span>
                  <span style={hubStyles.badge}>Version {localStatus.version}</span>
                </div>
                {localStatus.capabilities.length > 0 && (
                  <div style={hubStyles.providerCapabilityList} aria-label={`${provider.name} capabilities`}>
                    {localStatus.capabilities.slice(0, 4).map(capability => (
                      <span key={capability} style={hubStyles.badge}>{capability.replaceAll('_', ' ')}</span>
                    ))}
                  </div>
                )}
              </>
            )}
          </button>
        );
      })}
      <ProviderDetails
        provider={selectedProvider}
        displayState={selectedDisplay.state}
        displayNote={selectedDisplay.note}
        localStatus={selectedLocalStatus}
      />
    </div>
  );
}

function runStatusColor(status: AgentRunSummary['status']) {
  if (status === 'completed') return 'var(--accent-action)';
  if (status === 'failed' || status === 'canceled') return 'var(--accent-danger)';
  if (status === 'waiting_approval') return '#8aa7ff';
  return 'var(--accent-warn)';
}

function runModeLabel(mode: AgentRunSummary['mode']) {
  return mode.replaceAll('_', ' ');
}

function modeForTask(task: typeof TASK_MODES[number]): AgentRunMode {
  switch (task) {
    case 'Prepare deploy':
      return 'propose_only';
    case 'Generate IaC':
    case 'Fix policy':
      return 'propose_only';
    case 'Review project':
    case 'Explain plan':
      return 'read_only';
  }
  const exhaustiveTask: never = task;
  void exhaustiveTask;
  return 'read_only';
}

function isTerminalAgentRun(status: AgentRunSummary['status']) {
  return status === 'completed' || status === 'failed' || status === 'canceled';
}

function isConflictError(err: unknown) {
  return (
    typeof err === 'object'
    && err !== null
    && 'status' in err
    && (err as { status?: unknown }).status === 409
  );
}

function agentRunRefreshErrorMessage(err: unknown) {
  if (err instanceof Error && err.message) return `agent run refresh failed: ${err.message}`;
  return 'agent run refresh failed';
}

function agentRunDetailErrorMessage(err: unknown) {
  if (err instanceof Error && err.message) return `agent run detail failed: ${err.message}`;
  return 'agent run detail failed';
}

function RunDetailSection({
  title,
  empty,
  children,
}: {
  title: string;
  empty: string;
  children?: ReactNode;
}) {
  const hasContent = children !== null && children !== undefined && children !== false;
  return (
    <div style={hubStyles.runDetailSection}>
      <div style={hubStyles.runDetailSectionTitle}>{title}</div>
      {hasContent ? children : <div style={hubStyles.runDetailMessage}>{empty}</div>}
    </div>
  );
}

function RunDetailPanel({
  run,
  selectedRunId,
  loadingRunId,
  error,
  onClose,
}: {
  run: AgentRun | null;
  selectedRunId: string | null;
  loadingRunId: string | null;
  error: string | null;
  onClose: () => void;
}) {
  if (loadingRunId) {
    return (
      <div
        id={`agent-run-details-${loadingRunId}`}
        role="status"
        style={hubStyles.runDetailPanel}
        aria-label={`${loadingRunId} details loading`}
      >
        Loading {loadingRunId} details...
      </div>
    );
  }
  if (error) {
    return (
      <div
        id={selectedRunId ? `agent-run-details-${selectedRunId}` : undefined}
        role="alert"
        style={hubStyles.runDetailPanel}
      >
        <div style={hubStyles.runDetailHead}>
          <strong style={{ color: 'var(--text-main)', flex: 1 }}>Could not load run details.</strong>
          <button
            type="button"
            style={hubStyles.runDetailCloseButton}
            aria-label="Close run detail error"
            onClick={onClose}
          >
            Close
          </button>
        </div>
        <div style={{ ...hubStyles.runDetailMessage, marginTop: 6 }}>{error}</div>
      </div>
    );
  }
  if (!run) return null;

  return (
    <section
      id={`agent-run-details-${run.id}`}
      role="region"
      aria-label={`${run.id} details`}
      style={hubStyles.runDetailPanel}
    >
      <div style={hubStyles.runDetailHead}>
        <span style={hubStyles.runTitle}>{run.id}</span>
        <span style={{ ...hubStyles.badge, color: runStatusColor(run.status) }}>{run.status.replaceAll('_', ' ')}</span>
        <button
          type="button"
          style={hubStyles.runDetailCloseButton}
          aria-label={`Close details for ${run.id}`}
          onClick={onClose}
        >
          Close
        </button>
      </div>
      <div style={hubStyles.runPreview}>{run.prompt_preview || 'Prompt preview unavailable'}</div>
      <div style={hubStyles.runMeta}>
        <span style={hubStyles.badge}>{runModeLabel(run.mode)}</span>
        {run.provider_id && <span style={hubStyles.badge}>{run.provider_id}</span>}
        <span style={hubStyles.badge}>{run.prompt_hash}</span>
      </div>

      <RunDetailSection title="Logs" empty="No run logs yet.">
        {run.logs.length > 0 && (
          <div style={hubStyles.runDetailList}>
            {run.logs.map(log => (
              <div key={log.id} style={hubStyles.runDetailItem}>
                <div style={hubStyles.runMeta}>
                  <span style={hubStyles.badge}>{log.level}</span>
                  <span style={hubStyles.badge}>{log.at}</span>
                </div>
                <div style={{ ...hubStyles.runDetailMessage, marginTop: 6 }}>{log.message}</div>
              </div>
            ))}
          </div>
        )}
      </RunDetailSection>

      <RunDetailSection title="Proposed patches" empty="No proposed patches.">
        {run.patches.length > 0 && (
          <div style={hubStyles.runDetailList}>
            {run.patches.map(patch => (
              <div key={patch.id} style={hubStyles.runDetailItem}>
                <div style={hubStyles.runMeta}>
                  <span style={hubStyles.badge}>{patch.path}</span>
                  <span style={hubStyles.badge}>{patch.created_at}</span>
                </div>
                <div style={{ ...hubStyles.runDetailMessage, marginTop: 6 }}>{patch.summary}</div>
                <pre style={hubStyles.runDetailDiff}>{patch.diff}</pre>
              </div>
            ))}
          </div>
        )}
      </RunDetailSection>

      <RunDetailSection title="Approvals" empty="No approval history.">
        {run.approvals.length > 0 && (
          <div style={hubStyles.runDetailList}>
            {run.approvals.map(approval => (
              <div key={approval.id} style={hubStyles.runDetailItem}>
                <div style={hubStyles.runMeta}>
                  <span style={hubStyles.badge}>{approval.kind.replaceAll('_', ' ')}</span>
                  <span style={{ ...hubStyles.badge, color: approval.status === 'rejected' ? 'var(--accent-danger)' : approval.status === 'approved' ? 'var(--accent-action)' : '#8aa7ff' }}>
                    {approval.status}
                  </span>
                  <span style={hubStyles.badge}>{approval.id}</span>
                </div>
                <div style={{ ...hubStyles.runDetailMessage, marginTop: 6 }}>{approval.summary}</div>
              </div>
            ))}
          </div>
        )}
      </RunDetailSection>
    </section>
  );
}

function RunQueueCard({
  task,
  prompt,
  creating,
  disabled,
  onCreate,
}: {
  task: typeof TASK_MODES[number];
  prompt: string;
  creating: boolean;
  disabled: boolean;
  onCreate: () => void;
}) {
  const trimmedPrompt = prompt.trim();
  const mode = modeForTask(task);
  const blocked = disabled || creating || trimmedPrompt.length === 0;
  return (
    <div style={hubStyles.runQueueCard}>
      <div style={hubStyles.runQueueHead}>
        <strong style={{ color: 'var(--text-main)', fontSize: 12, flex: 1 }}>Queue audited run</strong>
        <span style={hubStyles.badge}>{task}</span>
        <span style={hubStyles.badge}>{runModeLabel(mode)}</span>
        <button
          type="button"
          style={{
            ...hubStyles.runQueueButton,
            ...(blocked ? { cursor: creating ? 'wait' : 'not-allowed', opacity: 0.7 } : {}),
          }}
          disabled={blocked}
          aria-busy={creating}
          aria-label="Queue current prompt as agent run"
          onClick={onCreate}
        >
          {creating ? 'Queueing...' : 'Queue run'}
        </button>
      </div>
      <div style={hubStyles.runQueueText}>
        {trimmedPrompt || 'Type a prompt in the message box, then queue it here as an auditable run.'}
      </div>
    </div>
  );
}

function RunSummaryCard({
  run,
  canceling,
  cancelDisabled,
  detailSelected,
  detailLoading,
  detailsDisabled,
  decidingGateKey,
  decisionDisabled,
  onCancel,
  onShowDetails,
  onDecideApproval,
}: {
  run: AgentRunSummary;
  canceling: boolean;
  cancelDisabled: boolean;
  detailSelected: boolean;
  detailLoading: boolean;
  detailsDisabled: boolean;
  decidingGateKey: string | null;
  decisionDisabled: boolean;
  onCancel: (id: string) => void;
  onShowDetails: (id: string) => void;
  onDecideApproval: (runId: string, approvalId: string, decision: AgentRunApprovalDecision) => void;
}) {
  const canCancel = !isTerminalAgentRun(run.status);
  const disabled = canceling || cancelDisabled;
  const detailDisabled = detailLoading || detailsDisabled;
  const detailOpen = detailSelected || detailLoading;
  const pendingGates = run.pending_gates ?? [];
  return (
    <div style={hubStyles.runCard}>
      <div style={hubStyles.runCardHead}>
        <span style={hubStyles.runTitle}>{run.id}</span>
        <span style={{ ...hubStyles.badge, color: runStatusColor(run.status) }}>{run.status.replaceAll('_', ' ')}</span>
      </div>
      <div style={hubStyles.runPreview}>
        {run.prompt_preview || 'Prompt preview unavailable'}
      </div>
      <div style={hubStyles.runMeta}>
        <span style={hubStyles.badge}>{runModeLabel(run.mode)}</span>
        {run.provider_id && <span style={hubStyles.badge}>{run.provider_id}</span>}
        <span style={hubStyles.badge}>{run.log_count} logs</span>
        <span style={hubStyles.badge}>{run.patch_count} patches</span>
        <span style={hubStyles.badge}>{run.approval_count} approvals</span>
        {run.pending_approval_count > 0 && (
          <span style={{ ...hubStyles.badge, color: '#8aa7ff' }}>{run.pending_approval_count} pending</span>
        )}
      </div>
      {pendingGates.length > 0 && (
        <div role="group" style={hubStyles.pendingGateList} aria-label={`${run.id} pending approval gates`}>
          {pendingGates.map(gate => {
            const gateKey = `${run.id}:${gate.id}`;
            const deciding = decidingGateKey === gateKey;
            const gateDisabled = decisionDisabled || deciding;
            return (
              <div key={gate.id} style={hubStyles.pendingGateRow}>
                <div>
                  <div style={hubStyles.pendingGateSummary}>{gate.summary}</div>
                  <div style={hubStyles.runMeta}>
                    <span style={hubStyles.badge}>{gate.kind.replaceAll('_', ' ')}</span>
                    <span style={hubStyles.badge}>{gate.id}</span>
                  </div>
                </div>
                <div style={hubStyles.pendingGateActions}>
                  <button
                    type="button"
                    style={{
                      ...hubStyles.approveButton,
                      ...(gateDisabled ? { cursor: deciding ? 'wait' : 'not-allowed', opacity: 0.7 } : {}),
                    }}
                    disabled={gateDisabled}
                    aria-busy={deciding}
                    aria-label={`Approve ${gate.id} for ${run.id}`}
                    onClick={() => onDecideApproval(run.id, gate.id, 'approved')}
                  >
                    Approve
                  </button>
                  <button
                    type="button"
                    style={{
                      ...hubStyles.rejectButton,
                      ...(gateDisabled ? { cursor: deciding ? 'wait' : 'not-allowed', opacity: 0.7 } : {}),
                    }}
                    disabled={gateDisabled}
                    aria-busy={deciding}
                    aria-label={`Reject ${gate.id} for ${run.id}`}
                    onClick={() => onDecideApproval(run.id, gate.id, 'rejected')}
                  >
                    Reject
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
      <div style={hubStyles.runActions}>
        <button
          type="button"
          style={{
            ...hubStyles.runDetailsButton,
            ...(detailDisabled ? { cursor: detailLoading ? 'wait' : 'not-allowed', opacity: 0.7 } : {}),
          }}
          disabled={detailDisabled}
          aria-busy={detailLoading}
          aria-expanded={detailOpen}
          aria-controls={detailOpen ? `agent-run-details-${run.id}` : undefined}
          aria-label={`View details for ${run.id}`}
          onClick={() => onShowDetails(run.id)}
        >
          {detailLoading ? 'Loading details...' : 'Details'}
        </button>
        {canCancel && (
          <button
            type="button"
            style={{
              ...hubStyles.runCancelButton,
              ...(disabled ? { cursor: canceling ? 'wait' : 'not-allowed', opacity: 0.7 } : {}),
            }}
            disabled={disabled}
            aria-label={`Cancel ${run.id}`}
            onClick={() => onCancel(run.id)}
          >
            {canceling ? 'Canceling...' : 'Cancel run'}
          </button>
        )}
      </div>
    </div>
  );
}

function RunsPanel({
  projectName,
  runs,
  loading,
  error,
  task,
  prompt,
  creatingRun,
  cancelingRunId,
  selectedRun,
  selectedRunId,
  detailLoadingRunId,
  detailError,
  decidingGateKey,
  onCreateRun,
  onCancelRun,
  onShowDetails,
  onCloseDetails,
  onDecideApproval,
}: {
  projectName?: string;
  runs: AgentRunSummary[];
  loading: boolean;
  error: string | null;
  task: typeof TASK_MODES[number];
  prompt: string;
  creatingRun: boolean;
  cancelingRunId: string | null;
  selectedRun: AgentRun | null;
  selectedRunId: string | null;
  detailLoadingRunId: string | null;
  detailError: string | null;
  decidingGateKey: string | null;
  onCreateRun: () => void;
  onCancelRun: (id: string) => void;
  onShowDetails: (id: string) => void;
  onCloseDetails: () => void;
  onDecideApproval: (runId: string, approvalId: string, decision: AgentRunApprovalDecision) => void;
}) {
  if (!projectName) {
    return (
      <div style={hubStyles.runsEmpty}>
        <strong style={{ color: 'var(--text-main)' }}>Open a project to see agent runs.</strong>
        <div style={{ marginTop: 6 }}>Run history is scoped to the active project.</div>
      </div>
    );
  }
  return (
    <div style={hubStyles.runsPanel} aria-label={`${projectName} agent runs`}>
      <RunQueueCard
        task={task}
        prompt={prompt}
        creating={creatingRun}
        disabled={cancelingRunId !== null || decidingGateKey !== null || detailLoadingRunId !== null}
        onCreate={onCreateRun}
      />
      {loading ? (
        <div style={hubStyles.runsEmpty}>Loading agent runs...</div>
      ) : error ? (
        <div style={hubStyles.runsEmpty}>
          <strong style={{ color: 'var(--text-main)' }}>Could not load agent runs.</strong>
          <div style={{ marginTop: 6 }}>{error}</div>
        </div>
      ) : runs.length === 0 ? (
        <div style={hubStyles.runsEmpty}>
          <strong style={{ color: 'var(--text-main)' }}>No agent runs yet.</strong>
          <div style={{ marginTop: 6 }}>
            Future runs will show provider, task mode, approvals, proposed patches, and deployment actions in one audit trail.
          </div>
          <div style={{ marginTop: 10, color: '#77847d' }}>
            This view is read-only; execution and approval gates remain behind the secure run lifecycle.
          </div>
        </div>
      ) : (
        runs.map(run => (
          <RunSummaryCard
            key={run.id}
            run={run}
            canceling={cancelingRunId === run.id}
            cancelDisabled={creatingRun || cancelingRunId !== null || decidingGateKey !== null}
            detailSelected={selectedRunId === run.id}
            detailLoading={detailLoadingRunId === run.id}
            detailsDisabled={creatingRun || (detailLoadingRunId !== null && detailLoadingRunId !== run.id)}
            decidingGateKey={decidingGateKey}
            decisionDisabled={creatingRun || cancelingRunId !== null || decidingGateKey !== null}
            onCancel={onCancelRun}
            onShowDetails={onShowDetails}
            onDecideApproval={onDecideApproval}
          />
        ))
      )}
      {(selectedRun || detailLoadingRunId || detailError) && (
        <RunDetailPanel
          run={selectedRun}
          selectedRunId={selectedRunId}
          loadingRunId={detailLoadingRunId}
          error={detailError}
          onClose={onCloseDetails}
        />
      )}
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
  projectName,
}: ChatPanelProps) {
  const [activeTab, setActiveTab] = useState<AgentHubTab>(() => readStoredAgentHubTab());
  const [activeTask, setActiveTask] = useState<typeof TASK_MODES[number]>(() => readStoredTaskMode());
  const [selectedProviders, setSelectedProviders] = useState<Partial<Record<ProviderTab, string>>>({});
  const [localProviders, setLocalProviders] = useState<Record<string, LocalAgentProviderStatus>>({});
  const [agentRuns, setAgentRuns] = useState<AgentRunSummary[]>([]);
  const [agentRunsLoading, setAgentRunsLoading] = useState(false);
  const [agentRunsError, setAgentRunsError] = useState<string | null>(null);
  const [creatingRun, setCreatingRun] = useState(false);
  const [cancelingRunId, setCancelingRunId] = useState<string | null>(null);
  const [decidingGateKey, setDecidingGateKey] = useState<string | null>(null);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [selectedRun, setSelectedRun] = useState<AgentRun | null>(null);
  const [detailLoadingRunId, setDetailLoadingRunId] = useState<string | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const latestProjectNameRef = useRef(projectName);
  const detailRequestKeyRef = useRef<string | null>(null);
  const detailRequestSeqRef = useRef(0);
  latestProjectNameRef.current = projectName;

  useEffect(() => {
    let cancelled = false;
    api.listLocalAgentProviders()
      .then(providers => {
        if (cancelled) return;
        setLocalProviders(Object.fromEntries(providers.map(provider => [provider.id, provider])));
      })
      .catch(() => {
        if (!cancelled) setLocalProviders({});
      });
    return () => {
      cancelled = true;
    };
  }, []);

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
    writeStoredAgentHubValue(AGENT_HUB_STORAGE_KEYS.activeTab, tab);
    if (focus) focusSelectedTab(tab);
  };

  const selectTask = (task: typeof TASK_MODES[number]) => {
    setActiveTask(task);
    writeStoredAgentHubValue(AGENT_HUB_STORAGE_KEYS.activeTask, task);
  };

  const selectProvider = (tab: ProviderTab, providerName: string) => {
    setSelectedProviders(current => ({ ...current, [tab]: providerName }));
  };

  useEffect(() => {
    if (activeTab !== 'chat') return;
    scrollAnchorRef?.current?.scrollIntoView?.({ block: 'nearest' });
  }, [activeTab, scrollAnchorRef]);

  useEffect(() => {
    if (activeTab !== 'runs' || !projectName) return;
    let cancelled = false;
    setAgentRunsLoading(true);
    setAgentRunsError(null);
    api.listAgentRuns(projectName)
      .then(runs => {
        if (cancelled) return;
        setAgentRuns(runs);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setAgentRuns([]);
        setAgentRunsError(err instanceof Error ? err.message : 'agent run list failed');
      })
      .finally(() => {
        if (!cancelled) setAgentRunsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [activeTab, projectName]);

  useEffect(() => {
    detailRequestKeyRef.current = null;
    setCreatingRun(false);
    setCancelingRunId(null);
    setDecidingGateKey(null);
    setSelectedRunId(null);
    setSelectedRun(null);
    setDetailLoadingRunId(null);
    setDetailError(null);
  }, [projectName]);

  const refreshAgentRunsForProject = (requestProjectName: string) => {
    if (latestProjectNameRef.current !== requestProjectName) return Promise.resolve();
    return api.listAgentRuns(requestProjectName)
      .then(runs => {
        if (latestProjectNameRef.current !== requestProjectName) return;
        setAgentRuns(runs);
      })
      .catch((err: unknown) => {
        if (latestProjectNameRef.current !== requestProjectName) return;
        setAgentRunsError(agentRunRefreshErrorMessage(err));
      });
  };

  const createAgentRun = () => {
    const requestProjectName = projectName;
    const prompt = input.trim();
    if (!requestProjectName || prompt.length === 0 || creatingRun || cancelingRunId || decidingGateKey || detailRequestKeyRef.current) return;
    setCreatingRun(true);
    setAgentRunsError(null);
    api.createAgentRun(requestProjectName, {
      prompt,
      mode: modeForTask(activeTask),
    })
      .then(() => refreshAgentRunsForProject(requestProjectName))
      .catch((err: unknown) => {
        if (isConflictError(err)) {
          return refreshAgentRunsForProject(requestProjectName);
        }
        if (latestProjectNameRef.current === requestProjectName) {
          setAgentRunsError(err instanceof Error ? err.message : 'agent run create failed');
        }
        return undefined;
      })
      .finally(() => {
        if (latestProjectNameRef.current === requestProjectName) {
          setCreatingRun(false);
        }
      });
  };

  const showAgentRunDetails = (id: string) => {
    const requestProjectName = projectName;
    if (!requestProjectName || detailRequestKeyRef.current) return;
    detailRequestSeqRef.current += 1;
    const requestKey = `${requestProjectName}:${id}:${detailRequestSeqRef.current}`;
    detailRequestKeyRef.current = requestKey;
    const isCurrentDetailRequest = () => (
      detailRequestKeyRef.current === requestKey && latestProjectNameRef.current === requestProjectName
    );
    setSelectedRunId(id);
    setSelectedRun(null);
    setDetailError(null);
    setDetailLoadingRunId(id);
    api.getAgentRun(requestProjectName, id)
      .then(run => {
        if (!isCurrentDetailRequest()) return;
        setSelectedRunId(run.id);
        setSelectedRun(run);
      })
      .catch((err: unknown) => {
        if (!isCurrentDetailRequest()) return;
        setSelectedRun(null);
        setDetailError(agentRunDetailErrorMessage(err));
      })
      .finally(() => {
        if (isCurrentDetailRequest()) {
          detailRequestKeyRef.current = null;
          setDetailLoadingRunId(null);
        }
      });
  };

  const closeAgentRunDetails = () => {
    detailRequestKeyRef.current = null;
    setSelectedRunId(null);
    setSelectedRun(null);
    setDetailError(null);
    setDetailLoadingRunId(null);
  };

  const cancelAgentRun = (id: string) => {
    const requestProjectName = projectName;
    if (!requestProjectName || cancelingRunId || decidingGateKey) return;
    setCancelingRunId(id);
    setAgentRunsError(null);
    api.cancelAgentRun(requestProjectName, id)
      .then(() => refreshAgentRunsForProject(requestProjectName))
      .catch((err: unknown) => {
        if (isConflictError(err)) {
          return refreshAgentRunsForProject(requestProjectName);
        }
        if (latestProjectNameRef.current === requestProjectName) {
          setAgentRunsError(err instanceof Error ? err.message : 'agent run cancel failed');
        }
        return undefined;
      })
      .finally(() => {
        if (latestProjectNameRef.current === requestProjectName) {
          setCancelingRunId(null);
        }
      });
  };

  const decideApprovalGate = (runId: string, approvalId: string, decision: AgentRunApprovalDecision) => {
    const requestProjectName = projectName;
    if (!requestProjectName || cancelingRunId || decidingGateKey) return;
    const gateKey = `${runId}:${approvalId}`;
    setDecidingGateKey(gateKey);
    setAgentRunsError(null);
    api.decideAgentRunApproval(requestProjectName, runId, approvalId, decision)
      .then(() => refreshAgentRunsForProject(requestProjectName))
      .catch((err: unknown) => {
        if (isConflictError(err)) {
          return refreshAgentRunsForProject(requestProjectName);
        }
        if (latestProjectNameRef.current === requestProjectName) {
          setAgentRunsError(err instanceof Error ? err.message : 'agent run approval decision failed');
        }
        return undefined;
      })
      .finally(() => {
        if (latestProjectNameRef.current === requestProjectName) {
          setDecidingGateKey(null);
        }
      });
  };

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
                onClick={() => selectTask(task)}
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
            <ProviderGroup
              key={tab}
              tab={tab}
              active={activeTab === tab}
              localProviders={localProviders}
              selectedProviderName={selectedProviders[tab]}
              onSelectProvider={selectProvider}
            />
          ))}

          <div
            role="tabpanel"
            id={panelId('runs')}
            aria-labelledby={tabId('runs')}
            hidden={activeTab !== 'runs'}
            style={panelStyle('runs')}
          >
            <RunsPanel
              projectName={projectName}
              runs={agentRuns}
              loading={agentRunsLoading}
              error={agentRunsError}
              task={activeTask}
              prompt={input}
              creatingRun={creatingRun}
              cancelingRunId={cancelingRunId}
              selectedRun={selectedRun}
              selectedRunId={selectedRunId}
              detailLoadingRunId={detailLoadingRunId}
              detailError={detailError}
              decidingGateKey={decidingGateKey}
              onCreateRun={createAgentRun}
              onCancelRun={cancelAgentRun}
              onShowDetails={showAgentRunDetails}
              onCloseDetails={closeAgentRunDetails}
              onDecideApproval={decideApprovalGate}
            />
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
