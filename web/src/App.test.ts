import { describe, expect, it } from 'vitest';

import { normalizeLayeredProject, resourcesForEnv } from './App';

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

describe('resourcesForEnv', () => {
  it('keeps only resources that are explicitly owned by the selected environment', () => {
    const resources = [
      { id: 'dev', file: 'environments/dev/main.tf' },
      { id: 'prod', file: 'environments/prod/main.tf' },
      { id: 'root', file: 'main.tf' },
      { id: 'legacy' },
    ];

    expect(resourcesForEnv(resources, 'dev').map(resource => resource.id)).toEqual(['dev']);
  });

  it('does not filter resources when no environment is active', () => {
    const resources = [{ id: 'root' }, { id: 'dev', file: 'environments/dev/main.tf' }];

    expect(resourcesForEnv(resources).map(resource => resource.id)).toEqual(['root', 'dev']);
  });
});
