import { useCallback } from 'react';
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
  const handleMount: DiffOnMount = useCallback(
    (editor, monacoNs) => {
      registerLanguages(monacoNs as typeof monaco);
      monacoNs.editor.defineTheme(THEME_ID, studioDarkTheme);
      monacoNs.editor.setTheme(THEME_ID);

      if (onModifiedChange) {
        // The diff editor has a "modified" model we listen on; changes
        // emit regardless of whether the edit happened by keystroke or
        // programmatic setValue, so we rely on allowEdits to gate
        // whether the callback fires meaningfully.
        const modifiedEditor = editor.getModifiedEditor();
        modifiedEditor.onDidChangeModelContent(() => {
          onModifiedChange(modifiedEditor.getValue());
        });
      }
    },
    [onModifiedChange],
  );

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
