import { useEffect, useMemo, useState } from 'react';
import { CheckCircle2, CloudCog, PlugZap, Trash2, XCircle } from 'lucide-react';

import {
  api,
  type CloudAuthMethod,
  type CloudConnection,
  type CloudConnectionInput,
  type CloudSecretStore,
  type CloudConnectionTestResult,
  type CloudProvider,
} from '../../api';
import { Button } from '../ui/button';

type CloudClient = Pick<
  typeof api,
  'listCloudConnections' |
  'createCloudConnection' |
  'updateCloudConnection' |
  'deleteCloudConnection' |
  'testCloudConnection'
>;

export interface CloudConnectionsPanelProps {
  client?: CloudClient;
  selectedConnectionId?: string | null;
  onConnectionSelected?: (_connection: CloudConnection | null) => void;
}

const providerMethods: Record<CloudProvider, { key: CloudAuthMethod; label: string }[]> = {
  aws: [
    { key: 'aws_profile', label: 'AWS profile' },
    { key: 'aws_sso', label: 'AWS SSO profile' },
    { key: 'aws_static', label: 'Static key fallback' },
  ],
  azure: [
    { key: 'azure_cli', label: 'Azure CLI' },
    { key: 'azure_service_principal', label: 'Service principal' },
  ],
  gcp: [
    { key: 'gcp_gcloud', label: 'gcloud auth' },
    { key: 'gcp_service_account', label: 'Service account JSON' },
  ],
};

const metadataFields: Record<CloudAuthMethod, { key: string; label: string; secret?: boolean; multiline?: boolean }[]> = {
  aws_profile: [
    { key: 'profile', label: 'Profile' },
    { key: 'account_id', label: 'Account ID' },
    { key: 'role_arn', label: 'Role ARN' },
  ],
  aws_sso: [
    { key: 'profile', label: 'SSO profile' },
    { key: 'account_id', label: 'Account ID' },
    { key: 'role_arn', label: 'Role ARN' },
  ],
  aws_static: [
    { key: 'access_key_id', label: 'Access key ID' },
    { key: 'secret_access_key', label: 'Secret access key', secret: true },
    { key: 'session_token', label: 'Session token', secret: true, multiline: true },
    { key: 'account_id', label: 'Account ID' },
    { key: 'role_arn', label: 'Role ARN' },
  ],
  azure_cli: [
    { key: 'subscription_id', label: 'Subscription ID' },
    { key: 'tenant_id', label: 'Tenant ID' },
  ],
  azure_service_principal: [
    { key: 'tenant_id', label: 'Tenant ID' },
    { key: 'subscription_id', label: 'Subscription ID' },
    { key: 'client_id', label: 'Client ID' },
    { key: 'client_secret', label: 'Client secret', secret: true },
  ],
  gcp_gcloud: [
    { key: 'project_id', label: 'Project ID' },
  ],
  gcp_service_account: [
    { key: 'project_id', label: 'Project ID' },
    { key: 'service_account_json', label: 'Service account JSON', secret: true, multiline: true },
  ],
};

const defaultInput = (): CloudConnectionInput => ({
  name: '',
  provider: 'aws',
  auth_method: 'aws_profile',
  region: '',
  metadata: { profile: 'default' },
  secrets: {},
});

const secretStoreLabels: Record<string, string> = {
  local_encrypted: 'Local encrypted',
  os_keychain: 'OS keychain',
  vault: 'Vault',
  aws_secrets_manager: 'AWS Secrets Manager',
  aws_ssm_parameter_store: 'AWS SSM Parameter Store',
  azure_key_vault: 'Azure Key Vault',
  gcp_secret_manager: 'GCP Secret Manager',
};

function secretStoreLabel(store?: CloudSecretStore) {
  const value = store?.trim();
  if (!value) return 'No stored secrets';
  return (
    secretStoreLabels[value]
    || value
      .split(/[_-]+/)
      .filter(Boolean)
      .map(part => part.charAt(0).toUpperCase() + part.slice(1))
      .join(' ')
  );
}

function secretStoreBadgeClass(store?: CloudSecretStore) {
  const value = store?.trim();
  if (!value) return 'border-border bg-muted/30 text-muted-foreground';
  if (value === 'local_encrypted') return 'border-amber-500/40 bg-amber-500/10 text-amber-100';
  return 'border-primary/40 bg-primary/10 text-primary';
}

function secretStoreMessage(store?: CloudSecretStore) {
  const value = store?.trim();
  if (!value || value === 'local_encrypted') {
    return 'Secrets are encrypted and stored locally on this machine in the IaC Studio projects directory. Use scoped, revocable credentials.';
  }
  return 'Secrets are stored using this connection’s configured secret store. Use scoped, revocable credentials.';
}

