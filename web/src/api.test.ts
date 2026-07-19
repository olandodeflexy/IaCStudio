import { afterEach, describe, expect, it, vi } from 'vitest';

import { api, ApiError, normalizeSuggestions } from './api';

describe('api.generateTopologyFromImages', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts multipart form data with tool, provider, description, and images', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ message: 'ok', resources: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const image = new File(['bytes'], 'diagram.png', { type: 'image/png' });
    await api.generateTopologyFromImages({
      description: 'three-tier app',
      tool: 'terraform',
      provider: 'aws',
      images: [image],
    });

    expect(fetchMock).toHaveBeenCalledWith('/api/ai/topology/image', {
      method: 'POST',
      body: expect.any(FormData),
    });
    const body = fetchMock.mock.calls[0][1].body as FormData;
    expect(body.get('tool')).toBe('terraform');
    expect(body.get('provider')).toBe('aws');
    expect(body.get('description')).toBe('three-tier app');
    expect(body.getAll('image')).toHaveLength(1);
    expect((body.get('image') as File).name).toBe('diagram.png');
  });
});

describe('api.cloudConnections', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('creates cloud connections with provider metadata and secrets', async () => {
    const response = {
      id: 'conn_1',
      name: 'prod-admin',
      provider: 'aws',
      auth_method: 'aws_static',
      metadata: { access_key_id: 'AKIAEXAMPLE' },
      secret_fields: ['secret_access_key'],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createCloudConnection({
      name: 'prod-admin',
      provider: 'aws',
      auth_method: 'aws_static',
      region: 'us-east-1',
      metadata: { access_key_id: 'AKIAEXAMPLE' },
      secrets: { secret_access_key: 'super-secret' },
    })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/cloud/connections', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: 'prod-admin',
        provider: 'aws',
        auth_method: 'aws_static',
        region: 'us-east-1',
        metadata: { access_key_id: 'AKIAEXAMPLE' },
        secrets: { secret_access_key: 'super-secret' },
      }),
    });
  });

  it('tests cloud connections through the scoped test endpoint', async () => {
    const response = {
      ok: true,
      summary: 'Connection is ready for local IaC workflows.',
      connection: {
        id: 'conn_1',
        name: 'prod-admin',
        provider: 'aws',
        auth_method: 'aws_profile',
      },
      checks: [{ name: 'auth_method', status: 'pass', message: 'configured' }],
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.testCloudConnection('conn_1')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/cloud/connections/conn_1/test', {
      method: 'POST',
    });
  });
});

describe('api.mcpAirlock', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('calls MCP Airlock lifecycle endpoints', async () => {
    const response = {
      server: { id: 'terraform-official', name: 'Terraform MCP Server' },
      ready: true,
      running: true,
      configured: true,
      command_available: true,
      state: 'running',
      summary: 'running',
      checks: [],
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.startMCPAirlockServer('terraform-official')).resolves.toEqual(response);
    await expect(api.stopMCPAirlockServer('terraform-official')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/mcp-airlock/servers/terraform-official/start', {
      method: 'POST',
    });
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/mcp-airlock/servers/terraform-official/stop', {
      method: 'POST',
    });
  });

  it('calls MCP Airlock tool inventory and firewall endpoints', async () => {
    const inventory = {
      server_id: 'terraform-official',
      tools: [{
        server_id: 'terraform-official',
        name: 'apply_workspace',
        input_schema_hash: 'sha256:def',
        last_seen_at: '2026-06-13T10:00:00Z',
        schema_state: 'new',
        risk: 'cloud_mutation',
        decision: {
          status: 'blocked',
          allowed: false,
          approval_required: false,
          risk: 'cloud_mutation',
          reason: 'requires allowlist',
          allowlisted: false,
          untrusted_output: true,
        },
      }],
      checks: [],
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory.tools[0]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(inventory.tools[0]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.getMCPAirlockTools('terraform-official')).resolves.toEqual(inventory);
    await expect(api.discoverMCPAirlockTools('terraform-official')).resolves.toEqual(inventory);
    await expect(api.evaluateMCPAirlockTool('terraform-official', 'apply_workspace', 'demo')).resolves.toEqual(inventory.tools[0]);
    await expect(api.setMCPAirlockToolAllowlist('terraform-official', 'apply_workspace', true, 'demo')).resolves.toEqual(inventory.tools[0]);

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/mcp-airlock/servers/terraform-official/tools');
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/mcp-airlock/servers/terraform-official/tools/discover', {
      method: 'POST',
    });
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/mcp-airlock/servers/terraform-official/tools/evaluate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool_name: 'apply_workspace', project: 'demo' }),
    });
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/mcp-airlock/servers/terraform-official/tools/allowlist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool_name: 'apply_workspace', project: 'demo', allowed: true }),
    });
  });
});

