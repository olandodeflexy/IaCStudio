import { describe, expect, it } from 'vitest';

import { normalizeLayeredProject } from './App';

describe('normalizeLayeredProject', () => {
  it('drops empty or invalid environment tool maps', () => {
    expect(normalizeLayeredProject({
      layout: 'layered-v1',
      environments: ['dev'],
      environment_tools: { prod: 'terraform' },
    })?.environmentTools).toBeUndefined();

    expect(normalizeLayeredProject({
      layout: 'layered-v1',
      environments: ['dev'],
      environment_tools: ['terraform'],
    })?.environmentTools).toBeUndefined();
  });

  it('keeps valid environment tool mappings', () => {
    expect(normalizeLayeredProject({
      layout: 'layered-v1',
      environments: ['dev', 'prod'],
      environment_tools: { dev: 'pulumi', prod: 'terraform', qa: 'terraform' },
    })?.environmentTools).toEqual({ dev: 'pulumi', prod: 'terraform' });
  });
});
