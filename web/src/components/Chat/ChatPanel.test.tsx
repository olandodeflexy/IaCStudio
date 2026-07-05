import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, fireEvent, waitFor, within } from '@testing-library/react';

import type { AgentRun, AgentRunSummary } from '../../api';
import { ChatPanel } from './ChatPanel';

const listLocalAgentProvidersMock = vi.hoisted(() => vi.fn());
const listAgentRunsMock = vi.hoisted(() => vi.fn());
const createAgentRunMock = vi.hoisted(() => vi.fn());
const getAgentRunMock = vi.hoisted(() => vi.fn());
const cancelAgentRunMock = vi.hoisted(() => vi.fn());
const decideAgentRunApprovalMock = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  api: {
    listLocalAgentProviders: listLocalAgentProvidersMock,
    listAgentRuns: listAgentRunsMock,
    createAgentRun: createAgentRunMock,
    getAgentRun: getAgentRunMock,
    cancelAgentRun: cancelAgentRunMock,
    decideAgentRunApproval: decideAgentRunApprovalMock,
  },
}));

describe('ChatPanel', () => {
  const baseProps = {
    messages: [],
    input: '',
    onInputChange: vi.fn(),
    onSubmit: vi.fn(),
    loading: false,
    toolColor: '#2FB5A8',
  };
  const agentRunFixture = (overrides: Partial<AgentRun> = {}): AgentRun => ({
    id: 'run_fixture',
    project: 'demo',
    provider_id: 'codex',
    mode: 'read_only',
    status: 'completed',
    prompt_preview: 'Fixture run',
    prompt_hash: 'sha256:fixture',
    created_at: '2026-07-01T10:00:00Z',
    updated_at: '2026-07-01T10:00:00Z',
    canceled: false,
    log_count: 0,
    patch_count: 0,
    approval_count: 0,
    pending_approval_count: 0,
    logs: [],
    patches: [],
    approvals: [],
    ...overrides,
  });
  const agentRunSummaryFixture = (overrides: Partial<AgentRunSummary> = {}): AgentRunSummary => ({
    id: 'run_fixture',
    project: 'demo',
    provider_id: 'codex',
    mode: 'read_only',
    status: 'completed',
    prompt_preview: 'Fixture run',
    prompt_hash: 'sha256:fixture',
    created_at: '2026-07-01T10:00:00Z',
    updated_at: '2026-07-01T10:00:00Z',
    canceled: false,
    log_count: 0,
    patch_count: 0,
    approval_count: 0,
    pending_approval_count: 0,
    ...overrides,
  });
  const flushAsyncUpdates = async () => {
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
  };

  beforeEach(() => {
    listLocalAgentProvidersMock.mockReset();
    listLocalAgentProvidersMock.mockReturnValue(new Promise(() => {}));
    listAgentRunsMock.mockReset();
    listAgentRunsMock.mockResolvedValue([]);
    createAgentRunMock.mockReset();
    createAgentRunMock.mockResolvedValue(agentRunFixture());
    getAgentRunMock.mockReset();
    getAgentRunMock.mockResolvedValue(agentRunFixture());
    cancelAgentRunMock.mockReset();
    cancelAgentRunMock.mockResolvedValue({});
    decideAgentRunApprovalMock.mockReset();
    decideAgentRunApprovalMock.mockResolvedValue({});
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders the empty-state hint when no messages are present', () => {
    render(<ChatPanel {...baseProps} />);
    expect(screen.getByText('Agent Hub')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Codex' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Claude Code' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Gemini' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Copilot' })).toBeInTheDocument();
    expect(screen.getByText('Read-only default')).toBeInTheDocument();
    expect(screen.getByText('No secret prompts')).toBeInTheDocument();
    expect(screen.getByText(/Ask me to create infrastructure/)).toBeInTheDocument();
  });

  it('renders messages in order, with an AI bullet only on AI turns', () => {
    render(
      <ChatPanel
        {...baseProps}
        messages={[
          { role: 'user', text: 'add a vpc' },
          { role: 'ai', text: 'on it' },
        ]}
      />,
    );
    expect(screen.getByText('add a vpc')).toBeInTheDocument();
    expect(screen.getByText('on it')).toBeInTheDocument();
  });

  it('submits via Enter in the input', () => {
    const onSubmit = vi.fn();
    render(<ChatPanel {...baseProps} onSubmit={onSubmit} input="hello" />);
    fireEvent.keyDown(screen.getByPlaceholderText(/Describe infrastructure/), { key: 'Enter' });
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('disables send + input while loading', () => {
    render(<ChatPanel {...baseProps} loading />);
    const input = screen.getByPlaceholderText(/Describe infrastructure/);
    expect(input).toBeDisabled();
    expect(screen.getByLabelText('Send message')).toBeDisabled();
    expect(screen.getByText('✦ Thinking...')).toBeInTheDocument();
  });

  it('shows the Codex provider lane without requiring API keys by default', () => {
    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    expect(within(codexPanel).getByRole('button', { name: /Codex CLI/ })).toBeInTheDocument();
    expect(within(codexPanel).getByRole('button', { name: /OpenAI API/ })).toBeInTheDocument();
    expect(within(codexPanel).getAllByText(/official CLI session/).length).toBeGreaterThan(0);
    expect(within(codexPanel).getByText(/Platform API account/)).toBeInTheDocument();
  });

  it('shows detected local provider status in provider lanes', async () => {
    listLocalAgentProvidersMock.mockResolvedValueOnce([{
      id: 'codex',
      name: 'Codex CLI',
      category: 'local_agent',
      state: 'available',
      installed: true,
      command: 'codex',
      entrypoint: 'codex',
      candidates: ['codex'],
      version: 'unknown',
      capabilities: ['chat', 'local_cli'],
      credential_mode: 'external_login',
      auth_hint: 'Use the official local Codex sign-in.',
    }]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await waitFor(() => {
      expect(within(codexPanel).getByText('Detected: codex')).toBeInTheDocument();
    });
    expect(within(codexPanel).getAllByText('External login').length).toBeGreaterThan(0);
    expect(within(codexPanel).getByText('Version unknown')).toBeInTheDocument();
    expect(within(codexPanel).getAllByText('local cli').length).toBeGreaterThan(0);
  });

  it('shows read-only details for the selected provider card', () => {
    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const codexCli = within(codexPanel).getByRole('button', { name: /Codex CLI/ });
    const openAiApi = within(codexPanel).getByRole('button', { name: /OpenAI API/ });
    expect(codexCli).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(openAiApi);

    expect(codexCli).toHaveAttribute('aria-pressed', 'false');
    expect(openAiApi).toHaveAttribute('aria-pressed', 'true');
    const details = within(codexPanel).getByRole('region', { name: 'OpenAI API details' });
    expect(within(details).getByText('Credential')).toBeInTheDocument();
    expect(within(details).getByText('Provider managed')).toBeInTheDocument();
    expect(within(details).getByText('Entrypoint')).toBeInTheDocument();
    expect(within(details).getAllByText('Not applicable')).toHaveLength(2);
    expect(within(details).getByRole('button', { name: 'Configure API' })).toBeDisabled();

    fireEvent.click(within(codexPanel).getByRole('button', { name: /Managed Codex token/ }));
    const enterpriseDetails = within(codexPanel).getByRole('region', { name: 'Managed Codex token details' });
    expect(within(enterpriseDetails).getByRole('button', { name: 'Use enterprise policy' })).toBeDisabled();
  });

  it('includes detected local provider details in the selected provider panel', async () => {
    listLocalAgentProvidersMock.mockResolvedValueOnce([{
      id: 'codex',
      name: 'Codex CLI',
      category: 'local_agent',
      state: 'available',
      installed: true,
      command: 'codex',
      entrypoint: 'codex',
      candidates: ['codex'],
      version: 'unknown',
      capabilities: ['chat', 'local_cli'],
      credential_mode: 'external_login',
      auth_hint: 'Use the official local Codex sign-in.',
    }]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await waitFor(() => {
      expect(within(codexPanel).getByText('Detected: codex')).toBeInTheDocument();
    });

    const details = within(codexPanel).getByRole('region', { name: 'Codex CLI details' });
    expect(within(details).getByText('Entrypoint')).toBeInTheDocument();
    expect(within(details).getByText('codex')).toBeInTheDocument();
    const selectedCapabilities = within(details).getByLabelText('Codex CLI selected capabilities');
    expect(within(selectedCapabilities).getByText('chat')).toBeInTheDocument();
    expect(within(selectedCapabilities).getByText('local cli')).toBeInTheDocument();
    expect(within(details).getByRole('button', { name: 'Use local CLI' })).toBeDisabled();
  });

  it('links provider tabs to stable tabpanels and supports roving keyboard selection', () => {
    render(<ChatPanel {...baseProps} />);

    const chatTab = screen.getByRole('tab', { name: 'Chat' });
    const codexTab = screen.getByRole('tab', { name: 'Codex' });
    const tabList = screen.getByRole('tablist', { name: 'Agent Hub providers' });
    expect(tabList).toHaveAttribute('aria-orientation', 'vertical');
    expect(chatTab).toHaveAttribute('id', 'agent-hub-tab-chat');
    expect(chatTab).toHaveAttribute('aria-controls', 'agent-hub-panel-chat');
    expect(codexTab).toHaveAttribute('aria-controls', 'agent-hub-panel-codex');
    expect(chatTab).toHaveAttribute('tabindex', '0');
    expect(codexTab).toHaveAttribute('tabindex', '-1');
    expect(document.getElementById('agent-hub-panel-codex')).toHaveAttribute('role', 'tabpanel');
    expect(screen.getByRole('tabpanel', { name: 'Chat' })).toHaveAttribute('id', 'agent-hub-panel-chat');

    fireEvent.keyDown(chatTab, { key: 'ArrowDown' });

    expect(codexTab).toHaveAttribute('aria-selected', 'true');
    expect(codexTab).toHaveAttribute('tabindex', '0');
    expect(screen.getByRole('tabpanel', { name: 'Codex' })).toHaveAttribute('id', 'agent-hub-panel-codex');
  });

  it('exposes task mode selection to assistive technology', () => {
    render(<ChatPanel {...baseProps} />);

    const taskModes = screen.getByRole('group', { name: 'Agent task modes' });
    const reviewProject = within(taskModes).getByRole('button', { name: 'Review project' });
    const generateIac = within(taskModes).getByRole('button', { name: 'Generate IaC' });
    expect(reviewProject).toHaveAttribute('aria-pressed', 'true');
    expect(generateIac).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(generateIac);

    expect(reviewProject).toHaveAttribute('aria-pressed', 'false');
    expect(generateIac).toHaveAttribute('aria-pressed', 'true');
  });

  it('persists the selected provider tab and task mode locally', () => {
    const firstRender = render(<ChatPanel {...baseProps} />);

    fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }));
    fireEvent.click(screen.getByRole('button', { name: 'Generate IaC' }));

    expect(window.localStorage.getItem('iac-studio.agentHub.activeTab')).toBe('gemini');
    expect(window.localStorage.getItem('iac-studio.agentHub.activeTask')).toBe('Generate IaC');

    firstRender.unmount();
    render(<ChatPanel {...baseProps} />);

    expect(screen.getByRole('tab', { name: 'Gemini' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Generate IaC' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('falls back to defaults when persisted provider selection is invalid', () => {
    window.localStorage.setItem('iac-studio.agentHub.activeTab', 'missing-provider');
    window.localStorage.setItem('iac-studio.agentHub.activeTask', 'Unsupported task');

    render(<ChatPanel {...baseProps} />);

    expect(screen.getByRole('tab', { name: 'Chat' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Review project' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps the UI usable when localStorage reads are blocked', () => {
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('localStorage read blocked');
    });

    try {
      render(<ChatPanel {...baseProps} />);

      expect(screen.getByRole('tab', { name: 'Chat' })).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByRole('button', { name: 'Review project' })).toHaveAttribute('aria-pressed', 'true');
    } finally {
      getItemSpy.mockRestore();
    }
  });

  it('keeps provider and task selection usable when localStorage writes are blocked', () => {
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('localStorage write blocked');
    });

    try {
      render(<ChatPanel {...baseProps} />);

      expect(() => fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }))).not.toThrow();
      expect(() => fireEvent.click(screen.getByRole('button', { name: 'Generate IaC' }))).not.toThrow();
      expect(screen.getByRole('tab', { name: 'Gemini' })).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByRole('button', { name: 'Generate IaC' })).toHaveAttribute('aria-pressed', 'true');
    } finally {
      setItemSpy.mockRestore();
    }
  });

  it('keeps local model support visible as a first-class lane', () => {
    render(<ChatPanel {...baseProps} providerLabel="Ollama" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Local' }));

    const localPanel = screen.getByRole('tabpanel', { name: 'Local' });
    expect(within(localPanel).getByRole('button', { name: /Ollama/ })).toBeInTheDocument();
    expect(within(localPanel).getByRole('button', { name: /LM Studio \/ vLLM/ })).toBeInTheDocument();
    expect(within(localPanel).getByText(/without cloud egress/)).toBeInTheDocument();
  });

  it('shows detected OpenAI-compatible local endpoint details', async () => {
    listLocalAgentProvidersMock.mockResolvedValueOnce([{
      id: 'openai-compatible-local',
      name: 'LM Studio / vLLM',
      category: 'local_model',
      state: 'available',
      installed: true,
      entrypoint: 'http://127.0.0.1:1234/v1',
      candidates: [],
      version: 'unknown',
      capabilities: ['chat', 'openai_compatible', 'local_model'],
      credential_mode: 'none',
      auth_hint: 'Uses a local OpenAI-compatible endpoint.',
    }]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Local' }));
    const localPanel = screen.getByRole('tabpanel', { name: 'Local' });

    await waitFor(() => {
      expect(within(localPanel).getByText('Detected: http://127.0.0.1:1234/v1')).toBeInTheDocument();
    });

    fireEvent.click(within(localPanel).getByRole('button', { name: /LM Studio \/ vLLM/ }));
    const details = within(localPanel).getByRole('region', { name: 'LM Studio / vLLM details' });
    expect(within(details).getByText('No credentials')).toBeInTheDocument();
    expect(within(details).getByText('http://127.0.0.1:1234/v1')).toBeInTheDocument();
    expect(within(details).getByText('openai compatible')).toBeInTheDocument();
    expect(within(details).getByRole('button', { name: 'Configure API' })).toBeDisabled();
  });

  it('shows Gemini and Copilot as first-class assistant lanes', () => {
    render(<ChatPanel {...baseProps} />);

    fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }));
    const geminiPanel = screen.getByRole('tabpanel', { name: 'Gemini' });
    expect(within(geminiPanel).getByRole('button', { name: /Gemini CLI/ })).toBeInTheDocument();
    expect(within(geminiPanel).getByRole('button', { name: /Gemini API/ })).toBeInTheDocument();
    expect(within(geminiPanel).getAllByText(/local Gemini session/).length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole('tab', { name: 'Copilot' }));
    const copilotPanel = screen.getByRole('tabpanel', { name: 'Copilot' });
    expect(within(copilotPanel).getByRole('button', { name: /GitHub Copilot CLI/ })).toBeInTheDocument();
    expect(within(copilotPanel).getByRole('button', { name: /Copilot coding agent/ })).toBeInTheDocument();
    expect(within(copilotPanel).getAllByText(/GitHub auth session/).length).toBeGreaterThan(0);
  });

  it('scrolls to the newest chat message when returning to the Chat tab', () => {
    const scrollAnchorRef = createRef<HTMLDivElement>();
    render(<ChatPanel {...baseProps} scrollAnchorRef={scrollAnchorRef} />);
    const scrollIntoView = vi.fn();
    Object.defineProperty(scrollAnchorRef.current, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    });

    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Chat' }));

    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
  });

  it('loads project-scoped run summaries in the Runs tab', async () => {
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'read_only',
      status: 'queued',
      prompt_preview: 'Review this project for unsafe Terraform changes',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      log_count: 2,
      patch_count: 1,
      approval_count: 1,
      pending_approval_count: 1,
      pending_gates: [{
        id: 'approval_000001',
        kind: 'command',
        summary: 'Run terraform plan after reviewing the patch',
        created_at: '2026-07-01T10:00:00Z',
      }],
    }]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByText('Review this project for unsafe Terraform changes')).toBeInTheDocument();
    });
    expect(listAgentRunsMock).toHaveBeenCalledWith('demo');
    expect(within(runsPanel).getByText('queued')).toBeInTheDocument();
    expect(within(runsPanel).getAllByText('read only').length).toBeGreaterThan(0);
    expect(within(runsPanel).getByText('Codex CLI')).toBeInTheDocument();
    expect(within(runsPanel).getByTitle('codex')).toBeInTheDocument();
    expect(within(runsPanel).getByText('2 logs')).toBeInTheDocument();
    expect(within(runsPanel).getByText('1 pending')).toBeInTheDocument();
    expect(within(runsPanel).getByText('Run terraform plan after reviewing the patch')).toBeInTheDocument();
    expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000001 for run_000001' })).toBeInTheDocument();
    expect(within(runsPanel).getByRole('button', { name: 'Reject approval_000001 for run_000001' })).toBeInTheDocument();
    expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
  });

  it('queues the current prompt as an audited run and refreshes the Runs tab', async () => {
    listAgentRunsMock
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'queued',
        prompt_preview: 'Apply the reviewed Terraform plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 0,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
    createAgentRunMock.mockResolvedValueOnce(agentRunFixture({
      id: 'run_000001',
      mode: 'propose_only',
      status: 'queued',
      prompt_preview: 'Apply the reviewed Terraform plan',
    }));

    render(<ChatPanel {...baseProps} projectName="demo" input="Apply the reviewed Terraform plan" />);
    fireEvent.click(screen.getByRole('button', { name: 'Prepare deploy' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeInTheDocument();
    });
    expect(within(runsPanel).getByText('Apply the reviewed Terraform plan')).toBeInTheDocument();
    expect(within(runsPanel).getByText('propose only')).toBeInTheDocument();
    expect(within(runsPanel).getByText('Ollama')).toBeInTheDocument();

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' }));

    await waitFor(() => {
      expect(createAgentRunMock).toHaveBeenCalledWith('demo', {
        prompt: 'Apply the reviewed Terraform plan',
        mode: 'propose_only',
        provider_id: 'ollama',
      });
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText('queued')).toBeInTheDocument();
    });
  });

  it('queues the selected Agent Hub provider id with audited runs', async () => {
    render(<ChatPanel {...baseProps} projectName="demo" input="Review this project with API context" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    fireEvent.click(within(codexPanel).getByRole('button', { name: /OpenAI API/ }));
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeEnabled();
    });
    expect(within(runsPanel).getByText('OpenAI API')).toBeInTheDocument();

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' }));

    await waitFor(() => {
      expect(createAgentRunMock).toHaveBeenCalledWith('demo', {
        prompt: 'Review this project with API context',
        mode: 'read_only',
        provider_id: 'codex-openai-api',
      });
    });
  });

  it('falls back to raw provider ids for unknown run providers', async () => {
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'custom-provider-bridge',
      mode: 'read_only',
      status: 'queued',
      prompt_preview: 'Review this project for custom policy checks',
      prompt_hash: 'sha256:xyz',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      log_count: 0,
      patch_count: 0,
      approval_count: 0,
      pending_approval_count: 0,
    }]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByText('Review this project for custom policy checks')).toBeInTheDocument();
    });
    expect(within(runsPanel).getByText('custom-provider-bridge')).toBeInTheDocument();
    expect(within(runsPanel).getByTitle('custom-provider-bridge')).toBeInTheDocument();
  });

  it('ignores stale run-list responses after queue refresh wins', async () => {
    let resolveInitialRuns: (runs: unknown[]) => void = () => {};
    let resolveRefreshRuns: (runs: unknown[]) => void = () => {};
    const initialList = new Promise<unknown[]>(resolve => {
      resolveInitialRuns = resolve;
    });
    const refreshList = new Promise<unknown[]>(resolve => {
      resolveRefreshRuns = resolve;
    });
    listAgentRunsMock
      .mockReturnValueOnce(initialList)
      .mockReturnValueOnce(refreshList);
    createAgentRunMock.mockResolvedValueOnce(agentRunFixture({
      id: 'run_000001',
      mode: 'read_only',
      status: 'queued',
      prompt_preview: 'Review the current plan',
    }));

    render(<ChatPanel {...baseProps} projectName="demo" input="Review the current plan" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeEnabled();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' }));

    await waitFor(() => {
      expect(createAgentRunMock).toHaveBeenCalledWith('demo', {
        prompt: 'Review the current plan',
        mode: 'read_only',
        provider_id: 'ollama',
      });
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
    });

    await act(async () => {
      resolveRefreshRuns([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'read_only',
        status: 'queued',
        prompt_preview: 'Review the current plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 0,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
      await refreshList;
    });

    await waitFor(() => {
      expect(within(runsPanel).getAllByText('Review the current plan').length).toBeGreaterThan(0);
      expect(within(runsPanel).getByText('queued')).toBeInTheDocument();
    });

    await act(async () => {
      resolveInitialRuns([{
        id: 'run_stale',
        project: 'demo',
        provider_id: 'codex',
        mode: 'read_only',
        status: 'completed',
        prompt_preview: 'Stale pre-queue run',
        prompt_hash: 'sha256:stale',
        created_at: '2026-07-01T09:00:00Z',
        updated_at: '2026-07-01T09:01:00Z',
        canceled: false,
        log_count: 0,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
      await initialList;
    });

    expect(within(runsPanel).getAllByText('Review the current plan').length).toBeGreaterThan(0);
    expect(within(runsPanel).queryByText('Stale pre-queue run')).not.toBeInTheDocument();
  });

  it('keeps the run queue action available while run summaries load', async () => {
    listAgentRunsMock.mockReturnValueOnce(new Promise(() => {}));

    render(<ChatPanel {...baseProps} projectName="demo" input="Review the current plan" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByText('Loading agent runs...')).toBeInTheDocument();
    });
    expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeEnabled();
  });

  it('keeps the run queue action available when run summaries fail to load', async () => {
    listAgentRunsMock.mockRejectedValueOnce(new Error('backend offline'));

    render(<ChatPanel {...baseProps} projectName="demo" input="Review the current plan" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByText('Could not load agent runs.')).toBeInTheDocument();
    });
    expect(within(runsPanel).getByText('backend offline')).toBeInTheDocument();
    expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeEnabled();
  });

  it('refreshes run summaries when queueing hits a conflict', async () => {
    const conflictError = new Error('agent run changed before queueing');
    Object.assign(conflictError, { status: 409 });
    listAgentRunsMock
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'read_only',
        status: 'running',
        prompt_preview: 'Review the current plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 0,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
    createAgentRunMock.mockRejectedValueOnce(conflictError);

    render(<ChatPanel {...baseProps} projectName="demo" input="Review the current plan" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' })).toBeEnabled();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Queue current prompt as agent run' }));

    await waitFor(() => {
      expect(createAgentRunMock).toHaveBeenCalledWith('demo', {
        prompt: 'Review the current plan',
        mode: 'read_only',
        provider_id: 'ollama',
      });
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText('running')).toBeInTheDocument();
    });
    expect(within(runsPanel).queryByText('agent run changed before queueing')).not.toBeInTheDocument();
  });

  it('auto-refreshes active run summaries while the Runs tab is open', async () => {
    vi.useFakeTimers();
    listAgentRunsMock
      .mockResolvedValueOnce([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'running',
        prompt_preview: 'Watch the live run',
      })])
      .mockResolvedValueOnce([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'completed',
        prompt_preview: 'Watch the live run',
        updated_at: '2026-07-01T10:00:05Z',
        completed_at: '2026-07-01T10:00:05Z',
        log_count: 1,
      })])
      .mockResolvedValue([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'completed',
        prompt_preview: 'Watch the live run',
        updated_at: '2026-07-01T10:00:05Z',
        completed_at: '2026-07-01T10:00:05Z',
        log_count: 1,
      })]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await flushAsyncUpdates();
    expect(within(runsPanel).getByText('running')).toBeInTheDocument();
    expect(listAgentRunsMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(within(runsPanel).getByText('completed')).toBeInTheDocument();
    expect(listAgentRunsMock).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
  });

  it('auto-refreshes open active run details', async () => {
    vi.useFakeTimers();
    listAgentRunsMock
      .mockResolvedValueOnce([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'running',
        prompt_preview: 'Watch detail logs',
      })])
      .mockResolvedValue([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'completed',
        prompt_preview: 'Watch detail logs',
        updated_at: '2026-07-01T10:00:05Z',
        completed_at: '2026-07-01T10:00:05Z',
        log_count: 1,
      })]);
    getAgentRunMock
      .mockResolvedValueOnce(agentRunFixture({
        id: 'run_000001',
        status: 'running',
        prompt_preview: 'Watch detail logs',
      }))
      .mockResolvedValueOnce(agentRunFixture({
        id: 'run_000001',
        status: 'completed',
        prompt_preview: 'Watch detail logs',
        log_count: 1,
        logs: [{
          id: 'log_000001',
          at: '2026-07-01T10:00:05Z',
          level: 'info',
          message: 'Finished live run',
        }],
      }));

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await flushAsyncUpdates();
    expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000001' }));

    await flushAsyncUpdates();
    expect(within(runsPanel).getByRole('region', { name: 'run_000001 details' })).toBeInTheDocument();
    expect(getAgentRunMock).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(within(runsPanel).getByText('Finished live run')).toBeInTheDocument();
    expect(getAgentRunMock).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(getAgentRunMock).toHaveBeenCalledTimes(2);
  });

  it('does not block switching run details while a detail poll is in flight', async () => {
    vi.useFakeTimers();

    // Two active runs so both detail buttons are visible
    listAgentRunsMock.mockResolvedValue([
      agentRunSummaryFixture({ id: 'run_000001', status: 'running', prompt_preview: 'Run one' }),
      agentRunSummaryFixture({ id: 'run_000002', status: 'running', prompt_preview: 'Run two' }),
    ]);

    // First user-initiated detail fetch for run_000001 (running)
    // Then poll fires for run_000001
    // Then user switches to run_000002
    let resolveFirstPoll!: (_value: AgentRun) => void;
    const firstPollPromise = new Promise<AgentRun>(res => { resolveFirstPoll = res; });
    getAgentRunMock
      .mockResolvedValueOnce(agentRunFixture({ id: 'run_000001', status: 'running' }))
      .mockReturnValueOnce(firstPollPromise) // stalled poll for run_000001
      .mockResolvedValue(agentRunFixture({ id: 'run_000002', status: 'running' }));

    render(<ChatPanel {...baseProps} projectName="demo" input="new task" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await flushAsyncUpdates();

    // Open details for run_000001
    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000001' }));
    await flushAsyncUpdates();
    expect(within(runsPanel).getByRole('region', { name: 'run_000001 details' })).toBeInTheDocument();

    // Advance timer so the detail poll fires (firstPollPromise stalls in flight)
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(getAgentRunMock).toHaveBeenCalledTimes(2); // 1 user-init + 1 poll

    // While poll is stalled, switching to run_000002 must succeed immediately
    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000002' }));
    await flushAsyncUpdates();
    expect(within(runsPanel).getByRole('region', { name: 'run_000002 details' })).toBeInTheDocument();

    // Resolve the stalled poll — its response must be silently discarded
    await act(async () => { resolveFirstPoll(agentRunFixture({ id: 'run_000001', status: 'running' })); });
    expect(within(runsPanel).getByRole('region', { name: 'run_000002 details' })).toBeInTheDocument();
  });

  it('loads one run detail record with logs, patches, and approvals', async () => {
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'propose_only',
      status: 'completed',
      prompt_preview: 'Review and propose a Terraform fix',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:02:00Z',
      completed_at: '2026-07-01T10:02:00Z',
      canceled: false,
      log_count: 1,
      patch_count: 1,
      approval_count: 1,
      pending_approval_count: 0,
    }]);
    getAgentRunMock.mockResolvedValueOnce({
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'propose_only',
      status: 'completed',
      prompt_preview: 'Review and propose a Terraform fix',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:02:00Z',
      completed_at: '2026-07-01T10:02:00Z',
      canceled: false,
      log_count: 1,
      patch_count: 1,
      approval_count: 1,
      pending_approval_count: 0,
      logs: [{
        id: 'log_000001',
        at: '2026-07-01T10:00:01Z',
        level: 'audit',
        message: 'Started read-only project review',
      }],
      patches: [{
        id: 'patch_000001',
        path: 'main.tf',
        summary: 'Restrict S3 bucket ACL',
        diff: '- acl = "public-read"\n+ acl = "private"',
        created_at: '2026-07-01T10:01:00Z',
      }],
      approvals: [{
        id: 'approval_000001',
        kind: 'file_write',
        status: 'approved',
        summary: 'Allow writing the proposed Terraform patch',
        created_at: '2026-07-01T10:01:30Z',
        decided_at: '2026-07-01T10:01:45Z',
        decided_by: 'operator',
      }],
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000001' }));

    await waitFor(() => {
      expect(getAgentRunMock).toHaveBeenCalledWith('demo', 'run_000001');
      expect(within(runsPanel).getByRole('region', { name: 'run_000001 details' })).toBeInTheDocument();
    });

    const details = within(runsPanel).getByRole('region', { name: 'run_000001 details' });
    expect(within(details).getByText('Codex CLI')).toBeInTheDocument();
    expect(within(details).getByText('Started read-only project review')).toBeInTheDocument();
    expect(within(details).getByText('Restrict S3 bucket ACL')).toBeInTheDocument();
    expect(within(details).getByText(/public-read/)).toBeInTheDocument();
    expect(within(details).getByText('Allow writing the proposed Terraform patch')).toBeInTheDocument();
    expect(within(details).getByText('approved')).toBeInTheDocument();

    fireEvent.click(within(details).getByRole('button', { name: 'Close details for run_000001' }));
    expect(within(runsPanel).queryByRole('region', { name: 'run_000001 details' })).not.toBeInTheDocument();
  });

  it('shows empty-state messages for run details without logs, patches, or approvals', async () => {
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'read_only',
      status: 'completed',
      prompt_preview: 'Read-only review completed',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:01:00Z',
      completed_at: '2026-07-01T10:01:00Z',
      canceled: false,
      log_count: 0,
      patch_count: 0,
      approval_count: 0,
      pending_approval_count: 0,
    }]);
    getAgentRunMock.mockResolvedValueOnce(agentRunFixture({
      id: 'run_000001',
      prompt_preview: 'Read-only review completed',
    }));

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000001' }));

    await waitFor(() => {
      expect(within(runsPanel).getByRole('region', { name: 'run_000001 details' })).toBeInTheDocument();
    });
    const details = within(runsPanel).getByRole('region', { name: 'run_000001 details' });
    expect(within(details).getByText('No run logs yet.')).toBeInTheDocument();
    expect(within(details).getByText('No proposed patches.')).toBeInTheDocument();
    expect(within(details).getByText('No approval history.')).toBeInTheDocument();
  });

  it('guards duplicate run detail requests before loading state rerenders', async () => {
    let resolveDetails!: (value: AgentRun) => void;
    getAgentRunMock.mockReturnValueOnce(
      new Promise(resolve => { resolveDetails = resolve; }),
    );
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'read_only',
      status: 'completed',
      prompt_preview: 'Review project',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      log_count: 0,
      patch_count: 0,
      approval_count: 0,
      pending_approval_count: 0,
    }]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
    });

    const detailsButton = within(runsPanel).getByRole('button', { name: 'View details for run_000001' });
    fireEvent.click(detailsButton);
    fireEvent.click(detailsButton);

    expect(getAgentRunMock).toHaveBeenCalledTimes(1);

    act(() => resolveDetails(agentRunFixture({ id: 'run_000001', prompt_preview: 'Review project' })));

    await waitFor(() => {
      expect(within(runsPanel).getByRole('region', { name: 'run_000001 details' })).toBeInTheDocument();
    });
  });

  it.each([
    { action: 'Approve', decision: 'approved', finalStatus: 'running' },
    { action: 'Reject', decision: 'rejected', finalStatus: 'failed' },
  ] as const)('decides pending approval gates with $action and refreshes the Runs tab', async ({ action, decision, finalStatus }) => {
    listAgentRunsMock
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'approved_execute',
        status: 'waiting_approval',
        prompt_preview: 'Apply a reviewed Terraform plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 1,
        approval_count: 1,
        pending_approval_count: 1,
        pending_gates: [{
          id: 'approval_000001',
          kind: 'iac_action',
          summary: 'Apply Terraform changes',
          created_at: '2026-07-01T10:00:00Z',
        }],
      }])
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'approved_execute',
        status: finalStatus,
        prompt_preview: 'Apply a reviewed Terraform plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:01:00Z',
        canceled: false,
        log_count: 3,
        patch_count: 1,
        approval_count: 1,
        pending_approval_count: 0,
      }]);
    decideAgentRunApprovalMock.mockResolvedValueOnce({
      id: 'run_000001',
      project: 'demo',
      status: finalStatus,
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: `${action} approval_000001 for run_000001` })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: `${action} approval_000001 for run_000001` }));

    await waitFor(() => {
      expect(decideAgentRunApprovalMock).toHaveBeenCalledWith('demo', 'run_000001', 'approval_000001', decision);
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText(finalStatus)).toBeInTheDocument();
    });
    expect(within(runsPanel).queryByRole('button', { name: 'Approve approval_000001 for run_000001' })).not.toBeInTheDocument();
    expect(within(runsPanel).queryByRole('button', { name: 'Reject approval_000001 for run_000001' })).not.toBeInTheDocument();
    expect(within(runsPanel).queryByText('Apply Terraform changes')).not.toBeInTheDocument();
  });

  it('disables all gate action buttons while an approval decision is in-flight', async () => {
    let resolveDecision!: (value: unknown) => void;
    decideAgentRunApprovalMock.mockReturnValueOnce(
      new Promise(resolve => { resolveDecision = resolve; }),
    );
    listAgentRunsMock.mockResolvedValueOnce([{
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'approved_execute',
      status: 'waiting_approval',
      prompt_preview: 'Waiting for approval',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      log_count: 0,
      patch_count: 0,
      approval_count: 1,
      pending_approval_count: 2,
      pending_gates: [
        { id: 'approval_000001', kind: 'command', summary: 'First gate', created_at: '2026-07-01T10:00:00Z' },
        { id: 'approval_000002', kind: 'iac_action', summary: 'Second gate', created_at: '2026-07-01T10:00:01Z' },
      ],
    }]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000001 for run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Approve approval_000001 for run_000001' }));

    // While the decision is in-flight all four gate buttons must be disabled
    expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000001 for run_000001' })).toBeDisabled();
    expect(within(runsPanel).getByRole('button', { name: 'Reject approval_000001 for run_000001' })).toBeDisabled();
    expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000002 for run_000001' })).toBeDisabled();
    expect(within(runsPanel).getByRole('button', { name: 'Reject approval_000002 for run_000001' })).toBeDisabled();

    // The deciding gate's buttons carry aria-busy; the other gate's do not
    expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000001 for run_000001' })).toHaveAttribute('aria-busy', 'true');
    expect(within(runsPanel).getByRole('button', { name: 'Reject approval_000001 for run_000001' })).toHaveAttribute('aria-busy', 'true');
    expect(within(runsPanel).getByRole('button', { name: 'Approve approval_000002 for run_000001' })).toHaveAttribute('aria-busy', 'false');
    expect(within(runsPanel).getByRole('button', { name: 'Reject approval_000002 for run_000001' })).toHaveAttribute('aria-busy', 'false');

    // Resolve the in-flight request so the component can settle
    listAgentRunsMock.mockResolvedValueOnce([]);
    act(() => resolveDecision({}));
    await waitFor(() => expect(decideAgentRunApprovalMock).toHaveBeenCalledTimes(1));
  });

  it('cancels non-terminal runs and refreshes the Runs tab', async () => {
    listAgentRunsMock
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'running',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }])
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'canceled',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:01:00Z',
        completed_at: '2026-07-01T10:01:00Z',
        canceled: true,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
    cancelAgentRunMock.mockResolvedValueOnce({
      id: 'run_000001',
      project: 'demo',
      status: 'canceled',
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' }));

    await waitFor(() => {
      expect(cancelAgentRunMock).toHaveBeenCalledWith('demo', 'run_000001');
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText('canceled')).toBeInTheDocument();
    });
    expect(within(runsPanel).queryByRole('button', { name: 'Cancel run_000001' })).not.toBeInTheDocument();
  });

  it('refreshes run summaries when cancel finds the run already terminal', async () => {
    const conflictError = new Error('agent run is already in a terminal state');
    Object.assign(conflictError, { status: 409 });
    listAgentRunsMock
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'running',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }])
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'completed',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:01:00Z',
        completed_at: '2026-07-01T10:01:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
    cancelAgentRunMock.mockRejectedValueOnce(conflictError);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' }));

    await waitFor(() => {
      expect(cancelAgentRunMock).toHaveBeenCalledWith('demo', 'run_000001');
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText('completed')).toBeInTheDocument();
    });
    expect(within(runsPanel).queryByRole('button', { name: 'Cancel run_000001' })).not.toBeInTheDocument();
    expect(within(runsPanel).queryByText('Could not load agent runs.')).not.toBeInTheDocument();
  });

  it('reports refresh failures separately after a successful cancel', async () => {
    listAgentRunsMock
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'running',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }])
      .mockRejectedValueOnce(new Error('network unavailable'));
    cancelAgentRunMock.mockResolvedValueOnce({
      id: 'run_000001',
      project: 'demo',
      status: 'canceled',
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' }));

    await waitFor(() => {
      expect(cancelAgentRunMock).toHaveBeenCalledWith('demo', 'run_000001');
      expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
      expect(within(runsPanel).getByText('agent run refresh failed: network unavailable')).toBeInTheDocument();
    });
    expect(within(runsPanel).queryByText('agent run cancel failed')).not.toBeInTheDocument();
  });

  it('does not overwrite run summaries after switching projects during cancel', async () => {
    let resolveCancel: (_value: { id: string; project: string; status: string }) => void = () => {};
    const cancelPromise = new Promise(resolve => {
      resolveCancel = resolve;
    });
    listAgentRunsMock
      .mockResolvedValueOnce([{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'propose_only',
        status: 'running',
        prompt_preview: 'Prepare a safe deployment plan',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 2,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }])
      .mockResolvedValueOnce([{
        id: 'run_000002',
        project: 'other',
        provider_id: 'codex',
        mode: 'read_only',
        status: 'queued',
        prompt_preview: 'Other project queued run',
        prompt_hash: 'sha256:def',
        created_at: '2026-07-01T10:02:00Z',
        updated_at: '2026-07-01T10:02:00Z',
        canceled: false,
        log_count: 1,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 0,
      }]);
    cancelAgentRunMock.mockReturnValueOnce(cancelPromise);

    const { rerender } = render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' })).toBeInTheDocument();
    });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'Cancel run_000001' }));
    rerender(<ChatPanel {...baseProps} projectName="other" />);

    await waitFor(() => {
      expect(listAgentRunsMock).toHaveBeenCalledWith('other');
      expect(within(runsPanel).getByText('Other project queued run')).toBeInTheDocument();
    });

    await act(async () => {
      resolveCancel({ id: 'run_000001', project: 'demo', status: 'canceled' });
    });

    expect(listAgentRunsMock).toHaveBeenCalledTimes(2);
    expect(within(runsPanel).getByText('Other project queued run')).toBeInTheDocument();
    expect(within(runsPanel).queryByText('Prepare a safe deployment plan')).not.toBeInTheDocument();
  });
});