describe('api.listLocalAgentProviders', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('fetches local agent provider statuses from Agent Hub', async () => {
    const response = {
      providers: [{
        id: 'ollama',
        name: 'Ollama',
        category: 'local_model',
        state: 'available',
        installed: true,
        command: 'ollama',
        entrypoint: 'ollama',
        candidates: ['ollama'],
        version: 'unknown',
        capabilities: ['chat', 'local_model', 'offline_runtime'],
        credential_mode: 'none',
        auth_hint: 'Uses local models and does not require cloud credentials.',
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.listLocalAgentProviders()).resolves.toEqual(response.providers);

    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hub/providers/local');
  });

  it('coerces null local agent provider responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response('null', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listLocalAgentProviders()).resolves.toEqual([]);
  });
});

describe('api.listAgentProviderConnections', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('fetches redacted API and enterprise provider connection metadata', async () => {
    const response = {
      providers: [{
        id: 'openai-api',
        name: 'OpenAI API',
        family: 'openai',
        category: 'api',
        credential_mode: 'secret_store',
        required_fields: ['model'],
        secret_fields: ['api_key'],
        capabilities: ['chat', 'tool_calling'],
        cost_controls: ['monthly_budget'],
        billing_hint: 'Billed through OpenAI Platform API usage.',
        data_handling_hint: 'Prompts are sent to the configured endpoint.',
        secret_storage_hint: 'Keys are stored through secret stores and never returned.',
        setup_hint: 'Use for automation.',
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.listAgentProviderConnections()).resolves.toEqual(response.providers);

    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hub/providers/connections');
  });

  it('coerces null provider connection responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response('null', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listAgentProviderConnections()).resolves.toEqual([]);
  });
});

describe('api.agentProviderConnectionProfiles', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists redacted saved provider connection profiles', async () => {
    const response = {
      connections: [{
        id: 'agent_provider_connection_000001',
        name: 'OpenAI automation',
        provider_id: 'openai-api',
        credential_mode: 'secret_store',
        metadata: { model: 'gpt-5' },
        cost_controls: { monthly_budget: '100' },
        secret_fields: ['api_key'],
        secret_store: 'local_encrypted',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.listAgentProviderConnectionProfiles()).resolves.toEqual(response.connections);

    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hub/provider-connections');
  });

  it('coerces null saved provider connection responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response('null', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listAgentProviderConnectionProfiles()).resolves.toEqual([]);
  });

  it('creates saved provider connection profiles with secret payloads', async () => {
    const response = {
      id: 'agent_provider_connection_000001',
      name: 'OpenAI automation',
      provider_id: 'openai-api',
      credential_mode: 'secret_store',
      secret_fields: ['api_key'],
      secret_store: 'local_encrypted',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
    };
    const input = {
      name: 'OpenAI automation',
      provider_id: 'openai-api',
      credential_mode: 'secret_store' as const,
      metadata: { model: 'gpt-5' },
      cost_controls: { monthly_budget: '100' },
      secrets: { api_key: 'sk-test' },
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createAgentProviderConnectionProfile(input)).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hub/provider-connections', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
  });

  it('gets and updates saved provider connection profiles by encoded id', async () => {
    const response = {
      id: 'profile with/slash',
      name: 'Anthropic automation',
      provider_id: 'anthropic-api',
      credential_mode: 'secret_store',
      metadata: { model: 'claude-sonnet' },
      secret_fields: ['api_key'],
      secret_store: 'local_encrypted',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
    };
    const input = {
      name: 'Anthropic automation',
      provider_id: 'anthropic-api',
      credential_mode: 'secret_store' as const,
      metadata: { model: 'claude-opus' },
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.getAgentProviderConnectionProfile('profile with/slash')).resolves.toEqual(response);
    await expect(api.updateAgentProviderConnectionProfile('profile with/slash', input)).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/agent-hub/provider-connections/profile%20with%2Fslash');
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/agent-hub/provider-connections/profile%20with%2Fslash', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
  });

  it('deletes saved provider connection profiles by encoded id', async () => {
    const response = { status: 'deleted' };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.deleteAgentProviderConnectionProfile('profile with/slash')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hub/provider-connections/profile%20with%2Fslash', {
      method: 'DELETE',
    });
  });
});

