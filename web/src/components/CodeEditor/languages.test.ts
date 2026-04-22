import { describe, expect, it, vi } from 'vitest';

import { languageForPath, registerLanguages } from './languages';

describe('languageForPath', () => {
  it.each([
    ['main.tf', 'hcl'],
    ['variables.tfvars', 'hcl'],
    ['providers.hcl', 'hcl'],
    ['policy.rego', 'rego'],
    ['guard.sentinel', 'sentinel'],
    ['plan.json', 'json'],
    ['values.yaml', 'yaml'],
    ['playbook.yml', 'yaml'],
    ['README.md', 'markdown'],
    ['script.sh', 'plaintext'],
    ['no-extension', 'plaintext'],
  ])('maps %s to %s', (path, expected) => {
    expect(languageForPath(path)).toBe(expected);
  });

  it('is case-insensitive', () => {
    expect(languageForPath('Main.TF')).toBe('hcl');
    expect(languageForPath('POLICY.REGO')).toBe('rego');
  });
});

describe('registerLanguages', () => {
  // registerLanguages touches the Monaco global — we stub the pieces it
  // uses so the tokeniser shape is exercised without pulling the full
  // editor (headless vitest can't load Monaco's worker bundles).
  const makeStub = () => {
    const register = vi.fn();
    const setLanguageConfiguration = vi.fn();
    const setMonarchTokensProvider = vi.fn();
    return {
      namespace: {
        languages: { register, setLanguageConfiguration, setMonarchTokensProvider },
      } as never,
      spies: { register, setLanguageConfiguration, setMonarchTokensProvider },
    };
  };

  it('registers hcl, rego, and sentinel', () => {
    const { namespace, spies } = makeStub();
    registerLanguages(namespace);
    const ids = spies.register.mock.calls.map((c) => c[0].id);
    expect(ids).toEqual(expect.arrayContaining(['hcl', 'rego', 'sentinel']));
  });

  it('is idempotent per Monaco namespace', () => {
    const { namespace, spies } = makeStub();
    registerLanguages(namespace);
    registerLanguages(namespace);
    // Three languages registered once each; a second call is a no-op.
    expect(spies.register).toHaveBeenCalledTimes(3);
  });

  it('attaches a Monarch tokenizer with the expected top-level shape', () => {
    const { namespace, spies } = makeStub();
    registerLanguages(namespace);
    for (const [, spec] of spies.setMonarchTokensProvider.mock.calls) {
      expect(spec).toMatchObject({
        tokenizer: { root: expect.any(Array) },
      });
    }
  });
});
