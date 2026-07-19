import { useEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, KeyboardEvent, ReactNode, RefObject } from 'react';
import { Pencil, Save, X } from 'lucide-react';

import { api, type AgentProviderConnectionDefinition, type AgentProviderConnectionProfile, type AgentRun, type AgentRunApprovalDecision, type AgentRunMode, type AgentRunSummary, type AgentToolPolicy, type AgentToolPolicyResponse, type LocalAgentProviderStatus } from '../../api';
import { S } from '../../styles';
import { ToolRoutePreviewPanel } from './ToolRoutePreviewPanel';

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
type RunProviderIdentity = { id: string; label: string };
type AgentToolPolicyView =
  | { status: 'idle' }
  | { status: 'loading' | 'missing' | 'error'; project: string; providerId: string }
  | { status: 'ready'; project: string; providerId: string; response: AgentToolPolicyResponse };
type SaveAgentToolPolicy = (
  _project: string,
  _providerId: string,
  _policy: AgentToolPolicy,
) => Promise<AgentToolPolicyResponse>;

const PROVIDER_TABS: ProviderTab[] = ['codex', 'claude', 'gemini', 'copilot', 'local', 'mcp'];

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
const AGENT_RUN_REFRESH_INTERVAL_MS = 5000;
const IDLE_AGENT_TOOL_POLICY_VIEW: AgentToolPolicyView = { status: 'idle' };
const AGENT_TOOL_POLICY_MODES = new Set(['read_only', 'propose_only', 'approved_execute']);
const AGENT_TOOL_POLICY_RISKS = new Set([
  'read_only',
  'generate_code',
  'modify_workspace',
  'cloud_mutation',
  'secret_sensitive',
  'destructive',
  'unknown',
]);
const AGENT_TOOL_POLICY_KEYS = new Set(['rules']);
const AGENT_TOOL_POLICY_RULE_KEYS = new Set([
  'project',
  'provider_id',
  'connection_id',
  'server_id',
  'tool_name',
  'modes',
  'risk',
  'effect',
  'approval_required',
]);

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