describe('api.agentRuns', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists project-scoped agent run summaries', async () => {
    const response = {
      runs: [{
        id: 'run_000001',
        project: 'demo',
        provider_id: 'codex',
        mode: 'read_only',
        status: 'queued',
        prompt_preview: 'Review this project',
        prompt_hash: 'sha256:abc',
        created_at: '2026-07-01T10:00:00Z',
        updated_at: '2026-07-01T10:00:00Z',
        canceled: false,
        log_count: 0,
        patch_count: 0,
        approval_count: 0,
        pending_approval_count: 1,
        pending_gates: [{
          id: 'approval_000001',
          kind: 'command',
          summary: 'Run terraform plan',
          created_at: '2026-07-01T10:00:00Z',
        }],
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.listAgentRuns('demo')).resolves.toEqual(response.runs);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/agent-runs');
  });

  it('coerces missing agent run lists to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listAgentRuns('demo')).resolves.toEqual([]);
  });

  it('creates project-scoped agent runs without client identity fields', async () => {
    const response = {
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'read_only',
      status: 'queued',
      prompt_preview: 'Review this project',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      logs: [],
      patches: [],
      approvals: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createAgentRun('demo', {
      prompt: 'Review this project',
      provider_id: 'codex',
      mode: 'read_only',
    })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/agent-runs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        prompt: 'Review this project',
        provider_id: 'codex',
        mode: 'read_only',
      }),
    });
  });

  it('gets one project-scoped agent run detail record', async () => {
    const response = {
      id: 'run_000001',
      project: 'demo',
      mode: 'read_only',
      status: 'queued',
      prompt_preview: 'Review this project',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:00:00Z',
      canceled: false,
      logs: [],
      patches: [],
      approvals: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.getAgentRun('demo', 'run_000001')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/agent-runs/run_000001');
  });

  it('cancels one project-scoped agent run', async () => {
    const response = {
      id: 'run_000001',
      project: 'demo',
      mode: 'read_only',
      status: 'canceled',
      prompt_preview: 'Review this project',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:01:00Z',
      completed_at: '2026-07-01T10:01:00Z',
      canceled: true,
      logs: [],
      patches: [],
      approvals: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.cancelAgentRun('demo', 'run_000001')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/agent-runs/run_000001/cancel', {
      method: 'POST',
    });
  });

  it('decides one project-scoped agent run approval', async () => {
    const response = {
      id: 'run_000001',
      project: 'demo',
      mode: 'propose_only',
      status: 'running',
      prompt_preview: 'Review this project',
      prompt_hash: 'sha256:abc',
      created_at: '2026-07-01T10:00:00Z',
      updated_at: '2026-07-01T10:01:00Z',
      canceled: false,
      logs: [],
      patches: [],
      approvals: [{
        id: 'approval_000001',
        kind: 'command',
        status: 'approved',
        summary: 'Run terraform plan',
        created_at: '2026-07-01T10:00:00Z',
        decided_at: '2026-07-01T10:01:00Z',
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.decideAgentRunApproval(
      'demo',
      'run_000001',
      'approval_000001',
      'approved',
    )).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/agent-runs/run_000001/approvals/approval_000001/decision', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'approved' }),
    });
  });
});

describe('api.agentToolRoutes', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('previews a run-scoped tool route without client-owned scope fields', async () => {
    const response = {
      decision: {
        status: 'allowed',
        reason: 'allowed',
        allowed: true,
        approval_required: false,
        untrusted_output: true,
      },
    };
    const input = {
      connection_id: 'aws-prod',
      server_id: 'aws-official',
      tool_name: 'list_resources',
      risk: 'read_only' as const,
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.previewAgentToolRoute('demo project', 'run/000001', input)).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo%20project/agent-runs/run%2F000001/tool-routes/preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    });
  });
});

