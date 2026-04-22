import { describe, expect, it } from 'vitest';

import type { Resource } from '../../types';

import { defaultLayeredClassifier } from './SwimlaneCanvas';

const make = (id: string, file?: string): Resource => ({
  id,
  type: 'aws_vpc',
  name: id,
  properties: {},
  file,
});

describe('defaultLayeredClassifier (layered-v1 scaffold)', () => {
  const envs = ['dev', 'stage', 'prod'];
  const modules = ['networking', 'compute', 'root'];
  const classify = defaultLayeredClassifier(envs, modules);

  it('maps environments/<env>/<stem>.tf to (env, <stem>) when <stem> is a known module', () => {
    expect(classify(make('a', 'environments/dev/networking.tf'))).toEqual({
      environment: 'dev',
      module: 'networking',
    });
  });

  it('falls back to the "root" pseudo-module when the filename stem is not a known module', () => {
    expect(classify(make('b', 'environments/prod/main.tf'))).toEqual({
      environment: 'prod',
      module: 'root',
    });
  });

  it('omits "root" when the caller does not register it', () => {
    const strict = defaultLayeredClassifier(envs, ['networking']);
    expect(strict(make('c', 'environments/dev/main.tf'))).toBeNull();
  });

  it('returns null for modules/<mod>/... (template lives outside any env)', () => {
    expect(classify(make('d', 'modules/networking/main.tf'))).toBeNull();
  });

  it('returns null for unknown environments', () => {
    expect(classify(make('e', 'environments/sandbox/networking.tf'))).toBeNull();
  });

  it('returns null when no file is set', () => {
    expect(classify(make('f'))).toBeNull();
  });

  it('handles paths with a leading directory prefix (e.g. project root)', () => {
    expect(classify(make('g', 'projects/demo/environments/stage/compute.tf'))).toEqual({
      environment: 'stage',
      module: 'compute',
    });
  });
});
