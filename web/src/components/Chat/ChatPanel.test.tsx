import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, fireEvent, waitFor, within } from '@testing-library/react';

import type { AgentRun, AgentRunSummary, AgentToolPolicyResponse } from '../../api';
import { ChatPanel } from './ChatPanel';

const listLocalAgentProvidersMock = vi.hoisted(() => vi.fn());
const listAgentProviderConnectionsMock = vi.hoisted(() => vi.fn());
const listAgentProviderConnectionProfilesMock = vi.hoisted(() => vi.fn());
const getAgentToolPolicyMock = vi.hoisted(() => vi.fn());
const saveAgentToolPolicyMock = vi.hoisted(() => vi.fn());
const listAgentRunsMock = vi.hoisted(() => vi.fn());
const createAgentRunMock = vi.hoisted(() => vi.fn());
const getAgentRunMock = vi.hoisted(() => vi.fn());
const cancelAgentRunMock = vi.hoisted(() => vi.fn());
const decideAgentRunApprovalMock = vi.hoisted(() => vi.fn());
const previewAgentToolRouteMock = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  api: {
    listLocalAgentProviders: listLocalAgentProvidersMock,
    listAgentProviderConnections: listAgentProviderConnectionsMock,
    listAgentProviderConnectionProfiles: listAgentProviderConnectionProfilesMock,
    getAgentToolPolicy: getAgentToolPolicyMock,
    saveAgentToolPolicy: saveAgentToolPolicyMock,
    listAgentRuns: listAgentRunsMock,
    createAgentRun: createAgentRunMock,
    getAgentRun: getAgentRunMock,
    cancelAgentRun: cancelAgentRunMock,
    decideAgentRunApproval: decideAgentRunApprovalMock,
    previewAgentToolRoute: previewAgentToolRouteMock,
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
    listAgentProviderConnectionsMock.mockReset();
    listAgentProviderConnectionsMock.mockReturnValue(new Promise(() => {}));
    listAgentProviderConnectionProfilesMock.mockReset();
    listAgentProviderConnectionProfilesMock.mockReturnValue(new Promise(() => {}));
    getAgentToolPolicyMock.mockReset();
    getAgentToolPolicyMock.mockReturnValue(new Promise(() => {}));
    saveAgentToolPolicyMock.mockReset();
    saveAgentToolPolicyMock.mockReturnValue(new Promise(() => {}));
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
    previewAgentToolRouteMock.mockReset();
    previewAgentToolRouteMock.mockResolvedValue({
      decision: {
        status: 'denied',
        reason: 'airlock_blocked',
        allowed: false,
        approval_required: false,
        untrusted_output: true,
      },
    });
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

  it('shows API and enterprise provider connection catalog metadata without secret values', async () => {
    listAgentProviderConnectionsMock.mockResolvedValueOnce([
      {
        id: 'openai-api',
        name: 'OpenAI API',
        family: 'openai',
        category: 'api',
        credential_mode: 'secret_store',
        required_fields: ['model'],
        secret_fields: ['api_key'],
        capabilities: ['chat', 'code_editing', 'iac_assistance', 'tool_calling', 'vision'],
        cost_controls: ['monthly_budget', 'per_run_token_limit', 'allowed_models', 'hard_stop'],
        billing_hint: 'Billed through the OpenAI Platform API account, separate from ChatGPT subscriptions.',
        data_handling_hint: 'Prompts and selected project context are sent to the configured OpenAI API endpoint.',
        secret_storage_hint: 'Store API keys through IaC Studio secret stores; keys are never returned to the browser after save.',
        setup_hint: 'Use for automation, hosted workflows, or centrally billed platform usage.',
      },
      {
        id: 'enterprise-gateway',
        name: 'Enterprise Gateway',
        family: 'gateway',
        category: 'enterprise_gateway',
        credential_mode: 'enterprise_sso',
        required_fields: ['endpoint', 'tenant'],
        secret_fields: [],
        capabilities: ['chat', 'code_editing', 'iac_assistance', 'tool_calling', 'audit_controls', 'policy_routing'],
        cost_controls: ['workspace_budget', 'allowed_models', 'team_quota'],
        billing_hint: "Billed through the organization's gateway or enterprise model platform.",
        data_handling_hint: 'Prompts and selected project context follow the configured enterprise gateway routing policy.',
        secret_storage_hint: 'Use SSO or gateway-managed credentials; IaC Studio should not collect individual API keys for this path.',
        setup_hint: 'Use for private routing, SSO, audit, and platform-team rollouts.',
      },
      {
        id: 'minimal-azure-openai',
        name: 'Minimal Azure OpenAI',
        family: 'azure_openai',
        category: 'api',
        credential_mode: 'secret_store',
        required_fields: ['model'],
        secret_fields: [],
        capabilities: [],
        cost_controls: [],
        billing_hint: 'Billed through the configured platform account.',
        data_handling_hint: 'Prompts follow the configured provider route.',
        secret_storage_hint: 'Credentials stay in a secret store.',
        setup_hint: 'Use for minimal API wiring.',
      },
    ]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await waitFor(() => {
      expect(within(codexPanel).getByRole('region', { name: 'Codex API and enterprise connection catalog' })).toBeInTheDocument();
    });
    const catalog = within(codexPanel).getByRole('region', { name: 'Codex API and enterprise connection catalog' });
    const openAiConnection = within(catalog).getByLabelText('OpenAI API connection');
    expect(openAiConnection).toBeInTheDocument();
    expect(within(catalog).getByText(/separate from ChatGPT subscriptions/)).toBeInTheDocument();
    expect(within(catalog).getByText(/configured OpenAI API endpoint/)).toBeInTheDocument();
    expect(within(catalog).getByText(/keys are never returned to the browser after save/)).toBeInTheDocument();
    expect(within(catalog).getAllByText('Secret store').length).toBeGreaterThan(0);
    expect(within(catalog).getByText('azure openai')).toBeInTheDocument();
    expect(within(openAiConnection).getByText('monthly budget')).toBeInTheDocument();
    expect(within(openAiConnection).getByText('per run token limit')).toBeInTheDocument();
    expect(within(openAiConnection).getByText('allowed models')).toBeInTheDocument();
    expect(within(openAiConnection).getByText('hard stop')).toBeInTheDocument();
    expect(within(openAiConnection).getByText('vision')).toBeInTheDocument();
    expect(within(catalog).getByText('Enterprise SSO')).toBeInTheDocument();
    expect(within(catalog).getByLabelText('Minimal Azure OpenAI connection')).toBeInTheDocument();
    expect(within(catalog).queryByLabelText('Minimal Azure OpenAI capabilities')).not.toBeInTheDocument();
    expect(within(catalog).queryByLabelText('Minimal Azure OpenAI cost controls')).not.toBeInTheDocument();
    expect(within(catalog).queryByText('sk-test-secret')).not.toBeInTheDocument();
  });

  it('shows redacted saved provider profiles for the matching provider lane', async () => {
    listAgentProviderConnectionsMock.mockResolvedValueOnce([
      {
        id: 'openai-api',
        name: 'OpenAI API',
        family: 'openai',
        category: 'api',
        credential_mode: 'secret_store',
        required_fields: ['model'],
        secret_fields: ['api_key'],
        capabilities: ['chat'],
        cost_controls: ['monthly_budget'],
        billing_hint: 'Billed through the OpenAI Platform API account.',
        data_handling_hint: 'Prompts are sent to the configured OpenAI API endpoint.',
        secret_storage_hint: 'Keys stay in a secret store.',
        setup_hint: 'Use for automation.',
      },
    ]);
    listAgentProviderConnectionProfilesMock.mockResolvedValueOnce([
      {
        id: 'agent_provider_connection_000001',
        name: 'OpenAI prod automation',
        provider_id: 'openai-api',
        credential_mode: 'secret_store',
        metadata: { model: 'gpt-5' },
        cost_controls: { monthly_budget: '100' },
        secret_fields: ['api_key'],
        secret_store: 'local_encrypted',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
      },
      {
        id: 'agent_provider_connection_000002',
        name: 'Anthropic team automation',
        provider_id: 'anthropic-api',
        credential_mode: 'secret_store',
        secret_fields: ['api_key'],
        secret_store: 'local_encrypted',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
      },
    ]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await waitFor(() => {
      expect(within(codexPanel).getByRole('region', { name: 'Codex saved provider connections' })).toBeInTheDocument();
    });
    const savedProfiles = within(codexPanel).getByRole('region', { name: 'Codex saved provider connections' });
    const openAiProfile = within(savedProfiles).getByLabelText('OpenAI prod automation saved provider connection');
    expect(openAiProfile).toBeInTheDocument();
    expect(within(openAiProfile).getByText('local_encrypted')).toBeInTheDocument();
    expect(within(openAiProfile).getByText('1 secret field')).toBeInTheDocument();
    expect(within(openAiProfile).getByText('model')).toBeInTheDocument();
    expect(within(openAiProfile).getByText('monthly budget')).toBeInTheDocument();
    expect(within(savedProfiles).getByText(/Secret values and external refs are never shown/)).toBeInTheDocument();
    expect(within(savedProfiles).queryByText('Anthropic team automation')).not.toBeInTheDocument();
    expect(within(savedProfiles).queryByText('sk-test-secret')).not.toBeInTheDocument();
  });

  it('keeps provider panels usable when the connection catalog cannot load', async () => {
    listAgentProviderConnectionsMock.mockRejectedValueOnce(new Error('catalog unavailable'));
    listAgentProviderConnectionProfilesMock.mockRejectedValueOnce(new Error('profiles unavailable'));

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await flushAsyncUpdates();

    expect(within(codexPanel).getByRole('button', { name: /Codex CLI/ })).toBeInTheDocument();
    expect(within(codexPanel).queryByRole('region', { name: 'Codex API and enterprise connection catalog' })).not.toBeInTheDocument();
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

  it('shows exact scoped tool permissions for the selected provider', async () => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: {
        rules: [
          {
            project: 'demo',
            provider_id: 'codex',
            connection_id: 'aws-prod',
            server_id: 'aws-official',
            tool_name: 'list_resources',
            modes: ['read_only'],
            risk: 'read_only',
            effect: 'allow',
          },
          {
            project: 'demo',
            provider_id: 'codex',
            connection_id: 'aws-prod',
            server_id: 'aws-official',
            tool_name: 'apply_change',
            modes: ['approved_execute'],
            risk: 'cloud_mutation',
            effect: 'deny',
          },
        ],
      },
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    await waitFor(() => {
      expect(getAgentToolPolicyMock).toHaveBeenCalledWith('demo', 'codex');
    });

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    expect(within(permissions).getByText('1 allowed')).toBeInTheDocument();
    expect(within(permissions).getByText('1 denied')).toBeInTheDocument();
    expect(within(permissions).getByText('aws-official / list_resources')).toBeInTheDocument();
    expect(within(permissions).getByText('aws-official / apply_change')).toBeInTheDocument();
    expect(within(permissions).queryByRole('status')).not.toBeInTheDocument();

    fireEvent.click(within(codexPanel).getByRole('button', { name: /OpenAI API/ }));
    await waitFor(() => {
      expect(getAgentToolPolicyMock).toHaveBeenLastCalledWith('demo', 'codex-openai-api');
    });
    const nextPermissions = within(codexPanel).getByRole('region', { name: 'OpenAI API tool permissions' });
    expect(within(nextPermissions).getByText('Checking scoped tool permissions...')).toBeInTheDocument();
    expect(within(nextPermissions).queryByText('aws-official / list_resources')).not.toBeInTheDocument();
  });

  it('saves an edited policy only to the selected exact scope', async () => {
    const savedPolicy = {
      rules: [{
        project: 'demo',
        provider_id: 'codex',
        connection_id: 'aws-prod',
        server_id: 'aws-official',
        tool_name: 'list_resources',
        modes: ['read_only'],
        risk: 'read_only',
        effect: 'allow',
      }],
    };
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: { rules: [] },
    });
    saveAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: savedPolicy,
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('No routes allowed. MCP tool access is blocked.')).toBeInTheDocument();
    });

    fireEvent.click(within(permissions).getByRole('button', { name: 'Edit Codex CLI tool policy' }));
    fireEvent.change(within(permissions).getByRole('textbox', { name: 'Codex CLI policy JSON' }), {
      target: { value: JSON.stringify(savedPolicy, null, 2) },
    });
    fireEvent.click(within(permissions).getByRole('button', { name: 'Save policy' }));

    await waitFor(() => {
      expect(saveAgentToolPolicyMock).toHaveBeenCalledWith('demo', 'codex', savedPolicy);
      expect(within(permissions).getByText('aws-official / list_resources')).toBeInTheDocument();
    });
    expect(within(permissions).queryByRole('textbox', { name: 'Codex CLI policy JSON' })).not.toBeInTheDocument();
  });

  it('rejects malformed, unknown-field, and cross-scope policy edits before sending them', async () => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: { rules: [] },
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByRole('button', { name: 'Edit Codex CLI tool policy' })).toBeInTheDocument();
    });
    fireEvent.click(within(permissions).getByRole('button', { name: 'Edit Codex CLI tool policy' }));
    const editor = within(permissions).getByRole('textbox', { name: 'Codex CLI policy JSON' });
    fireEvent.change(editor, { target: { value: '{' } });
    fireEvent.click(within(permissions).getByRole('button', { name: 'Save policy' }));
    expect(within(permissions).getByRole('alert')).toHaveTextContent('Policy must be valid JSON.');
    expect(saveAgentToolPolicyMock).not.toHaveBeenCalled();

    const validRule = {
      project: 'demo',
      provider_id: 'codex',
      connection_id: 'aws-prod',
      server_id: 'aws-official',
      tool_name: 'list_resources',
      modes: ['read_only'],
      risk: 'read_only',
      effect: 'allow',
    };
    const invalidPolicies = [
      { rules: [{ ...validRule, project: 'other' }] },
      { rules: [], extra: true },
      { rules: [{ ...validRule, extra: true }] },
    ];
    for (const policy of invalidPolicies) {
      fireEvent.change(editor, { target: { value: JSON.stringify(policy) } });
      fireEvent.click(within(permissions).getByRole('button', { name: 'Save policy' }));

      expect(within(permissions).getByRole('alert')).toHaveTextContent(
        'Policy contains invalid fields or rules for this project and provider.',
      );
    }
    expect(saveAgentToolPolicyMock).not.toHaveBeenCalled();
  });

  it('keeps the existing policy and sanitizes save failures', async () => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: { rules: [] },
    });
    saveAgentToolPolicyMock.mockRejectedValueOnce(new Error('token=policy-secret'));

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByRole('button', { name: 'Edit Codex CLI tool policy' })).toBeInTheDocument();
    });
    fireEvent.click(within(permissions).getByRole('button', { name: 'Edit Codex CLI tool policy' }));
    fireEvent.click(within(permissions).getByRole('button', { name: 'Save policy' }));

    await waitFor(() => {
      expect(within(permissions).getByRole('alert')).toHaveTextContent(
        'Policy save could not be confirmed. Reload before retrying.',
      );
    });
    expect(within(permissions).getByText('No routes allowed. MCP tool access is blocked.')).toBeInTheDocument();
    expect(within(permissions).queryByText(/policy-secret/)).not.toBeInTheDocument();
  });

  it('shows missing policies as blocked without exposing request details', async () => {
    getAgentToolPolicyMock.mockRejectedValueOnce({ status: 404, message: 'sensitive backend detail' });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('No scoped policy. MCP tool access is blocked.')).toBeInTheDocument();
    });
    expect(within(permissions).queryByText(/sensitive backend detail/)).not.toBeInTheDocument();
  });

  it('shows server errors as policy unavailable without exposing error details', async () => {
    getAgentToolPolicyMock.mockRejectedValueOnce({ status: 503, message: 'gateway timeout' });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('Policy unavailable. MCP tool access remains blocked.')).toBeInTheDocument();
    });
    expect(within(permissions).queryByText(/gateway timeout/)).not.toBeInTheDocument();
  });

  it('blocks access when the policy response scope does not match the request', async () => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'other-project', provider_id: 'codex' },
      policy: {
        rules: [{
          project: 'other-project',
          provider_id: 'codex',
          connection_id: 'c1',
          server_id: 'srv',
          tool_name: 'injected_tool',
          modes: ['read_only'],
          risk: 'read_only',
          effect: 'allow',
        }],
      },
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('Policy unavailable. MCP tool access remains blocked.')).toBeInTheDocument();
    });
    expect(within(permissions).queryByText('injected_tool')).not.toBeInTheDocument();
  });

  it.each([
    ['modes', { modes: null }],
    ['risk', { risk: null }],
  ])('blocks access when a policy rule has malformed %s', async (_field, override) => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: {
        rules: [{
          project: 'demo',
          provider_id: 'codex',
          connection_id: 'c1',
          server_id: 'srv',
          tool_name: 'malformed_tool',
          modes: ['read_only'],
          risk: 'read_only',
          effect: 'allow',
          ...override,
        }],
      },
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('Policy unavailable. MCP tool access remains blocked.')).toBeInTheDocument();
    });
    expect(within(permissions).queryByText('malformed_tool')).not.toBeInTheDocument();
  });

  it('blocks access when the scoped policy has no allowed routes', async () => {
    getAgentToolPolicyMock.mockResolvedValueOnce({
      scope: { project: 'demo', provider_id: 'codex' },
      policy: { rules: [] },
    });

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    const permissions = within(codexPanel).getByRole('region', { name: 'Codex CLI tool permissions' });
    await waitFor(() => {
      expect(within(permissions).getByText('No routes allowed. MCP tool access is blocked.')).toBeInTheDocument();
    });
    expect(within(permissions).getByText('0 allowed')).toBeInTheDocument();
    expect(within(permissions).getByText('0 denied')).toBeInTheDocument();
  });

  it('discards stale tool-policy responses when the provider tab changes', async () => {
    let resolveStaleCodex!: (_response: AgentToolPolicyResponse) => void;
    getAgentToolPolicyMock.mockImplementationOnce(
      () => new Promise(resolve => { resolveStaleCodex = resolve; }),
    );

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    await waitFor(() => {
      expect(getAgentToolPolicyMock).toHaveBeenCalledWith('demo', 'codex');
    });

    // Switch tab — the in-flight Codex request is now cancelled
    fireEvent.click(screen.getByRole('tab', { name: 'Claude Code' }));
    await waitFor(() => {
      expect(getAgentToolPolicyMock).toHaveBeenCalledWith('demo', 'claude');
    });

    // Deliver the stale Codex response after the switch
    await act(async () => {
      resolveStaleCodex({
        scope: { project: 'demo', provider_id: 'codex' },
        policy: {
          rules: [{
            project: 'demo',
            provider_id: 'codex',
            connection_id: 'c1',
            server_id: 'srv',
            tool_name: 'stale_tool',
            modes: ['read_only'],
            risk: 'read_only',
            effect: 'allow',
          }],
        },
      });
      await Promise.resolve();
    });

    const claudePanel = screen.getByRole('tabpanel', { name: 'Claude Code' });
    const permissions = within(claudePanel).getByRole('region', { name: 'Claude Code CLI tool permissions' });
    // Stale codex data must never appear in the Claude tab
    expect(within(permissions).queryByText('stale_tool')).not.toBeInTheDocument();
    // Claude's own request is still loading (default never-resolving mock)
    expect(within(permissions).getByText('Checking scoped tool permissions...')).toBeInTheDocument();
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

  it('clears transient summary polling errors after a successful refresh', async () => {
    vi.useFakeTimers();
    listAgentRunsMock
      .mockResolvedValueOnce([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'running',
        prompt_preview: 'Watch the live run',
      })])
      .mockRejectedValueOnce(new Error('temporary outage'))
      .mockResolvedValue([agentRunSummaryFixture({
        id: 'run_000001',
        status: 'running',
        prompt_preview: 'Watch the live run after recovery',
        updated_at: '2026-07-01T10:00:10Z',
        log_count: 1,
      })]);

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await flushAsyncUpdates();
    expect(within(runsPanel).getByText('Watch the live run')).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(within(runsPanel).getByText('agent run refresh failed: temporary outage')).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(within(runsPanel).queryByText('Could not load agent runs.')).not.toBeInTheDocument();
    expect(within(runsPanel).queryByText('agent run refresh failed: temporary outage')).not.toBeInTheDocument();
    expect(within(runsPanel).getByText('Watch the live run after recovery')).toBeInTheDocument();
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

  it('scopes the tool route preview to the selected active run', async () => {
    listAgentRunsMock.mockResolvedValueOnce([
      agentRunSummaryFixture({ id: 'run_000001', status: 'running', prompt_preview: 'Run one' }),
      agentRunSummaryFixture({ id: 'run_000002', status: 'running', prompt_preview: 'Run two' }),
    ]);
    getAgentRunMock.mockImplementation((_project: string, id: string) => Promise.resolve(
      agentRunFixture({ id, status: 'running', prompt_preview: id === 'run_000001' ? 'Run one' : 'Run two' }),
    ));

    render(<ChatPanel {...baseProps} projectName="demo" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Runs' }));
    const runsPanel = screen.getByRole('tabpanel', { name: 'Runs' });

    await waitFor(() => {
      expect(within(runsPanel).getByRole('button', { name: 'View details for run_000001' })).toBeInTheDocument();
    });
    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000001' }));

    let preview = await within(runsPanel).findByRole('region', { name: 'Tool route preview' });
    expect(within(preview).getByText('run_000001')).toBeInTheDocument();
    fireEvent.change(within(preview).getByLabelText('Connection'), { target: { value: 'stale-connection' } });

    fireEvent.click(within(runsPanel).getByRole('button', { name: 'View details for run_000002' }));
    await waitFor(() => {
      preview = within(runsPanel).getByRole('region', { name: 'Tool route preview' });
      expect(within(preview).getByText('run_000002')).toBeInTheDocument();
    });
    expect(within(preview).getByLabelText('Connection')).toHaveValue('');

    fireEvent.change(within(preview).getByLabelText('Connection'), { target: { value: 'aws-prod' } });
    fireEvent.change(within(preview).getByLabelText('MCP server'), { target: { value: 'aws-official' } });
    fireEvent.change(within(preview).getByLabelText('Tool'), { target: { value: 'list_resources' } });
    fireEvent.click(within(preview).getByRole('button', { name: 'Preview access' }));

    await waitFor(() => {
      expect(previewAgentToolRouteMock).toHaveBeenCalledWith('demo', 'run_000002', {
        connection_id: 'aws-prod',
        server_id: 'aws-official',
        tool_name: 'list_resources',
        risk: 'read_only',
      });
    });

    const details = within(runsPanel).getByRole('region', { name: 'run_000002 details' });
    fireEvent.click(within(details).getByRole('button', { name: 'Close details for run_000002' }));
    expect(within(runsPanel).queryByRole('region', { name: 'Tool route preview' })).not.toBeInTheDocument();
  });

  it.each([
    {
      reason: 'terminal',
      summary: agentRunSummaryFixture({ id: 'run_000001', status: 'completed' }),
      detail: agentRunFixture({ id: 'run_000001', status: 'completed' }),
    },
    {
      reason: 'cross-project',
      summary: agentRunSummaryFixture({ id: 'run_000001', status: 'running' }),
      detail: agentRunFixture({ id: 'run_000001', project: 'other', status: 'running' }),
    },
  ])('keeps the tool route preview hidden for $reason run details', async ({ summary, detail }) => {
    listAgentRunsMock.mockResolvedValueOnce([summary]);
    getAgentRunMock.mockResolvedValueOnce(detail);

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
    expect(within(runsPanel).queryByRole('region', { name: 'Tool route preview' })).not.toBeInTheDocument();
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