describe('api.agentToolPolicies', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('gets one exact project and provider routing policy', async () => {
    const response = {
      scope: {
        project: 'demo',
        provider_id: 'codex-cli',
      },
      policy: {
        rules: [{
          project: 'demo',
          provider_id: 'codex-cli',
          connection_id: 'aws-prod',
          server_id: 'aws-official',
          tool_name: 'list_resources',
          modes: ['read_only'],
          risk: 'read_only',
          effect: 'allow',
        }],
      },
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.getAgentToolPolicy('demo', 'codex-cli')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/projects/demo/agent-routing/policies/codex-cli',
    );
  });

  it('replaces one exact project and provider routing policy', async () => {
    const policy = {
      rules: [{
        project: 'demo',
        provider_id: 'codex-cli',
        connection_id: 'aws-prod',
        server_id: 'aws-official',
        tool_name: 'list_resources',
        modes: ['read_only' as const],
        risk: 'read_only' as const,
        effect: 'allow' as const,
      }],
    };
    const response = {
      scope: { project: 'demo', provider_id: 'codex-cli' },
      policy,
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.saveAgentToolPolicy('demo', 'codex-cli', policy)).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/projects/demo/agent-routing/policies/codex-cli',
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(policy),
      },
    );
  });
});

describe('api.listProjectStates', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('returns project state arrays unchanged', async () => {
    const states = [{ name: 'demo', tool: 'terraform' }];
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify(states), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listProjectStates()).resolves.toEqual(states);
  });

  it('coerces null project state responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response('null', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listProjectStates()).resolves.toEqual([]);
  });

  it('coerces malformed project state responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ states: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.listProjectStates()).resolves.toEqual([]);
  });
});

describe('api.runCommand', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('preserves structured policy_blocked errors for the UI override flow', async () => {
    const payload = {
      error: 'policy_blocked',
      detail: 'policy engine returned error-severity findings',
      findings: [{
        engine: 'crossguard',
        policy_id: 'required-owner-tag',
        policy_name: 'Required owner tag',
        severity: 'error',
        message: 'bucket should define an Owner tag',
      }],
    };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify(payload), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    let caught: unknown;
    try {
      await api.runCommand('demo', 'pulumi', 'apply', { approved: true, env: 'dev' });
    } catch (err) {
      caught = err;
    }

    expect(caught).toBeInstanceOf(ApiError);
    expect(caught).toMatchObject({
      name: 'ApiError',
      status: 409,
      payload,
    });
  });

  it('preserves structured plan_risk_blocked errors for the UI override flow', async () => {
    const payload = {
      error: 'plan_risk_blocked',
      detail: 'semantic plan classifier found risky changes',
      classification: {
        summary: {
          safe: 0,
          risky: 1,
          destructive: 0,
          unknown: 0,
          total: 1,
          requires_acknowledgment: true,
          text: 'Semantic plan: 1 risky',
        },
        changes: [{
          address: 'aws_security_group.web',
          action: 'update',
          risk: 'risky',
          categories: ['network_exposure'],
          reason: 'public CIDR exposure',
          reviewer_focus: ['Check ingress.'],
        }],
      },
    };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify(payload), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    let caught: unknown;
    try {
      await api.runCommand('demo', 'terraform', 'apply', { approved: true, env: 'dev' });
    } catch (err) {
      caught = err;
    }

    expect(caught).toBeInstanceOf(ApiError);
    expect(caught).toMatchObject({
      name: 'ApiError',
      status: 409,
      payload,
    });
  });

  it('sends the selected cloud connection when running commands', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: 'running' }), {
        status: 202,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await api.runCommand('demo', 'terraform', 'plan', { connectionId: 'conn_1', env: 'dev' });

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: 'terraform',
        command: 'plan',
        approved: false,
        env: 'dev',
        acknowledged: false,
        risk_acknowledged: false,
        connection_id: 'conn_1',
      }),
    });
  });

  it('sends plan_hash for approved mutating commands', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ status: 'running' }), {
        status: 202,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await api.runCommand('demo', 'terraform', 'apply', {
      approved: true,
      env: 'prod',
      planHash: 'plan_abc123',
    });

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        tool: 'terraform',
        command: 'apply',
        approved: true,
        env: 'prod',
        acknowledged: false,
        risk_acknowledged: false,
        connection_id: undefined,
        plan_hash: 'plan_abc123',
      }),
    });
  });
});

