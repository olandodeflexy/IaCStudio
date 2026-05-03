import { afterEach, describe, expect, it, vi } from 'vitest';

import { api, ApiError } from './api';

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
});
