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
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.testCloudConnection('conn_1')).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/cloud/connections/conn_1/test', {
      method: 'POST',
    });
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
});

describe('api.runDrift', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts tool and environment to the drift endpoint', async () => {
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

    await expect(api.runDrift('demo', { tool: 'terraform', env: 'dev' })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev' }),
    });
  });
});

describe('api.createDriftRemediation', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('posts remediation mode with tool and environment', async () => {
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

    await expect(api.createDriftRemediation('demo', { tool: 'terraform', env: 'dev', mode: 'revert' })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift/remediation', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', mode: 'revert' }),
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
    await expect(api.createDriftRemediationArtifacts('demo', { tool: 'terraform', env: 'dev', mode: 'revert', proposal })).resolves.toEqual(response);

    expect(fetchMock).toHaveBeenCalledWith('/api/projects/demo/drift/remediation/artifacts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tool: 'terraform', env: 'dev', mode: 'revert', proposal }),
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
