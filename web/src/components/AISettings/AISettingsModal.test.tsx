import { useState } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../../api';
import { AISettingsModal, type AISettingsConfig } from './AISettingsModal';

function renderModal({
  onClose = vi.fn(),
  onNotify = vi.fn(),
}: {
  onClose?: () => void;
  onNotify?: (_message: string, _duration?: number) => void;
} = {}) {
  function Harness() {
    const [settings, setSettings] = useState<AISettingsConfig>({
      type: 'ollama',
      endpoint: '',
      model: '',
      api_key: '',
    });

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

    fireEvent.click(screen.getByRole('button', { name: /OpenAI API/i }));

    expect(screen.getByDisplayValue('https://api.openai.com/v1')).toBeInTheDocument();
    expect(screen.getByDisplayValue('gpt-4o')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('sk-...')).toBeInTheDocument();
  });

  it('saves settings and closes the modal', async () => {
    const updateSettings = vi.spyOn(api, 'updateAISettings').mockResolvedValue({});
    const { onClose, onNotify } = renderModal();

    fireEvent.click(screen.getByRole('button', { name: /OpenAI API/i }));
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
