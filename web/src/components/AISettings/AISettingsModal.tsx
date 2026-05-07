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

const OPENAI_DEFAULT_ENDPOINT = 'https://api.openai.com/v1';

function normalizeEndpoint(endpoint: string) {
  return endpoint.trim().replace(/\/+$/, '');
}

function normalizeOpenAIEndpoint(endpoint: string) {
  const normalized = normalizeEndpoint(endpoint);
  if (normalized.endsWith('/chat/completions')) {
    return normalized.slice(0, -'/chat/completions'.length);
  }
  return normalized;
}

function selectedProvider(settings: AISettingsConfig) {
  if (settings.type === 'openai') {
    const endpoint = normalizeOpenAIEndpoint(settings.endpoint);
    return endpoint && endpoint !== OPENAI_DEFAULT_ENDPOINT ? 'custom' : 'openai';
  }
  return settings.type;
}

function settingsForProvider(provider: string, settings: AISettingsConfig): AISettingsConfig {
  if (provider === 'ollama') {
    return { ...settings, type: 'ollama', endpoint: 'http://localhost:11434', api_key: '' };
  }
  if (provider === 'openai') {
    return { ...settings, type: 'openai', endpoint: OPENAI_DEFAULT_ENDPOINT, model: 'gpt-4o' };
  }
  if (provider === 'anthropic') {
    return { ...settings, type: 'anthropic', endpoint: '', model: 'claude-haiku-4-5', api_key: '' };
  }
  return { ...settings, type: 'custom' };
}

function endpointPlaceholder(provider: string) {
  if (provider === 'ollama') return 'http://localhost:11434';
  if (provider === 'anthropic') return 'https://api.anthropic.com (optional)';
  return OPENAI_DEFAULT_ENDPOINT;
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
  const selectedProviderKey = selectedProvider(settings);

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

        <fieldset style={{ margin: '0 0 16px', padding: 0, border: 0 }}>
          <legend className="ui-label">Provider Type</legend>
          <div className="ui-choice-grid" style={{ marginTop: 8 }}>
            {providers.map(provider => {
              const isSelected = selectedProviderKey === provider.key;
              return (
                <label
                  key={provider.key}
                  className={isSelected ? 'ui-choice-card ai-provider-choice is-active' : 'ui-choice-card ai-provider-choice'}
                >
                  <input
                    type="radio"
                    name="ai-provider-type"
                    value={provider.key}
                    checked={isSelected}
                    onChange={() => onSettingsChange(current => settingsForProvider(provider.key, current))}
                  />
                  <span>
                    <span className="ui-choice-title">{provider.label}</span>
                    <span className="ui-choice-desc">{provider.desc}</span>
                  </span>
                </label>
              );
            })}
          </div>
        </fieldset>

        <div style={{ marginBottom: 12 }}>
          <UILabel>Endpoint</UILabel>
          <UIInput
            value={settings.endpoint}
            onChange={event => onSettingsChange(current => ({ ...current, endpoint: event.target.value }))}
            placeholder={endpointPlaceholder(selectedProviderKey)}
          />
        </div>

        <div style={{ marginBottom: 12 }}>
          <UILabel>Model</UILabel>
          <UIInput
            value={settings.model}
            onChange={event => onSettingsChange(current => ({ ...current, model: event.target.value }))}
            placeholder={modelPlaceholder(selectedProviderKey)}
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
