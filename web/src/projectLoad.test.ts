import { describe, expect, it } from 'vitest';

import { envForResourceLoad, envForTool, shouldParseResourcesFromDisk, toolForEnv } from './projectLoad';

describe('project load helpers', () => {
  it('does not force Pulumi disk parsing when saved canvas resources exist', () => {
    expect(shouldParseResourcesFromDisk({
      resources: [{ file: 'index.ts' }],
    })).toBe(false);
  });

  it('parses from disk when saved state has no resources', () => {
    expect(shouldParseResourcesFromDisk({ resources: [] })).toBe(true);
    expect(shouldParseResourcesFromDisk(null)).toBe(true);
  });

  it('parses layered projects when saved resources are missing file ownership', () => {
    expect(shouldParseResourcesFromDisk({
      layout: 'layered-v1',
      resources: [{ file: 'environments/dev/main.tf' }, {}],
    })).toBe(true);
  });

  it('uses the first layered environment for Pulumi resource parsing', () => {
    expect(envForTool('pulumi', { environments: ['dev', 'prod'] })).toBe('dev');
    expect(envForTool('pulumi', { environments: ['dev', 'prod'] }, 'prod')).toBe('prod');
    expect(envForTool('terraform', { environments: ['dev'] })).toBeUndefined();
  });

  it('lets hybrid resource parsing cover every environment', () => {
    const layered = {
      environments: ['dev', 'prod'],
      environmentTools: { dev: 'pulumi', prod: 'terraform' },
    };
    expect(envForResourceLoad('multi', layered)).toBeUndefined();
    expect(envForTool('multi', layered, 'prod')).toBe('prod');
    expect(toolForEnv('multi', layered, 'dev')).toBe('pulumi');
    expect(toolForEnv('multi', layered, 'prod')).toBe('terraform');
  });
});
