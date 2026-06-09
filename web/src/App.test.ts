import { describe, expect, it } from 'vitest';

import { extractLayoutMeta, normalizeLayeredProject, resourcesForEnv } from './app/layered';

describe('extractLayoutMeta', () => {
  it('preserves drift suppression metadata for flat projects', () => {
    expect(extractLayoutMeta({
      drift: {
        suppressions: [
          {
            address: 'aws_s3_bucket.logs',
            path: 'tags',
            reason: 'provider-managed owner tag',
          },
        ],
      },
      resources: [],
    })).toEqual({
      drift: {
        suppressions: [
          {
            address: 'aws_s3_bucket.logs',
            path: 'tags',
            reason: 'provider-managed owner tag',
          },
        ],
      },
    });
  });
});

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
    const layered = normalizeLayeredProject({
      layout: 'layered-v1',
      environments: ['dev', 'prod'],
      environment_tools: { dev: 'pulumi', prod: 'terraform', qa: 'terraform' },
    });

    expect({ ...layered?.environmentTools }).toEqual({ dev: 'pulumi', prod: 'terraform' });
  });

  it('stores environment tool mappings in a null-prototype map', () => {
    const layered = normalizeLayeredProject({
      layout: 'layered-v1',
      environments: ['__proto__', 'constructor'],
      environment_tools: JSON.parse('{"__proto__":"pulumi","constructor":"terraform"}'),
    });

    expect(Object.getPrototypeOf(layered?.environmentTools)).toBeNull();
    expect(layered?.environmentTools?.['__proto__']).toBe('pulumi');
    expect(layered?.environmentTools?.['constructor']).toBe('terraform');
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
