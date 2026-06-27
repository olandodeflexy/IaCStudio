import { createRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';

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

    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });
    expect(within(codexPanel).getByText('Codex CLI')).toBeInTheDocument();
    expect(within(codexPanel).getByText('OpenAI API')).toBeInTheDocument();
    expect(within(codexPanel).getByText(/official CLI session/)).toBeInTheDocument();
    expect(within(codexPanel).getByText(/Platform API account/)).toBeInTheDocument();
  });

  it('links provider tabs to stable tabpanels and supports roving keyboard selection', () => {
    render(<ChatPanel {...baseProps} />);

    const chatTab = screen.getByRole('tab', { name: 'Chat' });
    const codexTab = screen.getByRole('tab', { name: 'Codex' });
    const tabList = screen.getByRole('tablist', { name: 'Agent Hub providers' });
    expect(tabList).toHaveAttribute('aria-orientation', 'vertical');
    expect(chatTab).toHaveAttribute('id', 'agent-hub-tab-chat');
    expect(chatTab).toHaveAttribute('aria-controls', 'agent-hub-panel-chat');
    expect(codexTab).toHaveAttribute('aria-controls', 'agent-hub-panel-codex');
    expect(chatTab).toHaveAttribute('tabindex', '0');
    expect(codexTab).toHaveAttribute('tabindex', '-1');
    expect(document.getElementById('agent-hub-panel-codex')).toHaveAttribute('role', 'tabpanel');
    expect(screen.getByRole('tabpanel', { name: 'Chat' })).toHaveAttribute('id', 'agent-hub-panel-chat');

    fireEvent.keyDown(chatTab, { key: 'ArrowDown' });

    expect(codexTab).toHaveAttribute('aria-selected', 'true');
    expect(codexTab).toHaveAttribute('tabindex', '0');
    expect(screen.getByRole('tabpanel', { name: 'Codex' })).toHaveAttribute('id', 'agent-hub-panel-codex');
  });

  it('exposes task mode selection to assistive technology', () => {
    render(<ChatPanel {...baseProps} />);

    const taskModes = screen.getByRole('group', { name: 'Agent task modes' });
    const reviewProject = within(taskModes).getByRole('button', { name: 'Review project' });
    const generateIac = within(taskModes).getByRole('button', { name: 'Generate IaC' });
    expect(reviewProject).toHaveAttribute('aria-pressed', 'true');
    expect(generateIac).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(generateIac);

    expect(reviewProject).toHaveAttribute('aria-pressed', 'false');
    expect(generateIac).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps local model support visible as a first-class lane', () => {
    render(<ChatPanel {...baseProps} providerLabel="Ollama" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Local' }));

    const localPanel = screen.getByRole('tabpanel', { name: 'Local' });
    expect(within(localPanel).getByText('Ollama')).toBeInTheDocument();
    expect(within(localPanel).getByText('LM Studio / vLLM')).toBeInTheDocument();
    expect(within(localPanel).getByText(/without cloud egress/)).toBeInTheDocument();
  });

  it('scrolls to the newest chat message when returning to the Chat tab', () => {
    const scrollAnchorRef = createRef<HTMLDivElement>();
    render(<ChatPanel {...baseProps} scrollAnchorRef={scrollAnchorRef} />);
    const scrollIntoView = vi.fn();
    Object.defineProperty(scrollAnchorRef.current, 'scrollIntoView', {
      configurable: true,
      value: scrollIntoView,
    });

    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Chat' }));

    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'nearest' });
  });
});