const PROVIDER_CONNECTION_FAMILIES: Record<ProviderTab, string[]> = {
  codex: ['openai', 'azure_openai', 'gateway'],
  claude: ['anthropic', 'gateway'],
  gemini: ['vertex', 'gateway'],
  copilot: ['gateway'],
  local: [],
  mcp: ['bedrock', 'vertex', 'gateway'],
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
const isProviderTab = (tab: AgentHubTab): tab is ProviderTab => (
  PROVIDER_TABS.includes(tab as ProviderTab)
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
  toolPolicy: { borderTop: '1px solid var(--border-soft)', marginTop: 10, paddingTop: 8 },
  toolPolicyHead: { display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' },
  toolPolicyTitle: { color: 'var(--text-main)', fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.4, marginRight: 'auto' },
  toolPolicyMessage: { color: 'var(--text-muted)', fontSize: 11, lineHeight: 1.4, marginTop: 6 },
  toolPolicyRule: { borderTop: '1px solid var(--border-soft)', paddingTop: 7, marginTop: 7, minWidth: 0 },
  toolPolicyRuleName: { color: 'var(--text-main)', fontSize: 11, fontWeight: 700, overflowWrap: 'anywhere' },
  toolPolicyRuleMeta: { color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', fontSize: 10, lineHeight: 1.4, marginTop: 3, overflowWrap: 'anywhere' },
  toolPolicyButton: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-soft)', borderRadius: 6, background: 'var(--bg-elev-3)', color: 'var(--text-main)', cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 11, fontFamily: 'DM Sans', fontWeight: 700, padding: '5px 8px' },
  toolPolicyEditor: { borderTop: '1px solid var(--border-soft)', marginTop: 8, paddingTop: 8 },
  toolPolicyScope: { color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', fontSize: 10, marginBottom: 6, overflowWrap: 'anywhere' },
  toolPolicyTextarea: { width: '100%', minHeight: 168, resize: 'vertical', borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 6, background: 'var(--bg-elev-1)', color: 'var(--text-main)', boxSizing: 'border-box', fontFamily: 'JetBrains Mono', fontSize: 10, lineHeight: 1.5, padding: 8 },
  toolPolicyActions: { display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 6, marginTop: 7 },
  toolPolicyError: { color: 'var(--accent-danger)', fontSize: 11, lineHeight: 1.4, marginTop: 6 },
  connectionCatalog: { gridColumn: '1 / -1', borderTop: '1px solid var(--border-soft)', paddingTop: 10, marginTop: 2, display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(210px, 1fr))', gap: 8 },
  connectionCatalogIntro: { gridColumn: '1 / -1', color: 'var(--text-muted)', fontSize: 12, lineHeight: 1.45 },
  connectionCard: { borderWidth: 1, borderStyle: 'solid', borderColor: 'var(--border-main)', borderRadius: 8, background: 'rgba(23, 29, 27, 0.68)', padding: 10, minWidth: 0 },
  connectionHint: { color: '#77847d', fontSize: 11, lineHeight: 1.4, marginTop: 6, overflowWrap: 'anywhere' },
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
  if (mode === 'secret_store') return 'Secret store';
  if (mode === 'cloud_connection') return 'Cloud Connection';
  if (mode === 'enterprise_sso') return 'Enterprise SSO';
  return mode;
}

function compactList(items: string[], empty = 'None') {
  if (items.length === 0) return empty;
  return items.map(item => item.replaceAll('_', ' ')).join(', ');
}

function connectionProvidersForTab(
  tab: ProviderTab,
  providers: AgentProviderConnectionDefinition[],
) {
  const families = PROVIDER_CONNECTION_FAMILIES[tab];
  if (families.length === 0) return [];
  return providers.filter(provider => families.includes(provider.family));
}

function inferredProviderConnectionFamily(providerId: string) {
  const id = providerId.toLowerCase();
  if (id.includes('azure') && id.includes('openai')) return 'azure_openai';
  if (id.includes('openai')) return 'openai';
  if (id.includes('anthropic') || id.includes('claude')) return 'anthropic';
  if (id.includes('bedrock')) return 'bedrock';
  if (id.includes('vertex') || id.includes('gemini')) return 'vertex';
  if (id.includes('gateway') || id.includes('enterprise')) return 'gateway';
  return '';
}

function connectionProfilesForTab(
  tab: ProviderTab,
  profiles: AgentProviderConnectionProfile[],
  providers: AgentProviderConnectionDefinition[],
) {
  const families = PROVIDER_CONNECTION_FAMILIES[tab];
  if (families.length === 0) return [];
  const familiesByProviderID = new Map(providers.map(provider => [provider.id, provider.family]));
  return profiles.filter(profile => {
    const family = familiesByProviderID.get(profile.provider_id) || inferredProviderConnectionFamily(profile.provider_id);
    return families.includes(family);
  });
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

function providerSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}

function providerIdForRun(tab: ProviderTab, provider: ProviderDefinition) {
  return provider.localProviderId ?? `${tab}-${providerSlug(provider.name)}`;
}

function selectedRunProviderId(tab: ProviderTab, selectedProviderName?: string) {
  const group = PROVIDER_GROUPS[tab];
  const provider = group.providers.find(item => item.name === selectedProviderName) ?? group.providers[0];
  return providerIdForRun(tab, provider);
}

function providerIdentityForRunId(providerId?: string): RunProviderIdentity | null {
  if (!providerId) return null;
  for (const tab of PROVIDER_TABS) {
    for (const provider of PROVIDER_GROUPS[tab].providers) {
      const id = providerIdForRun(tab, provider);
      if (id === providerId) return { id, label: provider.name };
    }
  }
  return { id: providerId, label: providerId };
}

function hasHTTPStatus(err: unknown, status: number) {
  return (
    typeof err === 'object'
    && err !== null
    && 'status' in err
    && (err as { status?: unknown }).status === status
  );
}

function matchesToolPolicyScope(
  response: AgentToolPolicyResponse,
  project: string,
  providerId: string,
) {
  return (
    response?.scope?.project === project
    && response.scope.provider_id === providerId
    && matchesToolPolicy(response.policy, project, providerId)
  );
}

function matchesToolPolicy(
  value: unknown,
  project: string,
  providerId: string,
): value is AgentToolPolicy {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const policy = value as Record<string, unknown>;
  if (!hasOnlyAllowedKeys(policy, AGENT_TOOL_POLICY_KEYS)) return false;
  const rules = policy.rules;
  return Array.isArray(rules) && rules.every(rule => matchesToolPolicyRule(rule, project, providerId));
}

function matchesToolPolicyRule(value: unknown, project: string, providerId: string) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const rule = value as Record<string, unknown>;
  if (!hasOnlyAllowedKeys(rule, AGENT_TOOL_POLICY_RULE_KEYS)) return false;
  const modes = rule.modes;
  const risk = rule.risk;
  const effect = rule.effect;
  const approvalRequired = rule.approval_required;

  if (
    rule.project !== project
    || rule.provider_id !== providerId
    || !validPolicyField(rule.connection_id)
    || !validPolicyField(rule.server_id)
    || !validPolicyField(rule.tool_name)
    || !Array.isArray(modes)
    || modes.length === 0
    || !modes.every(mode => typeof mode === 'string' && AGENT_TOOL_POLICY_MODES.has(mode))
    || typeof risk !== 'string'
    || !AGENT_TOOL_POLICY_RISKS.has(risk)
    || (effect !== 'allow' && effect !== 'deny')
    || (approvalRequired !== undefined && typeof approvalRequired !== 'boolean')
  ) {
    return false;
  }
  if (effect === 'deny') return approvalRequired !== true;
  if (risk === 'unknown') return false;
  return risk === 'read_only' || approvalRequired === true;
}

function validPolicyField(value: unknown) {
  return typeof value === 'string' && value.length > 0 && value.trim() === value;
}

function hasOnlyAllowedKeys(value: Record<string, unknown>, allowed: Set<string>) {
  return Object.keys(value).every(key => allowed.has(key));
}

function toolPolicyScopeKey(project: string, providerId: string) {
  return `${project}\u0000${providerId}`;
}

function toolPolicyViewForScope(
  view: AgentToolPolicyView,
  project: string | undefined,
  providerId: string,
): AgentToolPolicyView {
  if (!project) return IDLE_AGENT_TOOL_POLICY_VIEW;
  if (view.status !== 'idle' && view.project === project && view.providerId === providerId) {
    return view;
  }
  return { status: 'loading', project, providerId };
}

function ToolPolicySummary({
  providerName,
  view,
  onSave,
}: {
  providerName: string;
  view: AgentToolPolicyView;
  onSave: SaveAgentToolPolicy;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const mountedRef = useRef(true);
  const rules = view.status === 'ready' ? view.response.policy.rules : [];
  const allowed = rules.filter(rule => rule.effect === 'allow').length;
  const denied = rules.filter(rule => rule.effect === 'deny').length;
  const approvals = rules.filter(rule => rule.approval_required).length;
  const editable = view.status === 'ready' || view.status === 'missing';

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  let message = '';
  if (view.status === 'idle') message = 'Project scope required. MCP tool access is blocked.';
  if (view.status === 'loading') message = 'Checking scoped tool permissions...';
  if (view.status === 'missing') message = 'No scoped policy. MCP tool access is blocked.';
  if (view.status === 'error') message = 'Policy unavailable. MCP tool access remains blocked.';
  if (view.status === 'ready' && rules.length === 0) message = 'No routes allowed. MCP tool access is blocked.';

  const beginEdit = () => {
    if (view.status !== 'ready' && view.status !== 'missing') return;
    setDraft(JSON.stringify(view.status === 'ready' ? view.response.policy : { rules: [] }, null, 2));
    setSaveError(null);
    setEditing(true);
  };

  const cancelEdit = () => {
    if (saving) return;
    setEditing(false);
    setSaveError(null);
  };

  const savePolicy = async () => {
    if ((view.status !== 'ready' && view.status !== 'missing') || saving) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(draft);
    } catch {
      setSaveError('Policy must be valid JSON.');
      return;
    }
    if (!matchesToolPolicy(parsed, view.project, view.providerId)) {
      setSaveError('Policy contains invalid fields or rules for this project and provider.');
      return;
    }

    setSaving(true);
    setSaveError(null);
    try {
      await onSave(view.project, view.providerId, parsed);
      if (!mountedRef.current) return;
      setEditing(false);
    } catch {
      if (!mountedRef.current) return;
      setSaveError('Policy save could not be confirmed. Reload before retrying.');
    } finally {
      if (mountedRef.current) setSaving(false);
    }
  };

  return (
    <section style={hubStyles.toolPolicy} aria-label={`${providerName} tool permissions`}>
      <div style={hubStyles.toolPolicyHead}>
        <span style={hubStyles.toolPolicyTitle}>Tool permissions</span>
        {view.status === 'ready' && (
          <>
            <span style={hubStyles.badge}>{allowed} allowed</span>
            <span style={hubStyles.badge}>{denied} denied</span>
            <span style={hubStyles.badge}>{approvals} approvals</span>
          </>
        )}
        {editable && !editing && (
          <button
            type="button"
            style={hubStyles.toolPolicyButton}
            onClick={beginEdit}
            aria-label={`Edit ${providerName} tool policy`}
          >
            <Pencil size={12} aria-hidden="true" />
            Edit
          </button>
        )}
      </div>
      {message && <div role="status" style={hubStyles.toolPolicyMessage}>{message}</div>}
      {rules.map((rule, index) => (
        <div
          key={`${rule.connection_id}:${rule.server_id}:${rule.tool_name}:${index}`}
          style={hubStyles.toolPolicyRule}
        >
          <div style={hubStyles.toolPolicyHead}>
            <span style={{ ...hubStyles.toolPolicyRuleName, flex: 1 }}>
              {rule.server_id} / {rule.tool_name}
            </span>
            <span style={hubStyles.badge}>{rule.effect}</span>
            {rule.approval_required && <span style={hubStyles.badge}>approval required</span>}
          </div>
          <div style={hubStyles.toolPolicyRuleMeta}>
            {rule.connection_id} · {rule.modes.map(mode => mode.replaceAll('_', ' ')).join(', ')} · {rule.risk.replaceAll('_', ' ')}
          </div>
        </div>
      ))}
      {editing && view.status !== 'idle' && (
        <div style={hubStyles.toolPolicyEditor}>
          <div style={hubStyles.toolPolicyScope}>
            {view.project} / {view.providerId}
          </div>
          <textarea
            aria-label={`${providerName} policy JSON`}
            value={draft}
            onChange={event => setDraft(event.target.value)}
            disabled={saving}
            spellCheck={false}
            style={hubStyles.toolPolicyTextarea}
          />
          {saveError && <div role="alert" style={hubStyles.toolPolicyError}>{saveError}</div>}
          <div style={hubStyles.toolPolicyActions}>
            <button
              type="button"
              style={{ ...hubStyles.toolPolicyButton, ...(saving ? { cursor: 'not-allowed', opacity: 0.6 } : {}) }}
              onClick={cancelEdit}
              disabled={saving}
            >
              <X size={12} aria-hidden="true" />
              Cancel
            </button>
            <button
              type="button"
              style={{ ...hubStyles.toolPolicyButton, ...(saving ? { cursor: 'not-allowed', opacity: 0.6 } : {}) }}
              onClick={savePolicy}
              disabled={saving}
            >
              <Save size={12} aria-hidden="true" />
              {saving ? 'Saving...' : 'Save policy'}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}

function ProviderDetails({
  provider,
  displayState,
  displayNote,
  localStatus,
  toolPolicyView,
  onSaveToolPolicy,
}: {
  provider: ProviderDefinition;
  displayState: ProviderState;
  displayNote: string;
  localStatus?: LocalAgentProviderStatus;
  toolPolicyView: AgentToolPolicyView;
  onSaveToolPolicy: SaveAgentToolPolicy;
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
      <ToolPolicySummary
        key={toolPolicyView.status === 'idle'
          ? 'idle'
          : toolPolicyScopeKey(toolPolicyView.project, toolPolicyView.providerId)}
        providerName={provider.name}
        view={toolPolicyView}
        onSave={onSaveToolPolicy}
      />
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

function ConnectionCatalog({
  title,
  providers,
}: {
  title: string;
  providers: AgentProviderConnectionDefinition[];
}) {
  if (providers.length === 0) return null;
  return (
    <section
      role="region"
      aria-label={`${title} API and enterprise connection catalog`}
      style={hubStyles.connectionCatalog}
    >
      <div style={hubStyles.connectionCatalogIntro}>
        <strong style={{ color: 'var(--text-main)' }}>API and enterprise connections</strong>
        <span> - read-only catalog metadata. Credentials stay behind local or external secret stores.</span>
      </div>
      {providers.map(provider => (
        <article key={provider.id} style={hubStyles.connectionCard} aria-label={`${provider.name} connection`}>
          <div style={hubStyles.providerHead}>
            <span style={{ ...hubStyles.providerName, flex: 1 }}>{provider.name}</span>
            <span style={hubStyles.badge}>{provider.category.replaceAll('_', ' ')}</span>
          </div>
          <div style={hubStyles.providerMeta}>
            <span style={hubStyles.badge}>{credentialLabel(provider.credential_mode)}</span>
            <span style={hubStyles.badge}>{provider.family.replaceAll('_', ' ')}</span>
          </div>
          <div style={hubStyles.connectionHint}>{provider.billing_hint}</div>
          <div style={hubStyles.connectionHint}>{provider.data_handling_hint}</div>
          <div style={hubStyles.connectionHint}>{provider.secret_storage_hint}</div>
          <div style={hubStyles.providerDetailsGrid}>
            <div>
              <div style={hubStyles.providerDetailLabel}>Required</div>
              <div style={hubStyles.providerDetailValue} title={compactList(provider.required_fields)}>
                {compactList(provider.required_fields)}
              </div>
            </div>
            <div>
              <div style={hubStyles.providerDetailLabel}>Secrets</div>
              <div style={hubStyles.providerDetailValue} title={compactList(provider.secret_fields)}>
                {compactList(provider.secret_fields)}
              </div>
            </div>
          </div>
          {provider.capabilities.length > 0 && (
            <div style={hubStyles.providerCapabilityList} aria-label={`${provider.name} capabilities`}>
              {provider.capabilities.map(capability => (
                <span key={capability} style={hubStyles.badge}>{capability.replaceAll('_', ' ')}</span>
              ))}
            </div>
          )}
          {provider.cost_controls.length > 0 && (
            <div style={hubStyles.providerCapabilityList} aria-label={`${provider.name} cost controls`}>
              {provider.cost_controls.map(control => (
                <span key={control} style={hubStyles.badge}>{control.replaceAll('_', ' ')}</span>
              ))}
            </div>
          )}
        </article>
      ))}
    </section>
  );
}

function SavedConnectionProfiles({
  title,
  profiles,
}: {
  title: string;
  profiles: AgentProviderConnectionProfile[];
}) {
  if (profiles.length === 0) return null;
  return (
    <section
      role="region"
      aria-label={`${title} saved provider connections`}
      style={hubStyles.connectionCatalog}
    >
      <div style={hubStyles.connectionCatalogIntro}>
        <strong style={{ color: 'var(--text-main)' }}>Saved provider profiles</strong>
        <span> - redacted inventory only. Secret values and external refs are never shown here.</span>
      </div>
      {profiles.map(profile => (
        <article key={profile.id} style={hubStyles.connectionCard} aria-label={`${profile.name} saved provider connection`}>
          <div style={hubStyles.providerHead}>
            <span style={{ ...hubStyles.providerName, flex: 1 }}>{profile.name}</span>
            <span style={hubStyles.badge}>{profile.provider_id}</span>
          </div>
          <div style={hubStyles.providerMeta}>
            <span style={hubStyles.badge}>{credentialLabel(profile.credential_mode)}</span>
            <span style={hubStyles.badge}>{profile.secret_store || 'No secret store'}</span>
            <span style={hubStyles.badge}>
              {(profile.secret_fields || []).length} secret {(profile.secret_fields || []).length === 1 ? 'field' : 'fields'}
            </span>
          </div>
          <div style={hubStyles.providerDetailsGrid}>
            <div>
              <div style={hubStyles.providerDetailLabel}>Metadata</div>
              <div style={hubStyles.providerDetailValue} title={compactList(Object.keys(profile.metadata || {}))}>
                {compactList(Object.keys(profile.metadata || {}))}
              </div>
            </div>
            <div>
              <div style={hubStyles.providerDetailLabel}>Cost controls</div>
              <div style={hubStyles.providerDetailValue} title={compactList(Object.keys(profile.cost_controls || {}))}>
                {compactList(Object.keys(profile.cost_controls || {}))}
              </div>
            </div>
          </div>
          {(profile.secret_fields || []).length > 0 && (
            <div style={hubStyles.providerCapabilityList} aria-label={`${profile.name} secret fields`}>
              {(profile.secret_fields || []).map(field => (
                <span key={field} style={hubStyles.badge}>{field.replaceAll('_', ' ')}</span>
              ))}
            </div>
          )}
        </article>
      ))}
    </section>
  );
}

function ProviderGroup({
  tab,
  active,
  localProviders,
  connectionProviders,
  connectionProfiles,
  toolPolicyView,
  selectedProviderName,
  onSelectProvider,
  onSaveToolPolicy,
}: {
  tab: ProviderTab;
  active: boolean;
  localProviders: Record<string, LocalAgentProviderStatus>;
  connectionProviders: AgentProviderConnectionDefinition[];
  connectionProfiles: AgentProviderConnectionProfile[];
  toolPolicyView: AgentToolPolicyView;
  selectedProviderName?: string;
  onSelectProvider: (tab: ProviderTab, providerName: string) => void;
  onSaveToolPolicy: SaveAgentToolPolicy;
}) {
  const group = PROVIDER_GROUPS[tab];
  const visibleConnectionProviders = connectionProvidersForTab(tab, connectionProviders);
  const visibleConnectionProfiles = connectionProfilesForTab(tab, connectionProfiles, connectionProviders);
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
        toolPolicyView={toolPolicyView}
        onSaveToolPolicy={onSaveToolPolicy}
      />
      <ConnectionCatalog title={group.title} providers={visibleConnectionProviders} />
      <SavedConnectionProfiles title={group.title} profiles={visibleConnectionProfiles} />
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

function ProviderBadge({ providerId }: { providerId?: string }) {
  const provider = providerIdentityForRunId(providerId);
  if (!provider) return null;
  return (
    <span
      style={hubStyles.badge}
      title={provider.id}
      aria-label={`Provider ${provider.label}`}
    >
      {provider.label}
    </span>
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
        <ProviderBadge providerId={run.provider_id} />
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
  providerId,
  creating,
  disabled,
  onCreate,
}: {
  task: typeof TASK_MODES[number];
  prompt: string;
  providerId: string;
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
        <ProviderBadge providerId={providerId} />
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
        <ProviderBadge providerId={run.provider_id} />
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
  providerId,
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
  providerId: string;
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
  const routePreviewScope = selectedRun
    && selectedRunId === selectedRun.id
    && selectedRun.project === projectName
    && !isTerminalAgentRun(selectedRun.status)
    ? { projectName, runId: selectedRun.id }
    : null;
  return (
    <div style={hubStyles.runsPanel} aria-label={`${projectName} agent runs`}>
      <RunQueueCard
        task={task}
        prompt={prompt}
        providerId={providerId}
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
      {routePreviewScope && (
        <ToolRoutePreviewPanel
          key={`${routePreviewScope.projectName}:${routePreviewScope.runId}`}
          projectName={routePreviewScope.projectName}
          runId={routePreviewScope.runId}
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
  const [runProviderTab, setRunProviderTab] = useState<ProviderTab>(() => {
    const storedTab = readStoredAgentHubTab();
    return isProviderTab(storedTab) ? storedTab : 'local';
  });
  const [activeTask, setActiveTask] = useState<typeof TASK_MODES[number]>(() => readStoredTaskMode());
  const [selectedProviders, setSelectedProviders] = useState<Partial<Record<ProviderTab, string>>>({});
  const [localProviders, setLocalProviders] = useState<Record<string, LocalAgentProviderStatus>>({});
  const [connectionProviders, setConnectionProviders] = useState<AgentProviderConnectionDefinition[]>([]);
  const [connectionProfiles, setConnectionProfiles] = useState<AgentProviderConnectionProfile[]>([]);
  const [toolPolicyView, setToolPolicyView] = useState<AgentToolPolicyView>(IDLE_AGENT_TOOL_POLICY_VIEW);
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
  const latestToolPolicyScopeRef = useRef<string | null>(null);
  const agentRunsListSeqRef = useRef(0);
  const agentRunsRefreshInFlightRef = useRef(false);
  const detailRequestKeyRef = useRef<string | null>(null);
  const detailPollInFlightRef = useRef(false);
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

  useEffect(() => {
    let cancelled = false;
    api.listAgentProviderConnections()
      .then(providers => {
        if (!cancelled) setConnectionProviders(providers);
      })
      .catch(() => {
        if (!cancelled) setConnectionProviders([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    api.listAgentProviderConnectionProfiles()
      .then(profiles => {
        if (!cancelled) setConnectionProfiles(profiles);
      })
      .catch(() => {
        if (!cancelled) setConnectionProfiles([]);
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
  const runProviderId = useMemo(
    () => selectedRunProviderId(runProviderTab, selectedProviders[runProviderTab]),
    [runProviderTab, selectedProviders],
  );
  latestToolPolicyScopeRef.current = projectName && isProviderTab(activeTab)
    ? toolPolicyScopeKey(projectName, runProviderId)
    : null;
  const hasActiveAgentRuns = agentRuns.some(run => !isTerminalAgentRun(run.status));

  useEffect(() => {
    if (!projectName || !isProviderTab(activeTab)) {
      setToolPolicyView(IDLE_AGENT_TOOL_POLICY_VIEW);
      return;
    }

    let cancelled = false;
    const requestProject = projectName;
    const requestProvider = runProviderId;
    const requestScope = { project: requestProject, providerId: requestProvider };
    setToolPolicyView({ status: 'loading', ...requestScope });
    api.getAgentToolPolicy(requestProject, requestProvider)
      .then(response => {
        if (cancelled) return;
        if (!matchesToolPolicyScope(response, requestProject, requestProvider)) {
          setToolPolicyView({ status: 'error', ...requestScope });
          return;
        }
        setToolPolicyView({ status: 'ready', response, ...requestScope });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setToolPolicyView({
          status: hasHTTPStatus(err, 404) ? 'missing' : 'error',
          ...requestScope,
        });
      });

    return () => {
      cancelled = true;
    };
  }, [activeTab, projectName, runProviderId]);

  const saveToolPolicy: SaveAgentToolPolicy = async (project, providerId, policy) => {
    const response = await api.saveAgentToolPolicy(project, providerId, policy);
    if (!matchesToolPolicyScope(response, project, providerId)) {
      throw new Error('agent tool policy response scope mismatch');
    }
    if (latestToolPolicyScopeRef.current === toolPolicyScopeKey(project, providerId)) {
      setToolPolicyView({ status: 'ready', project, providerId, response });
    }
    return response;
  };

  const panelStyle = (tab: AgentHubTab) => (
    activeTab === tab ? hubStyles.tabPanel : hubStyles.hiddenTabPanel
  );

  const focusSelectedTab = (tab: AgentHubTab) => {
    if (typeof document === 'undefined') return;
    window.setTimeout(() => document.getElementById(tabId(tab))?.focus(), 0);
  };

  const selectTab = (tab: AgentHubTab, focus = false) => {
    setActiveTab(tab);
    if (isProviderTab(tab)) setRunProviderTab(tab);
    writeStoredAgentHubValue(AGENT_HUB_STORAGE_KEYS.activeTab, tab);
    if (focus) focusSelectedTab(tab);
  };

  const selectTask = (task: typeof TASK_MODES[number]) => {
    setActiveTask(task);
    writeStoredAgentHubValue(AGENT_HUB_STORAGE_KEYS.activeTask, task);
  };

  const selectProvider = (tab: ProviderTab, providerName: string) => {
    setRunProviderTab(tab);
    setSelectedProviders(current => ({ ...current, [tab]: providerName }));
  };

  useEffect(() => {
    if (activeTab !== 'chat') return;
    scrollAnchorRef?.current?.scrollIntoView?.({ block: 'nearest' });
  }, [activeTab, scrollAnchorRef]);

  useEffect(() => {
    if (activeTab !== 'runs' || !projectName) return;
    let cancelled = false;
    agentRunsListSeqRef.current += 1;
    const requestSeq = agentRunsListSeqRef.current;
    const isCurrentListRequest = () => (
      !cancelled
      && agentRunsListSeqRef.current === requestSeq
      && latestProjectNameRef.current === projectName
    );
    setAgentRunsLoading(true);
    setAgentRunsError(null);
    api.listAgentRuns(projectName)
      .then(runs => {
        if (!isCurrentListRequest()) return;
        setAgentRuns(runs);
      })
      .catch((err: unknown) => {
        if (!isCurrentListRequest()) return;
        setAgentRuns([]);
        setAgentRunsError(err instanceof Error ? err.message : 'agent run list failed');
      })
      .finally(() => {
        if (isCurrentListRequest()) setAgentRunsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [activeTab, projectName]);

  useEffect(() => {
    detailRequestKeyRef.current = null;
    detailPollInFlightRef.current = false;
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
    agentRunsListSeqRef.current += 1;
    const requestSeq = agentRunsListSeqRef.current;
    const isCurrentListRequest = () => (
      agentRunsListSeqRef.current === requestSeq
      && latestProjectNameRef.current === requestProjectName
    );
    return api.listAgentRuns(requestProjectName)
      .then(runs => {
        if (!isCurrentListRequest()) return;
        setAgentRuns(runs);
      })
      .catch((err: unknown) => {
        if (!isCurrentListRequest()) return;
        setAgentRunsError(agentRunRefreshErrorMessage(err));
      })
      .finally(() => {
        if (isCurrentListRequest()) setAgentRunsLoading(false);
      });
  };

  useEffect(() => {
    if (activeTab !== 'runs' || !projectName || !hasActiveAgentRuns || agentRunsLoading) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      if (agentRunsRefreshInFlightRef.current) return;
      const requestProjectName = projectName;
      agentRunsListSeqRef.current += 1;
      const requestSeq = agentRunsListSeqRef.current;
      const isCurrentListRequest = () => (
        !cancelled
        && agentRunsListSeqRef.current === requestSeq
        && latestProjectNameRef.current === requestProjectName
      );
      agentRunsRefreshInFlightRef.current = true;
      void api.listAgentRuns(requestProjectName)
        .then(runs => {
          if (!isCurrentListRequest()) return;
          setAgentRuns(runs);
          setAgentRunsError(null);
        })
        .catch((err: unknown) => {
          if (!isCurrentListRequest()) return;
          setAgentRunsError(agentRunRefreshErrorMessage(err));
        })
        .finally(() => {
          if (isCurrentListRequest()) setAgentRunsLoading(false);
          if (!cancelled) agentRunsRefreshInFlightRef.current = false;
        });
    }, AGENT_RUN_REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      agentRunsRefreshInFlightRef.current = false;
      window.clearInterval(timer);
    };
  }, [activeTab, projectName, hasActiveAgentRuns, agentRunsLoading]);

  useEffect(() => {
    const requestProjectName = projectName;
    const requestRun = selectedRun;
    const requestRunId = requestRun?.id;
    if (
      activeTab !== 'runs'
      || !requestProjectName
      || !requestRunId
      || isTerminalAgentRun(requestRun.status)
    ) {
      return;
    }

    let cancelled = false;
    const timer = window.setInterval(() => {
      if (detailRequestKeyRef.current || detailPollInFlightRef.current) return;
      detailPollInFlightRef.current = true;
      const isCurrentDetailPoll = () => (
        !cancelled
        && latestProjectNameRef.current === requestProjectName
        && selectedRunId === requestRunId
      );
      api.getAgentRun(requestProjectName, requestRunId)
        .then(run => {
          if (!isCurrentDetailPoll()) return;
          setSelectedRunId(run.id);
          setSelectedRun(run);
          setDetailError(null);
        })
        .catch((err: unknown) => {
          if (!isCurrentDetailPoll()) return;
          setDetailError(agentRunDetailErrorMessage(err));
        })
        .finally(() => {
          if (!cancelled) detailPollInFlightRef.current = false;
        });
    }, AGENT_RUN_REFRESH_INTERVAL_MS);

    return () => {
      cancelled = true;
      detailPollInFlightRef.current = false;
      window.clearInterval(timer);
    };
  }, [activeTab, projectName, selectedRun?.id, selectedRun?.status, selectedRunId]);

  const createAgentRun = () => {
    const requestProjectName = projectName;
    const prompt = input.trim();
    if (!requestProjectName || prompt.length === 0 || creatingRun || cancelingRunId || decidingGateKey || detailRequestKeyRef.current) return;
    setCreatingRun(true);
    setAgentRunsError(null);
    api.createAgentRun(requestProjectName, {
      prompt,
      mode: modeForTask(activeTask),
      provider_id: runProviderId,
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

          {PROVIDER_TABS.map(tab => (
            <ProviderGroup
              key={tab}
              tab={tab}
              active={activeTab === tab}
              localProviders={localProviders}
              connectionProviders={connectionProviders}
              connectionProfiles={connectionProfiles}
              toolPolicyView={activeTab === tab
                ? toolPolicyViewForScope(toolPolicyView, projectName, runProviderId)
                : IDLE_AGENT_TOOL_POLICY_VIEW}
              selectedProviderName={selectedProviders[tab]}
              onSelectProvider={selectProvider}
              onSaveToolPolicy={saveToolPolicy}
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
              providerId={runProviderId}
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