describe('api.runDrift', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts tool, environment, and connection context to the drift endpoint', async () => {
    const response = {
      has_state: true,
      state_path: '/tmp/demo/terraform.tfstate',
      drifted: [],
      findings: [],
      suppressed_findings: [],
      suppressed: 0,
      missing: [],
      unmanaged: [],
      in_sync: 1,
      total: 1,
      classifications: {},
      summary: '1 resources: 1 in sync, 0 drifted, 0 missing from state, 0 unmanaged',
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.runDrift('demo', { tool: 'terraform', env: 'dev', connectionId: 'conn_1' })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', connection_id: 'conn_1' }),
    });
  });
});

describe('api.createDriftRemediation', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts remediation mode with tool, environment, and connection context', async () => {
    const response = {
      mode: 'revert',
      title: 'Revert unauthorized drift for demo',
      branch: 'iac-studio-drift-revert-demo-dev',
      commit_message: 'Document drift revert for demo',
      body: '## Summary',
      findings: [],
      file_changes: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createDriftRemediation('demo', { tool: 'terraform', env: 'dev', connectionId: 'conn_1', mode: 'revert' })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift/remediation', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', connection_id: 'conn_1', mode: 'revert' }),
    });
  });
});

describe('api.createDriftRemediationArtifacts', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts remediation artifact generation requests', async () => {
    const response = {
      id: 'iac-studio-drift-revert-demo-dev',
      root: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev',
      created_at: '2026-06-09T19:00:00Z',
      proposal: {
        mode: 'revert',
        title: 'Revert unauthorized drift for demo',
        branch: 'iac-studio-drift-revert-demo-dev',
        commit_message: 'Document drift revert for demo',
        body: '## Summary',
        findings: [],
        file_changes: [],
      },
      files: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const proposal = response.proposal;
    await expect(api.createDriftRemediationArtifacts('demo', { tool: 'terraform', env: 'dev', connectionId: 'conn_1', mode: 'revert', proposal })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift/remediation/artifacts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', connection_id: 'conn_1', mode: 'revert', proposal }),
    });
  });
});

describe('api.createDriftRemediationPullRequest', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts remediation PR branch generation requests', async () => {
    const proposal = {
      mode: 'revert' as const,
      title: 'Revert unauthorized drift for demo',
      branch: 'iac-studio-drift-revert-demo-dev',
      commit_message: 'Document drift revert for demo',
      body: '## Summary',
      findings: [],
      file_changes: [],
    };
    const response = {
      artifacts: {
        id: 'iac-studio-drift-revert-demo-dev',
        root: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev',
        created_at: '2026-06-09T19:00:00Z',
        proposal,
        files: [],
      },
      pull_request: {
        title: proposal.title,
        branch: proposal.branch,
        base_branch: 'main',
        commit: 'abc123',
        commit_message: proposal.commit_message,
        body_path: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev/pr-body.md',
        files: [],
        commands: [],
      },
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createDriftRemediationPullRequest('demo', { tool: 'terraform', env: 'dev', connectionId: 'conn_1', mode: 'revert', proposal })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift/remediation/pr', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', connection_id: 'conn_1', mode: 'revert', proposal }),
    });
  });
});

describe('api.listStateSnapshots', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('fetches environment-scoped recovery snapshots', async () => {
    const response = [{
      id: '20260610T120000Z-terraform-apply-dev-abc12345',
      project: 'demo',
      tool: 'terraform',
      env: 'dev',
      command: 'apply',
      work_dir: 'environments/dev',
      state_path: 'environments/dev/terraform.tfstate',
      state_sha256: 'abc123',
      state_size: 42,
      created_at: '2026-06-10T12:00:00Z',
      status: 'recorded',
    }];
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.listStateSnapshots('demo', 'dev')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/snapshots?env=dev');
  });
});

describe('api.createRollbackProposal', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts rollback proposal requests for a selected checkpoint', async () => {
    const response = {
      id: 'rollback-demo-terraform-dev-checkpoint',
      title: 'Rollback demo to checkpoint checkpoint',
      branch: 'iac-studio-rollback-demo-checkpoint',
      commit_message: 'Document rollback proposal for demo',
      body: '## Summary',
      tool: 'terraform',
      env: 'dev',
      work_dir: 'environments/dev',
      target_snapshot: {
        id: 'checkpoint',
        project: 'demo',
        tool: 'terraform',
        env: 'dev',
        command: 'apply',
        work_dir: 'environments/dev',
        created_at: '2026-06-10T12:00:00Z',
        status: 'recorded',
      },
      classification: {
        summary: {
          safe: 0,
          risky: 0,
          destructive: 0,
          unknown: 1,
          total: 1,
          requires_acknowledgment: true,
          text: 'Semantic plan: 1 unknown change',
        },
        changes: [],
      },
      warnings: ['Generate and review a fresh plan before applying any rollback.'],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createRollbackProposal('demo', 'checkpoint', { env: 'dev' })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/snapshots/checkpoint/rollback', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ env: 'dev' }),
    });
  });
});