function SecretStoreBadge({ store }: { store?: CloudSecretStore }) {
  const label = secretStoreLabel(store);
  return (
    <span
      className={`max-w-44 shrink-0 truncate rounded border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${secretStoreBadgeClass(store)}`}
      title={label}
    >
      {label}
    </span>
  );
}

function connectionSecretStore(
  connection?: Pick<CloudConnection, 'secret_fields' | 'secret_store'>,
): CloudSecretStore | undefined {
  return connection?.secret_store || (connection?.secret_fields?.length ? 'local_encrypted' : undefined);
}

function splitFields(input: CloudConnectionInput) {
  const metadata: Record<string, string> = {};
  const secrets: Record<string, string> = {};
  for (const field of metadataFields[input.auth_method]) {
    const value = field.secret ? input.secrets?.[field.key] : input.metadata?.[field.key];
    if (value?.trim()) {
      if (field.secret) secrets[field.key] = value.trim();
      else metadata[field.key] = value.trim();
    }
  }
  return { metadata, secrets };
}

function fromConnection(connection: CloudConnection): CloudConnectionInput {
  return {
    id: connection.id,
    name: connection.name,
    provider: connection.provider,
    auth_method: connection.auth_method,
    region: connection.region || '',
    metadata: { ...(connection.metadata || {}) },
    secrets: {},
  };
}

