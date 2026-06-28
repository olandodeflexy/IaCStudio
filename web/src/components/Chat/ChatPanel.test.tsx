import { createRef } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';

import { ChatPanel } from './ChatPanel';

const listLocalAgentProvidersMock = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  api: {
    listLocalAgentProviders: listLocalAgentProvidersMock,
  },
}));

describe('ChatPanel', () => {
  const baseProps = {
    messages: [],
    input: '',
    onInputChange: vi.fn(),
    onSubmit: vi.fn(),
    loading: false,
    toolColor: '#2FB5A8',
  };

  beforeEach(() => {
    listLocalAgentProvidersMock.mockReset();
    listLocalAgentProvidersMock.mockReturnValue(new Promise(() => {}));
    window.localStorage.clear();
  });

  it('renders the empty-state hint when no messages are present', () => {
    render(<ChatPanel {...baseProps} />);
    expect(screen.getByText('Agent Hub')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Codex' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Claude Code' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Gemini' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Copilot' })).toBeInTheDocument();
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

  it('shows detected local provider status in provider lanes', async () => {
    listLocalAgentProvidersMock.mockResolvedValueOnce([{
      id: 'codex',
      name: 'Codex CLI',
      category: 'local_agent',
      state: 'available',
      installed: true,
      command: 'codex',
      entrypoint: 'codex',
      candidates: ['codex'],
      version: 'unknown',
      capabilities: ['chat', 'local_cli'],
      credential_mode: 'external_login',
      auth_hint: 'Use the official local Codex sign-in.',
    }]);

    render(<ChatPanel {...baseProps} />);
    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const codexPanel = screen.getByRole('tabpanel', { name: 'Codex' });

    await waitFor(() => {
      expect(within(codexPanel).getByText('Detected: codex')).toBeInTheDocument();
    });
    expect(within(codexPanel).getByText('External login')).toBeInTheDocument();
    expect(within(codexPanel).getByText('Version unknown')).toBeInTheDocument();
    expect(within(codexPanel).getByText('local cli')).toBeInTheDocument();
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

  it('persists the selected provider tab and task mode locally', () => {
    const firstRender = render(<ChatPanel {...baseProps} />);

    fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }));
    fireEvent.click(screen.getByRole('button', { name: 'Generate IaC' }));

    expect(window.localStorage.getItem('iac-studio.agentHub.activeTab')).toBe('gemini');
    expect(window.localStorage.getItem('iac-studio.agentHub.activeTask')).toBe('Generate IaC');

    firstRender.unmount();
    render(<ChatPanel {...baseProps} />);

    expect(screen.getByRole('tab', { name: 'Gemini' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Generate IaC' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('falls back to defaults when persisted provider selection is invalid', () => {
    window.localStorage.setItem('iac-studio.agentHub.activeTab', 'missing-provider');
    window.localStorage.setItem('iac-studio.agentHub.activeTask', 'Unsupported task');

    render(<ChatPanel {...baseProps} />);

    expect(screen.getByRole('tab', { name: 'Chat' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Review project' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps the UI usable when localStorage reads are blocked', () => {
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('localStorage read blocked');
    });

    try {
      render(<ChatPanel {...baseProps} />);

      expect(screen.getByRole('tab', { name: 'Chat' })).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByRole('button', { name: 'Review project' })).toHaveAttribute('aria-pressed', 'true');
    } finally {
      getItemSpy.mockRestore();
    }
  });

  it('keeps provider and task selection usable when localStorage writes are blocked', () => {
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('localStorage write blocked');
    });

    try {
      render(<ChatPanel {...baseProps} />);

      expect(() => fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }))).not.toThrow();
      expect(() => fireEvent.click(screen.getByRole('button', { name: 'Generate IaC' }))).not.toThrow();
      expect(screen.getByRole('tab', { name: 'Gemini' })).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByRole('button', { name: 'Generate IaC' })).toHaveAttribute('aria-pressed', 'true');
    } finally {
      setItemSpy.mockRestore();
    }
  });

  it('keeps local model support visible as a first-class lane', () => {
    render(<ChatPanel {...baseProps} providerLabel="Ollama" />);
    fireEvent.click(screen.getByRole('tab', { name: 'Local' }));

    const localPanel = screen.getByRole('tabpanel', { name: 'Local' });
    expect(within(localPanel).getByText('Ollama')).toBeInTheDocument();
    expect(within(localPanel).getByText('LM Studio / vLLM')).toBeInTheDocument();
    expect(within(localPanel).getByText(/without cloud egress/)).toBeInTheDocument();
  });

  it('shows Gemini and Copilot as first-class assistant lanes', () => {
    render(<ChatPanel {...baseProps} />);

    fireEvent.click(screen.getByRole('tab', { name: 'Gemini' }));
    const geminiPanel = screen.getByRole('tabpanel', { name: 'Gemini' });
    expect(within(geminiPanel).getByText('Gemini CLI')).toBeInTheDocument();
    expect(within(geminiPanel).getByText('Gemini API')).toBeInTheDocument();
    expect(within(geminiPanel).getByText(/local Gemini session/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Copilot' }));
    const copilotPanel = screen.getByRole('tabpanel', { name: 'Copilot' });
    expect(within(copilotPanel).getByText('GitHub Copilot CLI')).toBeInTheDocument();
    expect(within(copilotPanel).getByText('Copilot coding agent')).toBeInTheDocument();
    expect(within(copilotPanel).getByText(/GitHub auth session/)).toBeInTheDocument();
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
