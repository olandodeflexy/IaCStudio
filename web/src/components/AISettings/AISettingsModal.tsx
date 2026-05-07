import type { Dispatch, SetStateAction } from 'react';

import { api } from '../../api';
import { errorMessage } from '../../lib/errors';
import { UIButton, UIInput, UILabel, UIModal } from '../../ui';

export interface AISettingsConfig {
  type: string;
  endpoint: string;
  model: string;
  api_key: string;
}

interface ProviderOption {
  key: string;
  label: string;
  desc: string;
}

export interface AISettingsModalProps {
  settings: AISettingsConfig;
  onSettingsChange: Dispatch<SetStateAction<AISettingsConfig>>;
  onNotify: (_message: string, _duration?: number) => void;
  onClose: () => void;
}

const providers: ProviderOption[] = [
  { key: 'ollama', label: 'Ollama (Local)', desc: 'Free, private, runs on your machine' },
  { key: 'openai', label: 'OpenAI API', desc: 'GPT-4o, GPT-4-turbo' },
  { key: 'anthropic', label: 'Anthropic', desc: 'Claude Opus, Claude Haiku' },
  { key: 'custom', label: 'Custom API', desc: 'Any OpenAI-compatible endpoint' },
];

function settingsForProvider(provider: string, settings: AISettingsConfig): AISettingsConfig {
  if (provider === 'ollama') {
    return { ...settings, type: 'ollama', endpoint: 'http://localhost:11434', api_key: '' };
  }
  if (provider === 'openai') {
    return { ...settings, type: 'openai', endpoint: 'https://api.openai.com/v1', model: 'gpt-4o' };
  }
  if (provider === 'anthropic') {
    return { ...settings, type: 'anthropic', endpoint: '', model: 'claude-haiku-4-5', api_key: '' };
  }
  return { ...settings, type: 'custom' };
}

function endpointPlaceholder(provider: string) {
  if (provider === 'ollama') return 'http://localhost:11434';
  if (provider === 'anthropic') return 'https://api.anthropic.com (optional)';
  return 'https://api.openai.com/v1';
}

function modelPlaceholder(provider: string) {
  if (provider === 'ollama') return 'gemma4';
  if (provider === 'anthropic') return 'claude-haiku-4-5';
  return 'gpt-4o';
}

export function AISettingsModal({
  settings,
  onSettingsChange,
  onNotify,
  onClose,
}: AISettingsModalProps) {
  const saveSettings = async () => {
    try {
      await api.updateAISettings(settings);
      onNotify('AI settings updated');
      onClose();
    } catch (err: unknown) {
      onNotify(`Failed: ${errorMessage(err, 'Unknown error')}`, 4000);
    }
  };

  return (
    <UIModal onClose={onClose} width={480} className="ui-panel--raised">
      <div style={{ padding: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
          <span className="ui-modal-title">AI Settings</span>
          <button className="ui-close" aria-label="Close AI settings" onClick={onClose}>x</button>
        </div>

        <div style={{ marginBottom: 16 }}>
          <UILabel>Provider Type</UILabel>
          <div className="ui-choice-grid" style={{ marginTop: 8 }}>
            {providers.map(provider => (
              <button
                key={provider.key}
                className={settings.type === provider.key ? 'ui-choice-card is-active' : 'ui-choice-card'}
                onClick={() => onSettingsChange(current => settingsForProvider(provider.key, current))}
              >
                <div className="ui-choice-title">{provider.label}</div>
                <div className="ui-choice-desc">{provider.desc}</div>
              </button>
            ))}
          </div>
        </div>

        <div style={{ marginBottom: 12 }}>
          <UILabel>Endpoint</UILabel>
          <UIInput
            value={settings.endpoint}
            onChange={event => onSettingsChange(current => ({ ...current, endpoint: event.target.value }))}
            placeholder={endpointPlaceholder(settings.type)}
          />
        </div>

        <div style={{ marginBottom: 12 }}>
          <UILabel>Model</UILabel>
          <UIInput
            value={settings.model}
            onChange={event => onSettingsChange(current => ({ ...current, model: event.target.value }))}
            placeholder={modelPlaceholder(settings.type)}
          />
        </div>

        {settings.type !== 'ollama' && (
          <div style={{ marginBottom: 12 }}>
            <UILabel>API Key</UILabel>
            <UIInput
              type="password"
              value={settings.api_key}
              onChange={event => onSettingsChange(current => ({ ...current, api_key: event.target.value }))}
              placeholder="sk-..."
            />
            <div className="ui-note ui-note--small" style={{ marginTop: 4 }}>
              Your key is sent to the IaC Studio backend, kept in process memory, and used only for requests to the selected provider endpoint or your configured custom endpoint. It is not stored on disk.
            </div>
          </div>
        )}

        <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
          <UIButton block onClick={onClose}>Cancel</UIButton>
          <UIButton block variant="primary" onClick={saveSettings}>Save</UIButton>
        </div>
      </div>
    </UIModal>
  );
}
