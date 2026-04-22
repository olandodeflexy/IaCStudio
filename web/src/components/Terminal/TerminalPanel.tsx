import { S } from '../../styles';

export interface TerminalPanelProps {
  lines: string[];
  onClear: () => void;
  // When a recent command failed, the parent populates these so the
  // panel can render the "Fix with AI" affordance. Click → onFix,
  // which the parent wires up to the /api/ai/fix flow.
  lastError?: { command: string; output: string } | null;
  onFix?: () => void;
  fixLoading?: boolean;
  // Accent colour for command lines (prefixed with $) — matches the
  // currently selected tool so the terminal feels of-a-piece with the
  // rest of the UI.
  toolColor: string;
}

// Per-line colour rules. Split out so they're easy to tweak and the
// render loop stays terse.
function lineColour(line: string, toolColor: string): string {
  if (line.startsWith('✓') || line.includes('Apply complete')) return '#4ade80';
  if (line.startsWith('$')) return toolColor;
  if (line.startsWith('  +')) return '#60a5fa';
  if (line.startsWith('✦')) return '#a78bfa';
  if (line.startsWith('Error') || line.startsWith('ERROR')) return '#ef4444';
  return '#999';
}

// Thin terminal output pane. Command execution, WebSocket wiring, and
// the "fix with AI" analyze-plan call all live in the parent — this
// component only renders lines and exposes the affordances. Keeping it
// dumb means the tests can exercise the "failure → fix button" state
// transition by just changing props.
export function TerminalPanel({
  lines,
  onClear,
  lastError,
  onFix,
  fixLoading,
  toolColor,
}: TerminalPanelProps) {
  return (
    <div style={S.term}>
      <div style={S.termHead}>
        <span>Terminal</span>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          {lastError && onFix && (
            <button
              style={{
                background: '#ef444422',
                border: '1px solid #ef444444',
                borderRadius: 6,
                padding: '3px 10px',
                color: '#ef4444',
                fontSize: 10,
                cursor: 'pointer',
                fontFamily: 'JetBrains Mono',
                fontWeight: 600,
              }}
              disabled={fixLoading}
              onClick={onFix}
            >
              {fixLoading ? '✦ Analyzing...' : '✦ Fix with AI'}
            </button>
          )}
          <button style={S.termClear} onClick={onClear}>
            Clear
          </button>
        </div>
      </div>
      <div style={S.termContent}>
        {lines.length === 0 && (
          <span style={{ color: '#444' }}>Run init, plan, or apply to see output...</span>
        )}
        {lines.map((line, i) => (
          <div key={i} style={{ color: lineColour(line, toolColor) }}>
            {line || ' '}
          </div>
        ))}
      </div>
    </div>
  );
}
