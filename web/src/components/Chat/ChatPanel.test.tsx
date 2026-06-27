import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

import { ChatPanel } from './ChatPanel';

describe('ChatPanel', () => {
  const baseProps = {
    messages: [],
    input: '',
    onInputChange: vi.fn(),
    onSubmit: vi.fn(),
    loading: false,
    toolColor: '#2FB5A8',
  };

  it('renders the empty-state hint when no messages are present', () => {
    render(<ChatPanel {...baseProps} />);
    expect(screen.getByText('Agent Hub')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Codex' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Claude Code' })).toBeInTheDocument();
    expect(screen.getByText('Read-only default')).toBeInTheDocument();
    expect(screen.getByText('No secret prompts')).toBeInTheDocument();
    expect(screen.getByText(/Ask me to create infrastructure/)).toBeInTheDocument();
  });

  it('renders messages in order, with an AI bullet only on AI turns', () => {
    render(
      <ChatPanel
        {...baseProps}
        messages={[
          { role: 'user', text: 'add a vpc' },
          { role: 'ai', text: 'on it' },
        ]}
      />,
    );
    expect(screen.getByText('add a vpc')).toBeInTheDocument();
    expect(screen.getByText('on it')).toBeInTheDocument();
  });

  it('submits via Enter in the input', () => {
    const onSubmit = vi.fn();
    render(<ChatPanel {...baseProps} onSubmit={onSubmit} input="hello" />);
    fireEvent.keyDown(screen.getByPlaceholderText(/Describe infrastructure/), { key: 'Enter' });
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('disables send + input while loading', () => {
    render(<ChatPanel {...baseProps} loading />);
    const input = screen.getByPlaceholderText(/Describe infrastructure/);
    expect(input).toBeDisabled();
    expect(screen.getByLabelText('Send message')).toBeDisabled();
    expect(screen.getByText('✦ Thinking...')).toBeInTheDocument();
  });

  it('shows the Codex provider lane without requiring API keys by default', () => {
    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));

    expect(screen.getAllByText('Codex CLI').length).toBeGreaterThan(0);
    expect(screen.getByText('OpenAI API')).toBeInTheDocument();
    expect(screen.getByText(/official CLI session/)).toBeInTheDocument();
    expect(screen.getByText(/Platform API account/)).toBeInTheDocument();
  });

  it('keeps local model support visible as a first-class lane', () => {
    render(<ChatPanel {...baseProps} providerLabel="Ollama" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Local' }));

    expect(screen.getAllByText('Ollama').length).toBeGreaterThan(0);
    expect(screen.getByText('LM Studio / vLLM')).toBeInTheDocument();
    expect(screen.getByText(/without cloud egress/)).toBeInTheDocument();
  });
});
