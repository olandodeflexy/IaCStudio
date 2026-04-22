import { useCallback, useRef } from 'react';
import Editor, { type OnMount } from '@monaco-editor/react';
import type * as monaco from 'monaco-editor';

import { cn } from '../../lib/utils';

import { languageForPath, registerLanguages, studioDarkTheme } from './languages';

export interface CodeEditorProps {
  value: string;
  // filePath drives language detection; if you pass `language` explicitly
  // it takes precedence. Either way one of them must be set.
  filePath?: string;
  language?: string;
  readOnly?: boolean;
  onChange?: (value: string) => void;
  // onSave fires when the user hits ⌘/Ctrl-S. The parent decides whether
  // to POST to /sync; we never save implicitly so unmounting the editor
  // can't silently overwrite on-disk content.
  onSave?: (value: string) => void;
  className?: string;
  height?: string | number;
}

const THEME_ID = 'iac-studio-dark';

// CodeEditor wraps @monaco-editor/react with our Monarch language
// registrations + a palette-matched dark theme. Loading Monaco lazily
// (the react wrapper handles the chunk-split) keeps the initial bundle
// small — ~200 kB for the app, Monaco arrives on first open.
export function CodeEditor({
  value,
  filePath,
  language,
  readOnly = false,
  onChange,
  onSave,
  className,
  height = '100%',
}: CodeEditorProps) {
  const saveRef = useRef(onSave);
  saveRef.current = onSave;

  const handleMount: OnMount = useCallback((editor, monacoNs) => {
    registerLanguages(monacoNs as typeof monaco);
    monacoNs.editor.defineTheme(THEME_ID, studioDarkTheme);
    monacoNs.editor.setTheme(THEME_ID);

    // ⌘/Ctrl-S → onSave. Monaco swallows the native shortcut anyway so
    // binding it here is the only way to preserve the "hit save"
    // muscle memory inside the editor.
    editor.addCommand(monacoNs.KeyMod.CtrlCmd | monacoNs.KeyCode.KeyS, () => {
      saveRef.current?.(editor.getValue());
    });
  }, []);

  const resolvedLanguage = language ?? (filePath ? languageForPath(filePath) : 'plaintext');

  return (
    <div className={cn('flex h-full w-full overflow-hidden rounded-md border border-border bg-card', className)}>
      <Editor
        height={height}
        language={resolvedLanguage}
        value={value}
        theme={THEME_ID}
        onChange={(v) => onChange?.(v ?? '')}
        options={{
          readOnly,
          minimap: { enabled: false },
          fontSize: 13,
          fontFamily: 'JetBrains Mono, ui-monospace, Menlo, monospace',
          fontLigatures: true,
          lineNumbers: 'on',
          renderLineHighlight: 'line',
          scrollBeyondLastLine: false,
          smoothScrolling: true,
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
          automaticLayout: true,
          tabSize: 2,
          insertSpaces: true,
          wordWrap: 'off',
          padding: { top: 12, bottom: 12 },
          renderWhitespace: 'selection',
        }}
        onMount={handleMount}
      />
    </div>
  );
}
