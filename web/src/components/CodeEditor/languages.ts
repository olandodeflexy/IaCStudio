import type * as monaco from 'monaco-editor';

// Monaco Monarch tokenisers for the languages IaC Studio authors. Kept
// in one module so registration is idempotent: languages register once
// per Monaco instance and subsequent calls short-circuit.
//
// Monarch is regex-based and lives entirely on the worker thread, so
// there's no language-server dependency to ship. When/if we wire a
// proper terraform-ls or regols LSP, these become the fallback and the
// LSP takes over for rich features (go-to-def, hover, etc.).

export type SupportedLanguage = 'hcl' | 'rego' | 'sentinel';

const registered = new WeakSet<typeof monaco>();

export function registerLanguages(m: typeof monaco): void {
  if (registered.has(m)) return;
  registered.add(m);

  registerHCL(m);
  registerRego(m);
  registerSentinel(m);
}

// ─── HCL (Terraform / OpenTofu) ──────────────────────────────────────
function registerHCL(m: typeof monaco): void {
  m.languages.register({ id: 'hcl', extensions: ['.tf', '.hcl', '.tfvars'], aliases: ['HCL', 'terraform'] });

  m.languages.setLanguageConfiguration('hcl', {
    comments: { lineComment: '#', blockComment: ['/*', '*/'] },
    brackets: [['{', '}'], ['[', ']'], ['(', ')']],
    autoClosingPairs: [
      { open: '{', close: '}' },
      { open: '[', close: ']' },
      { open: '(', close: ')' },
      { open: '"', close: '"' },
    ],
  });

  m.languages.setMonarchTokensProvider('hcl', {
    defaultToken: '',
    tokenPostfix: '.hcl',
    keywords: [
      'resource', 'data', 'module', 'provider', 'variable', 'output',
      'locals', 'terraform', 'required_providers', 'required_version',
      'for_each', 'count', 'depends_on', 'lifecycle', 'dynamic',
      'backend', 'workspace', 'true', 'false', 'null',
    ],
    operators: ['=', '==', '!=', '<', '<=', '>', '>=', '&&', '||', '!', '?', ':'],
    symbols: /[=<>!~?:&|+\-*/^%]+/,
    escapes: /\\(?:[abfnrtv\\"']|x[0-9A-Fa-f]{1,4}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8})/,
    tokenizer: {
      root: [
        [/\$\{/, { token: 'delimiter.interp', next: '@interp' }],
        [/[a-zA-Z_][\w-]*/, { cases: { '@keywords': 'keyword', '@default': 'identifier' } }],
        { include: '@whitespace' },
        [/[{}()[\]]/, '@brackets'],
        [/@symbols/, { cases: { '@operators': 'operator', '@default': '' } }],
        [/\d+(\.\d+)?([eE][-+]?\d+)?/, 'number'],
        [/"([^"\\]|\\.)*$/, 'string.invalid'],
        [/"/, { token: 'string.quote', next: '@string' }],
        [/[;,.]/, 'delimiter'],
      ],
      whitespace: [
        [/[ \t\r\n]+/, 'white'],
        [/#.*$/, 'comment'],
        [/\/\/.*$/, 'comment'],
        [/\/\*/, { token: 'comment', next: '@blockcomment' }],
      ],
      blockcomment: [
        [/[^/*]+/, 'comment'],
        [/\*\//, { token: 'comment', next: '@pop' }],
        [/[/*]/, 'comment'],
      ],
      string: [
        [/[^\\"$]+/, 'string'],
        [/\$\{/, { token: 'delimiter.interp', next: '@interp' }],
        [/@escapes/, 'string.escape'],
        [/\\./, 'string.escape.invalid'],
        [/"/, { token: 'string.quote', next: '@pop' }],
      ],
      interp: [
        [/}/, { token: 'delimiter.interp', next: '@pop' }],
        [/[a-zA-Z_][\w.]*/, 'variable'],
        [/\d+/, 'number'],
        [/[.(),[\]]/, 'delimiter'],
        [/@symbols/, 'operator'],
        [/[ \t]+/, 'white'],
      ],
    },
  });
}

// ─── Rego (Open Policy Agent) ────────────────────────────────────────
function registerRego(m: typeof monaco): void {
  m.languages.register({ id: 'rego', extensions: ['.rego'], aliases: ['Rego', 'OPA'] });

  m.languages.setLanguageConfiguration('rego', {
    comments: { lineComment: '#' },
    brackets: [['{', '}'], ['[', ']'], ['(', ')']],
    autoClosingPairs: [
      { open: '{', close: '}' },
      { open: '[', close: ']' },
      { open: '(', close: ')' },
      { open: '"', close: '"' },
    ],
  });

  m.languages.setMonarchTokensProvider('rego', {
    defaultToken: '',
    tokenPostfix: '.rego',
    keywords: [
      'package', 'import', 'default', 'not', 'if', 'else', 'as',
      'some', 'every', 'with', 'in', 'contains',
      'true', 'false', 'null',
    ],
    builtins: [
      'allow', 'deny', 'violation', 'warn', 'data', 'input',
      'count', 'sum', 'max', 'min', 'sort', 'array', 'object',
      'regex', 'startswith', 'endswith', 'contains',
    ],
    operators: [':=', '=', '==', '!=', '<', '<=', '>', '>=', '|', '&'],
    symbols: /[=<>!:&|+\-*/]+/,
    tokenizer: {
      root: [
        [/[a-z_][\w]*/, {
          cases: {
            '@keywords': 'keyword',
            '@builtins': 'type.identifier',
            '@default': 'identifier',
          },
        }],
        [/[A-Z][\w]*/, 'type.identifier'],
        { include: '@whitespace' },
        [/[{}()[\]]/, '@brackets'],
        [/@symbols/, { cases: { '@operators': 'operator', '@default': '' } }],
        [/\d+(\.\d+)?/, 'number'],
        [/"([^"\\]|\\.)*$/, 'string.invalid'],
        [/"/, { token: 'string.quote', next: '@string' }],
        [/`/, { token: 'string.quote', next: '@rawstring' }],
        [/[;,.]/, 'delimiter'],
      ],
      whitespace: [
        [/[ \t\r\n]+/, 'white'],
        [/#.*$/, 'comment'],
      ],
      string: [
        [/[^\\"]+/, 'string'],
        [/\\./, 'string.escape'],
        [/"/, { token: 'string.quote', next: '@pop' }],
      ],
      rawstring: [
        [/[^`]+/, 'string'],
        [/`/, { token: 'string.quote', next: '@pop' }],
      ],
    },
  });
}

// ─── Sentinel (HashiCorp) ────────────────────────────────────────────
function registerSentinel(m: typeof monaco): void {
  m.languages.register({ id: 'sentinel', extensions: ['.sentinel'], aliases: ['Sentinel'] });

  m.languages.setLanguageConfiguration('sentinel', {
    comments: { lineComment: '#', blockComment: ['/*', '*/'] },
    brackets: [['{', '}'], ['[', ']'], ['(', ')']],
    autoClosingPairs: [
      { open: '{', close: '}' },
      { open: '[', close: ']' },
      { open: '(', close: ')' },
      { open: '"', close: '"' },
    ],
  });

  m.languages.setMonarchTokensProvider('sentinel', {
    defaultToken: '',
    tokenPostfix: '.sentinel',
    keywords: [
      'import', 'param', 'rule', 'main', 'all', 'any', 'filter',
      'map', 'func', 'return', 'if', 'else', 'for', 'in',
      'and', 'or', 'not', 'is', 'contains', 'matches',
      'true', 'false', 'null', 'undefined',
    ],
    operators: ['=', '==', '!=', '<', '<=', '>', '>=', '&&', '||', '!'],
    symbols: /[=<>!&|+\-*/]+/,
    tokenizer: {
      root: [
        [/[a-zA-Z_][\w]*/, { cases: { '@keywords': 'keyword', '@default': 'identifier' } }],
        { include: '@whitespace' },
        [/[{}()[\]]/, '@brackets'],
        [/@symbols/, { cases: { '@operators': 'operator', '@default': '' } }],
        [/\d+(\.\d+)?/, 'number'],
        [/"([^"\\]|\\.)*$/, 'string.invalid'],
        [/"/, { token: 'string.quote', next: '@string' }],
        [/[;,.]/, 'delimiter'],
      ],
      whitespace: [
        [/[ \t\r\n]+/, 'white'],
        [/#.*$/, 'comment'],
        [/\/\/.*$/, 'comment'],
        [/\/\*/, { token: 'comment', next: '@blockcomment' }],
      ],
      blockcomment: [
        [/[^/*]+/, 'comment'],
        [/\*\//, { token: 'comment', next: '@pop' }],
        [/[/*]/, 'comment'],
      ],
      string: [
        [/[^\\"]+/, 'string'],
        [/\\./, 'string.escape'],
        [/"/, { token: 'string.quote', next: '@pop' }],
      ],
    },
  });
}

// Theme tuned to match the app's dark palette — overrides Monaco's
// default "vs-dark" so the editor blends with the surrounding panels.
export const studioDarkTheme: monaco.editor.IStandaloneThemeData = {
  base: 'vs-dark',
  inherit: true,
  rules: [
    { token: 'keyword', foreground: '54b8a9', fontStyle: 'bold' },
    { token: 'identifier', foreground: 'dde6df' },
    { token: 'type.identifier', foreground: 'd9b15c' },
    { token: 'string', foreground: 'c3e4d4' },
    { token: 'string.quote', foreground: 'c3e4d4' },
    { token: 'comment', foreground: '6d7a72', fontStyle: 'italic' },
    { token: 'number', foreground: 'd9b15c' },
    { token: 'operator', foreground: '93a39a' },
    { token: 'delimiter', foreground: '93a39a' },
    { token: 'delimiter.interp', foreground: 'cf6767' },
    { token: 'variable', foreground: 'dde6df' },
  ],
  colors: {
    'editor.background': '#171d1b',
    'editor.foreground': '#dde6df',
    'editor.lineHighlightBackground': '#1d2522',
    'editor.selectionBackground': '#54b8a933',
    'editorLineNumber.foreground': '#6d7a72',
    'editorLineNumber.activeForeground': '#93a39a',
    'editorCursor.foreground': '#54b8a9',
    'editorIndentGuide.background': '#27312d',
    'editorIndentGuide.activeBackground': '#313c37',
    'editorGutter.background': '#171d1b',
  },
};

// languageForPath picks the registered language id based on the file's
// extension. Falls back to 'plaintext' when the extension is unknown so
// callers don't have to special-case it.
export function languageForPath(path: string): string {
  const lower = path.toLowerCase();
  if (lower.endsWith('.tf') || lower.endsWith('.hcl') || lower.endsWith('.tfvars')) return 'hcl';
  if (lower.endsWith('.rego')) return 'rego';
  if (lower.endsWith('.sentinel')) return 'sentinel';
  if (lower.endsWith('.json')) return 'json';
  if (lower.endsWith('.yaml') || lower.endsWith('.yml')) return 'yaml';
  if (lower.endsWith('.md')) return 'markdown';
  return 'plaintext';
}
