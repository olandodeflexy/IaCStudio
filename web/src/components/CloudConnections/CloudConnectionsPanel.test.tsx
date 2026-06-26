import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { CloudConnection, CloudConnectionInput, CloudConnectionTestResult } from '../../api';
import { CloudConnectionsPanel } from './CloudConnectionsPanel';

function makeClient(initial: CloudConnection[] = []) {
  let connections = [...initial];
  return {
    listCloudConnections: vi.fn(async () => connections),
    createCloudConnection: vi.fn(async (input: CloudConnectionInput) => {
      const connection: CloudConnection = {
        id: 'conn_1',
        name: input.name,
        provider: input.provider,
        auth_method: input.auth_method,
        region: input.region,
        metadata: input.metadata,
        secret_fields: input.secrets && Object.keys(input.secrets).length > 0 ? Object.keys(input.secrets) : undefined,
      };
      connections = [connection];
      return connection;
    }),
    updateCloudConnection: vi.fn(async (id: string, input: CloudConnectionInput) => {
      const connection: CloudConnection = {
        id,
        name: input.name,
        provider: input.provider,
        auth_method: input.auth_method,
        region: input.region,
        metadata: input.metadata,
        secret_fields: initial.find(item => item.id === id)?.secret_fields,
      };
      connections = connections.map(item => item.id === id ? connection : item);
      return connection;
    }),
    deleteCloudConnection: vi.fn(async (id: string) => {
      connections = connections.filter(item => item.id !== id);
      return { status: 'deleted' };
    }),
    testCloudConnection: vi.fn(async (id: string): Promise<CloudConnectionTestResult> => ({
      ok: false,
      summary: 'Connection is missing required fields before it can be used.',
      connection: connections.find(item => item.id === id)!,
      checks: [
        { name: 'client_secret', status: 'error', message: 'client_secret is required for azure_service_principal' },
      ],
    })),
  };
}

describe('CloudConnectionsPanel', () => {
  it('shows the local secret storage warning', async () => {
    const client = makeClient();
    render(<CloudConnectionsPanel client={client} />);

    expect(screen.getByText(/Secrets are encrypted and stored locally on this machine/)).toBeInTheDocument();
    await screen.findByText('0 connections');
  });

  it('creates an AWS profile connection and refreshes the list', async () => {
    const client = makeClient();
    render(<CloudConnectionsPanel client={client} />);

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'prod-admin' } });
    fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'us-east-1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save connection' }));

    await waitFor(() => {
      expect(client.createCloudConnection).toHaveBeenCalledWith(expect.objectContaining({
        name: 'prod-admin',
        provider: 'aws',
        auth_method: 'aws_profile',
        region: 'us-east-1',
        metadata: { profile: 'default' },
        secrets: {},
      }));
    });
    await waitFor(() => {
      expect(screen.getByText('prod-admin')).toBeInTheDocument();
    });
  });

  it('does not render stored secret values when editing', async () => {
    const client = makeClient([
      {
        id: 'conn_1',
        name: 'break-glass',
        provider: 'aws',
        auth_method: 'aws_static',
        metadata: { access_key_id: 'AKIAEXAMPLE' },
        secret_fields: ['secret_access_key'],
      },
    ]);
    render(<CloudConnectionsPanel client={client} />);

    await screen.findByText('break-glass');
    fireEvent.click(screen.getByText('break-glass'));

    expect(screen.getByLabelText('Secret access key')).toHaveValue('');
    expect(screen.getByPlaceholderText('Stored secret; leave blank to keep it')).toBeInTheDocument();
  });

  it('selects a connection as the run target', async () => {
    const connection: CloudConnection = {
      id: 'conn_1',
      name: 'prod-admin',
      provider: 'aws',
      auth_method: 'aws_profile',
      metadata: { profile: 'prod-admin' },
    };
    const client = makeClient([connection]);
    const onConnectionSelected = vi.fn();
    render(<CloudConnectionsPanel client={client} onConnectionSelected={onConnectionSelected} />);

    await screen.findByText('prod-admin');
    fireEvent.click(screen.getByRole('button', { name: 'Use for runs' }));

    expect(onConnectionSelected).toHaveBeenCalledWith(connection);
  });

  it('clears the selected run target when it is deleted', async () => {
    const client = makeClient([
      {
        id: 'conn_1',
        name: 'prod-admin',
        provider: 'aws',
        auth_method: 'aws_profile',
        metadata: { profile: 'prod-admin' },
      },
    ]);
    const onConnectionSelected = vi.fn();
    render(
      <CloudConnectionsPanel
        client={client}
        selectedConnectionId="conn_1"
        onConnectionSelected={onConnectionSelected}
      />,
    );

    await screen.findByText('Run target: prod-admin');
    fireEvent.click(screen.getByRole('button', { name: 'Delete prod-admin' }));

    expect(onConnectionSelected).toHaveBeenCalledWith(null);
    await waitFor(() => {
      expect(client.deleteCloudConnection).toHaveBeenCalledWith('conn_1');
    });
  });

  it('preserves draft identity fields when switching providers', async () => {
    const client = makeClient();
    render(<CloudConnectionsPanel client={client} />);

    await screen.findByText('0 connections');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'prod-admin' } });
    fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'us-east-1' } });
    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'azure' } });

    expect(screen.getByLabelText('Name')).toHaveValue('prod-admin');
    expect(screen.getByLabelText('Region')).toHaveValue('us-east-1');
    expect(screen.getByLabelText('Auth')).toHaveValue('azure_cli');
  });

  it('runs connection tests and deletes connections', async () => {
    const client = makeClient([
      {
        id: 'conn_1',
        name: 'sp',
        provider: 'azure',
        auth_method: 'azure_service_principal',
        metadata: { tenant_id: 'tenant-1' },
      },
    ]);
    render(<CloudConnectionsPanel client={client} />);

    await screen.findByText('sp');
    fireEvent.click(screen.getByRole('button', { name: 'Test' }));

    await waitFor(() => {
      expect(client.testCloudConnection).toHaveBeenCalledWith('conn_1');
    });
    expect(await screen.findByText(/client_secret is required/)).toBeInTheDocument();
    expect(screen.getByLabelText('Connection test failed')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Delete sp' }));
    await waitFor(() => {
      expect(client.deleteCloudConnection).toHaveBeenCalledWith('conn_1');
    });
  });
});