export function CloudConnectionsPanel({
  client = api,
  selectedConnectionId = null,
  onConnectionSelected,
}: CloudConnectionsPanelProps) {
  const [connections, setConnections] = useState<CloudConnection[]>([]);
  const [form, setForm] = useState<CloudConnectionInput>(defaultInput);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testingId, setTestingId] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<CloudConnectionTestResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  const selected = useMemo(
    () => connections.find(connection => connection.id === form.id),
    [connections, form.id],
  );
  const formSecretStore = connectionSecretStore(selected)
    || (Object.keys(form.secrets || {}).length > 0 ? 'local_encrypted' : undefined);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      setConnections(await client.listCloudConnections());
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  useEffect(() => {
    if (!selectedConnectionId || connections.length === 0) return;
    if (!connections.some(connection => connection.id === selectedConnectionId)) {
      onConnectionSelected?.(null);
    }
  }, [connections, onConnectionSelected, selectedConnectionId]);

  const updateField = (key: string, value: string, secret?: boolean) => {
    setForm(current => ({
      ...current,
      [secret ? 'secrets' : 'metadata']: {
        ...(secret ? current.secrets : current.metadata),
        [key]: value,
      },
    }));
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    const { metadata, secrets } = splitFields(form);
    const payload: CloudConnectionInput = {
      ...form,
      region: form.region?.trim(),
      metadata,
      secrets,
    };
    try {
      const saved = form.id
        ? await client.updateCloudConnection(form.id, payload)
        : await client.createCloudConnection(payload);
      setForm(fromConnection(saved));
      if (selectedConnectionId === saved.id) onConnectionSelected?.(saved);
      setTestResult(null);
      await load();
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    setError(null);
    try {
      await client.deleteCloudConnection(id);
      if (form.id === id) setForm(defaultInput());
      if (testResult?.connection.id === id) setTestResult(null);
      await load();
    } catch (err) {
      setError(String(err));
    }
  };

  const test = async (id: string) => {
    setTestingId(id);
    setError(null);
    try {
      setTestResult(await client.testCloudConnection(id));
    } catch (err) {
      setError(String(err));
    } finally {
      setTestingId(null);
    }
  };

  return (
    <div className="flex h-full flex-col gap-3 bg-background p-4">
      <header className="flex items-center gap-3">
        <CloudCog className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Cloud Connections
        </h2>
        <Button size="sm" variant="outline" className="ml-auto" onClick={() => setForm(defaultInput())}>
          New
        </Button>
      </header>

      {selectedConnectionId && (
        <div className="rounded-md border border-primary/40 bg-primary/10 px-3 py-2 text-xs text-foreground">
          Run target: {connections.find(connection => connection.id === selectedConnectionId)?.name || 'selected connection'}
        </div>
      )}

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}

      <section className="grid gap-2">
        <div className="flex flex-col gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-100 sm:flex-row sm:items-start">
          <span className="min-w-0 flex-1">
            {secretStoreMessage(formSecretStore)}
          </span>
          <div className="self-start">
            <SecretStoreBadge store={formSecretStore} />
          </div>
        </div>

        <label className="grid gap-1 text-xs text-muted-foreground">
          Name
          <input
            className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-primary"
            value={form.name}
            onChange={event => setForm(current => ({ ...current, name: event.target.value }))}
            placeholder="prod-admin"
          />
        </label>

        <div className="grid grid-cols-2 gap-2">
          <label className="grid gap-1 text-xs text-muted-foreground">
            Provider
            <select
              className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-primary"
              value={form.provider}
              onChange={event => {
                const provider = event.target.value as CloudProvider;
                setForm(current => ({
                  ...current,
                  id: undefined,
                  provider,
                  auth_method: providerMethods[provider][0].key,
                  metadata: {},
                  secrets: {},
                }));
              }}
            >
              <option value="aws">AWS</option>
              <option value="azure">Azure</option>
              <option value="gcp">GCP</option>
            </select>
          </label>

          <label className="grid gap-1 text-xs text-muted-foreground">
            Auth
            <select
              className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-primary"
              value={form.auth_method}
              onChange={event => setForm(current => ({ ...current, auth_method: event.target.value as CloudAuthMethod, metadata: {}, secrets: {} }))}
            >
              {providerMethods[form.provider].map(method => (
                <option key={method.key} value={method.key}>{method.label}</option>
              ))}
            </select>
          </label>
        </div>

        <label className="grid gap-1 text-xs text-muted-foreground">
          Region
          <input
            className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-primary"
            value={form.region || ''}
            onChange={event => setForm(current => ({ ...current, region: event.target.value }))}
            placeholder={form.provider === 'aws' ? 'us-east-1' : form.provider === 'azure' ? 'eastus' : 'us-central1'}
          />
        </label>

        {metadataFields[form.auth_method].map(field => {
          const value = field.secret ? form.secrets?.[field.key] || '' : form.metadata?.[field.key] || '';
          const stored = selected?.secret_fields?.includes(field.key);
          const className = 'rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-primary';
          return (
            <label key={field.key} className="grid gap-1 text-xs text-muted-foreground">
              {field.label}
              {field.multiline ? (
                <textarea
                  className={`${className} min-h-20 resize-none font-mono text-xs`}
                  value={value}
                  onChange={event => updateField(field.key, event.target.value, field.secret)}
                  placeholder={stored ? 'Stored secret; leave blank to keep it' : undefined}
                />
              ) : (
                <input
                  className={className}
                  type={field.secret ? 'password' : 'text'}
                  value={value}
                  onChange={event => updateField(field.key, event.target.value, field.secret)}
                  placeholder={stored ? 'Stored secret; leave blank to keep it' : undefined}
                />
              )}
            </label>
          );
        })}

        <Button onClick={save} disabled={saving || !form.name.trim()}>
          {saving ? 'Saving...' : form.id ? 'Update connection' : 'Save connection'}
        </Button>
      </section>

      <section className="min-h-0 flex-1 overflow-y-auto border-t border-border pt-3">
        <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
          <span>{loading ? 'Loading...' : `${connections.length} connection${connections.length === 1 ? '' : 's'}`}</span>
        </div>
        <div className="grid gap-2">
          {connections.map(connection => (
            <div key={connection.id} className="rounded-md border border-border bg-card p-3">
              <button
                type="button"
                className="mb-2 flex w-full items-center gap-2 text-left"
                onClick={() => {
                  setForm(fromConnection(connection));
                  setTestResult(null);
                }}
              >
                <PlugZap className="h-3.5 w-3.5 text-primary" />
                <span className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">{connection.name}</span>
                <span className="rounded bg-primary/10 px-2 py-0.5 font-mono text-[10px] uppercase text-primary">{connection.provider}</span>
              </button>
              <div className="mb-3 flex items-center gap-2">
                <div className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
                  {connection.auth_method}{connection.region ? ` / ${connection.region}` : ''}
                </div>
                <SecretStoreBadge store={connectionSecretStore(connection)} />
              </div>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant={selectedConnectionId === connection.id ? 'default' : 'outline'}
                  onClick={() => onConnectionSelected?.(connection)}
                  disabled={!onConnectionSelected || selectedConnectionId === connection.id}
                >
                  {selectedConnectionId === connection.id ? 'Selected' : 'Use for runs'}
                </Button>
                <Button size="sm" variant="outline" onClick={() => test(connection.id)} disabled={testingId === connection.id}>
                  {testingId === connection.id ? 'Testing...' : 'Test'}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    if (selectedConnectionId === connection.id) onConnectionSelected?.(null);
                    remove(connection.id);
                  }}
                  aria-label={`Delete ${connection.name}`}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      </section>

      {testResult && (
        <section className={`rounded-md border px-3 py-2 text-xs ${testResult.ok ? 'border-primary/40 bg-primary/10' : 'border-destructive/50 bg-destructive/10'}`}>
          <div className="mb-2 flex items-center gap-2 font-semibold text-foreground">
            {testResult.ok ? (
              <CheckCircle2 className="h-3.5 w-3.5 text-primary" aria-label="Connection test passed" />
            ) : (
              <XCircle className="h-3.5 w-3.5 text-destructive" aria-label="Connection test failed" />
            )}
            {testResult.summary}
          </div>
          <div className="grid gap-1 font-mono text-[11px]">
            {testResult.checks.map(check => (
              <div key={check.name} className={check.status === 'error' ? 'text-destructive' : 'text-muted-foreground'}>
                {check.status.toUpperCase()} {check.name}: {check.message}
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
