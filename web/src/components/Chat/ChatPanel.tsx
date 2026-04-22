import type { RefObject } from 'react';

import { S } from '../../styles';

export interface ChatMessage {
  role: string;
  text: string;
  id?: string;
}

export interface ChatPanelProps {
  messages: ChatMessage[];
  input: string;
  onInputChange: (_value: string) => void;
  onSubmit: () => void;
  loading: boolean;
  // Tool colour is used for the AI bullet glyph and the send button
  // background so the panel picks up the same accent as the rest of
  // the app for whichever tool (terraform / opentofu / ansible) is
  // active.
  toolColor: string;
  // Scroll anchor the parent pins to the bottom of the list when new
  // messages arrive; we accept it as a ref so the "scroll into view"
  // behaviour stays in one place.
  scrollAnchorRef?: RefObject<HTMLDivElement>;
  // Provider name shown in the header badge. Defaults to "Ollama".
  providerLabel?: string;
}

// Self-contained chat panel: AI message stream + input bar. No state
// lives here — the parent owns messages and handles provider calls so
// the component stays dumb and easy to swap (e.g. a different model
// backend, a streaming-over-WebSocket variant).
export function ChatPanel({
  messages,
  input,
  onInputChange,
  onSubmit,
  loading,
  toolColor,
  scrollAnchorRef,
  providerLabel = 'Ollama',
}: ChatPanelProps) {
  return (
    <div style={S.chat}>
      <div style={S.chatHead}>
        <span style={{ fontSize: 14, color: 'var(--accent-action)' }}>✦</span>
        <span>AI Assistant</span>
        <span style={S.chatBadge}>{providerLabel}</span>
      </div>
      <div style={S.chatMsgs}>
        {messages.length === 0 && (
          <div style={{ padding: '8px 0', color: '#888', fontSize: 13 }}>
            <p style={{ margin: 0 }}>Ask me to create infrastructure:</p>
            <p style={{ margin: '4px 0 0', color: '#555', fontSize: 12 }}>
              "Add a VPC" · "Create an RDS database" · "I need an S3 bucket"
            </p>
          </div>
        )}
        {messages.map((m, i) => (
          <div
            key={m.id ?? i}
            style={{
              padding: '6px 0',
              fontSize: 13,
              display: 'flex',
              gap: 8,
              color: m.role === 'ai' ? '#999' : '#ccc',
            }}
          >
            {m.role === 'ai' && (
              <span style={{ color: toolColor, fontWeight: 700, flexShrink: 0 }}>✦</span>
            )}
            <span>{m.text}</span>
          </div>
        ))}
        {loading && (
          <div style={{ padding: '6px 0', fontSize: 13, color: '#666' }}>✦ Thinking...</div>
        )}
        <div ref={scrollAnchorRef} />
      </div>
      <div style={S.chatInputRow}>
        <input
          style={S.chatInput}
          value={input}
          onChange={(e) => onInputChange(e.target.value)}
          placeholder="Describe infrastructure you need..."
          onKeyDown={(e) => e.key === 'Enter' && onSubmit()}
          disabled={loading}
        />
        <button
          style={{ ...S.chatSend, background: toolColor }}
          onClick={onSubmit}
          disabled={loading}
          aria-label="Send message"
        >
          ↑
        </button>
      </div>
    </div>
  );
}