describe('api.createRollbackArtifacts', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts rollback artifact generation requests with the reviewed proposal', async () => {
    const proposal = {
      id: 'rollback-demo-terraform-dev-checkpoint',
      title: 'Rollback demo to checkpoint checkpoint',
      branch: 'iac-studio-rollback-demo-checkpoint',
      commit_message: 'Document rollback proposal for demo',
      body: '## Summary',
      tool: 'terraform',
      env: 'dev',
      work_dir: 'environments/dev',
      target_snapshot: {
        id: 'checkpoint',
        project: 'demo',
        tool: 'terraform',
        env: 'dev',
        command: 'apply',
        work_dir: 'environments/dev',
        created_at: '2026-06-10T12:00:00Z',
        status: 'recorded',
      },
      classification: {
        summary: {
          safe: 0,
          risky: 0,
          destructive: 0,
          unknown: 1,
          total: 1,
          requires_acknowledgment: true,
          text: 'Semantic plan: 1 unknown change',
        },
        changes: [],
      },
    };
    const response = {
      id: 'rollback-demo-terraform-dev-checkpoint',
      root: '.iac-studio/rollbacks/rollback-demo-terraform-dev-checkpoint',
      created_at: '2026-06-10T13:00:00Z',
      proposal,
      files: [],
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createRollbackArtifacts('demo', 'checkpoint', { env: 'dev', proposal })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/snapshots/checkpoint/rollback/artifacts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ env: 'dev', proposal }),
    });
  });
});

describe('api.createRollbackPullRequest', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts rollback PR branch generation requests', async () => {
    const proposal = {
      id: 'rollback-demo-terraform-dev-checkpoint',
      title: 'Rollback demo to checkpoint checkpoint',
      branch: 'iac-studio-rollback-demo-checkpoint',
      commit_message: 'Document rollback proposal for demo',
      body: '## Summary',
      tool: 'terraform',
      env: 'dev',
      work_dir: 'environments/dev',
      target_snapshot: {
        id: 'checkpoint',
        project: 'demo',
        tool: 'terraform',
        env: 'dev',
        command: 'apply',
        work_dir: 'environments/dev',
        created_at: '2026-06-10T12:00:00Z',
        status: 'recorded',
      },
      classification: {
        summary: {
          safe: 0,
          risky: 0,
          destructive: 0,
          unknown: 1,
          total: 1,
          requires_acknowledgment: true,
          text: 'Semantic plan: 1 unknown change',
        },
        changes: [],
      },
    };
    const response = {
      artifacts: {
        id: 'rollback-demo-terraform-dev-checkpoint',
        root: '.iac-studio/rollbacks/rollback-demo-terraform-dev-checkpoint',
        created_at: '2026-06-10T13:00:00Z',
        proposal,
        files: [],
      },
      pull_request: {
        title: proposal.title,
        branch: proposal.branch,
        base_branch: 'main',
        commit: 'abc123',
        commit_message: proposal.commit_message,
        body_path: '.iac-studio/rollbacks/rollback-demo-terraform-dev-checkpoint/proposal.md',
        files: [],
        commands: [],
      },
    };
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createRollbackPullRequest('demo', 'checkpoint', { env: 'dev', proposal })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/snapshots/checkpoint/rollback/pr', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ env: 'dev', proposal }),
    });
  });
});

describe('api.suggest', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('normalizes null suggestion responses to an empty array', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response('null', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ));

    await expect(api.suggest('terraform', 'aws', [])).resolves.toEqual([]);
  });
});

describe('normalizeSuggestions', () => {
  it('normalizes omitted and explicit null suggestions to empty arrays', () => {
    expect(normalizeSuggestions(undefined)).toEqual([]);
    expect(normalizeSuggestions(null)).toEqual([]);
  });
});
