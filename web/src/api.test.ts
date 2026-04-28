import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from './api';

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
