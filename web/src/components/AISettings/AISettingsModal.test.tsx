import { useState } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../../api';
import { AISettingsModal, type AISettingsConfig } from './AISettingsModal';

function renderModal({
  initialSettings = {
    type: 'ollama',
    endpoint: '',
    model: '',
    api_key: '',
  },
  onClose = vi.fn(),
  onNotify = vi.fn(),
}: {
  initialSettings?: AISettingsConfig;
  onClose?: () => void;
  onNotify?: (_message: string, _duration?: number) => void;
} = {}) {
  function Harness() {
    const [settings, setSettings] = useState<AISettingsConfig>(initialSettings);

    return (
      <AISettingsModal
        settings={settings}
        onSettingsChange={setSettings}
        onNotify={onNotify}
        onClose={onClose}
      />
    );
  }

  render(<Harness />);
  return { onClose, onNotify };
}

describe('AISettingsModal', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('applies provider presets to the editable fields', () => {
    renderModal();

    fireEvent.click(screen.getByRole('radio', { name: /OpenAI API/i }));

    expect(screen.getByDisplayValue('https://api.openai.com/v1')).toBeInTheDocument();
    expect(screen.getByDisplayValue('gpt-4o')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('sk-...')).toBeInTheDocument();
    expect(screen.getByRole('radiogroup', { name: /Provider Type/i })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /OpenAI API/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByText(/IaC Studio backend/i)).toHaveTextContent(/kept in process memory/i);
    expect(screen.getByText(/IaC Studio backend/i)).toHaveTextContent(/not stored on disk/i);
  });

  it('shows custom selected for an OpenAI-compatible custom endpoint', () => {
    renderModal({
      initialSettings: {
        type: 'openai',
        endpoint: 'https://llm.example.com/v1',
        model: 'gpt-compatible',
        api_key: '********',
      },
    });

    expect(screen.getByRole('radio', { name: /Custom API/i })).toHaveClass('is-active');
    expect(screen.getByRole('radio', { name: /Custom API/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: /OpenAI API/i })).not.toHaveClass('is-active');
    expect(screen.getByRole('radio', { name: /OpenAI API/i })).toHaveAttribute('aria-checked', 'false');
  });

  it('shows OpenAI selected for the default chat completions endpoint', () => {
    renderModal({
      initialSettings: {
        type: 'openai',
        endpoint: 'https://api.openai.com/v1/chat/completions',
        model: 'gpt-4o',
        api_key: '********',
      },
    });

    expect(screen.getByRole('radio', { name: /OpenAI API/i })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: /Custom API/i })).toHaveAttribute('aria-checked', 'false');
  });

  it('saves settings and closes the modal', async () => {
    const updateSettings = vi.spyOn(api, 'updateAISettings').mockResolvedValue({});
    const { onClose, onNotify } = renderModal();

    fireEvent.click(screen.getByRole('radio', { name: /OpenAI API/i }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(updateSettings).toHaveBeenCalledWith({
        type: 'openai',
        endpoint: 'https://api.openai.com/v1',
        model: 'gpt-4o',
        api_key: '',
      });
    });
    expect(onNotify).toHaveBeenCalledWith('AI settings updated');
    expect(onClose).toHaveBeenCalled();
  });
});
