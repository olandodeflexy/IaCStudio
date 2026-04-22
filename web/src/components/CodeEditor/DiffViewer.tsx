import { useCallback, useEffect, useRef } from 'react';
import { DiffEditor, type DiffOnMount } from '@monaco-editor/react';
import type * as monaco from 'monaco-editor';

import { cn } from '../../lib/utils';

import { languageForPath, registerLanguages, studioDarkTheme } from './languages';

export interface DiffViewerProps {
  original: string;
  modified: string;
  filePath?: string;
  language?: string;
  // When true, the right pane is editable — handy for "accept AI
  // patch" flows where the user may tweak the proposed text before
  // applying it. Default false.
  allowEdits?: boolean;
  onModifiedChange?: (value: string) => void;
  className?: string;
  height?: string | number;
}

const THEME_ID = 'iac-studio-dark';

// DiffViewer renders Monaco's side-by-side diff editor. Used for three
// flows today: plan preview (current HCL vs. planned change), drift
// resolution (committed HCL vs. live state), and AI patches (current
// vs. proposed by the model).
export function DiffViewer({
  original,
  modified,
  filePath,
  language,
  allowEdits = false,
  onModifiedChange,
  className,
  height = '100%',
}: DiffViewerProps) {
  // Disposable returned by onDidChangeModelContent. We stash it on a
  // ref so the effect teardown can dispose it when the component
  // unmounts or the change-callback identity changes — otherwise the
  // listener leaks for the life of the page and a stale `onModified-
  // Change` closure keeps firing against old parent state.
  const changeSubRef = useRef<{ dispose: () => void } | null>(null);
  const onChangeRef = useRef(onModifiedChange);
  onChangeRef.current = onModifiedChange;

  const handleMount: DiffOnMount = useCallback(
    (editor, monacoNs) => {
      registerLanguages(monacoNs as typeof monaco);
      monacoNs.editor.defineTheme(THEME_ID, studioDarkTheme);
      monacoNs.editor.setTheme(THEME_ID);

      // Only subscribe when the user can actually edit — otherwise the
      // callback fires for programmatic prop updates too and creates
      // feedback loops in controlled usages.
      if (allowEdits && onChangeRef.current) {
        const modifiedEditor = editor.getModifiedEditor();
        changeSubRef.current = modifiedEditor.onDidChangeModelContent(() => {
          onChangeRef.current?.(modifiedEditor.getValue());
        });
      }
    },
    [allowEdits],
  );

  useEffect(() => {
    return () => {
      changeSubRef.current?.dispose();
      changeSubRef.current = null;
    };
  }, []);

  const resolvedLanguage = language ?? (filePath ? languageForPath(filePath) : 'plaintext');

  return (
    <div className={cn('flex h-full w-full overflow-hidden rounded-md border border-border bg-card', className)}>
      <DiffEditor
        height={height}
        language={resolvedLanguage}
        original={original}
        modified={modified}
        theme={THEME_ID}
        options={{
          readOnly: !allowEdits,
          originalEditable: false,
          minimap: { enabled: false },
          fontSize: 13,
          fontFamily: 'JetBrains Mono, ui-monospace, Menlo, monospace',
          fontLigatures: true,
          renderSideBySide: true,
          scrollBeyondLastLine: false,
          automaticLayout: true,
        }}
        onMount={handleMount}
      />
    </div>
  );
}
