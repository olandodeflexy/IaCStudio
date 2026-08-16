import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { MCPAirlockExecutableAttestation, MCPAirlockServerStatus } from '../../api';
import { MCPAirlockPanel } from './MCPAirlockPanel';

function server(overrides: Partial<MCPAirlockServerStatus> = {}): MCPAirlockServerStatus {
  return {
    server: {
      id: 'terraform-official',
      name: 'Terraform MCP Server',
      vendor: 'HashiCorp',
      description: 'Official Terraform MCP server.',
      source_url: 'https://github.com/hashicorp/terraform-mcp-server',
      docs_url: 'https://developer.hashicorp.com/terraform/mcp-server',
      install_hint: 'Install terraform-mcp-server on PATH.',
      transport: 'stdio',
      command: 'terraform-mcp-server',
      launch_source: 'registry',
      trusted: true,
      read_only_default: true,
      credential_mode: 'none',
      capabilities: ['terraform registry'],
    },
    ready: false,
    running: false,
    configured: true,
    command_available: false,
    state: 'command_missing',
    summary: 'Configured command was not found on PATH.',
    checks: [
      { name: 'trusted_registry', status: 'pass', message: 'trusted' },
      { name: 'command', status: 'error', message: 'command is not installed' },
    ],
    ...overrides,
  };
}

function approval(overrides: Partial<MCPAirlockExecutableAttestation> = {}): MCPAirlockExecutableAttestation {
  return {
    server_id: 'terraform-official',
    launch_source: 'registry',
    fingerprint: { algorithm: 'sha256', digest: 'ab'.repeat(32) },
    approved_at: '2026-08-15T20:00:00Z',
    ...overrides,
  };
}

describe('MCPAirlockPanel', () => {
  it('lists trusted servers and runs health checks', async () => {
    const initial = server();
    const checked = server({
      ready: true,
      command_available: true,
      state: 'ready',
      summary: 'Health check completed without exposing cloud credentials.',
      checked_at: '2026-06-13T10:00:00Z',
      checks: [{ name: 'health_probe', status: 'pass', message: 'probe succeeded' }],
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => checked),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => checked),
      stopMCPAirlockServer: vi.fn(async () => checked),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    expect(await screen.findByText('Terraform MCP Server')).toBeInTheDocument();
    expect(screen.getByText('credentials: none')).toBeInTheDocument();
    expect(screen.getByText('Install terraform-mcp-server on PATH.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Check' }));

    await waitFor(() => {
      expect(client.checkMCPAirlockServer).toHaveBeenCalledWith('terraform-official');
    });
    expect(await screen.findByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Health check completed without exposing cloud credentials.')).toBeInTheDocument();
  });

  it('approves a reviewed executable fingerprint', async () => {
    const initial = server({
      ready: true,
      command_available: true,
      state: 'ready',
      summary: 'Health check completed without exposing cloud credentials.',
      executable_fingerprint: approval().fingerprint,
      executable_attestation: 'approval_required',
      checks: [{ name: 'executable_attestation', status: 'warn', message: 'executable has not been approved for this launch source' }],
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    expect(await screen.findByText('Executable approval required')).toBeInTheDocument();
    expect(screen.getByText(`SHA-256 ${'ab'.repeat(32)}`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Approve executable for Terraform MCP Server' }));

    await waitFor(() => {
      expect(client.approveMCPAirlockExecutable).toHaveBeenCalledWith('terraform-official', approval().fingerprint);
    });
    expect(await screen.findByText(/executable matches the approved fingerprint for this launch source/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Approve executable for Terraform MCP Server' })).not.toBeInTheDocument();
  });

  it('rejects an executable approval response for another server', async () => {
    const initial = server({
      command_available: true,
      state: 'ready',
      executable_fingerprint: approval().fingerprint,
      executable_attestation: 'approval_required',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval({ server_id: 'aws-official' })),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve executable for Terraform MCP Server' }));

    expect(await screen.findByText('Error: MCP executable approval identity mismatch')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Approve executable for Terraform MCP Server' })).toBeInTheDocument();
  });

  it('rejects an approval response for a different executable fingerprint', async () => {
    const initial = server({
      command_available: true,
      state: 'ready',
      executable_fingerprint: approval().fingerprint,
      executable_attestation: 'approval_required',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval({
        fingerprint: { algorithm: 'sha256', digest: 'cd'.repeat(32) },
      })),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve executable for Terraform MCP Server' }));

    expect(await screen.findByText('Error: MCP executable approval fingerprint mismatch')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Approve executable for Terraform MCP Server' })).toBeInTheDocument();
  });

  it('treats executable drift as an error and allows explicit reapproval', async () => {
    const initial = server({
      command_available: true,
      state: 'ready',
      executable_fingerprint: approval().fingerprint,
      executable_attestation: 'executable_changed',
      checks: [{ name: 'executable_attestation', status: 'error', message: 'executable fingerprint changed after approval' }],
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Executable changed');
    expect(alert).toHaveClass('border-destructive');

    fireEvent.click(screen.getByRole('button', { name: 'Approve executable for Terraform MCP Server' }));
    await waitFor(() => {
      expect(client.approveMCPAirlockExecutable).toHaveBeenCalledWith('terraform-official', approval().fingerprint);
    });
    expect(screen.queryByText('Executable changed')).not.toBeInTheDocument();
  });

  it('starts and stops configured servers', async () => {
    const initial = server({
      command_available: true,
      state: 'available',
      summary: 'Command is available.',
    });
    const running = server({
      ready: true,
      running: true,
      command_available: true,
      state: 'running',
      summary: 'MCP server process is running with cloud credentials withheld.',
      started_at: '2026-06-13T10:00:00Z',
    });
    const stopped = server({
      command_available: true,
      state: 'stopped',
      summary: 'MCP server process stopped.',
      last_exit_at: '2026-06-13T10:01:00Z',
      last_exit_reason: 'stopped by user',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => running),
      stopMCPAirlockServer: vi.fn(async () => stopped),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Start Terraform MCP Server' }));

    await waitFor(() => {
      expect(client.startMCPAirlockServer).toHaveBeenCalledWith('terraform-official');
    });
    expect(await screen.findByText('Running')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Stop Terraform MCP Server' }));

    await waitFor(() => {
      expect(client.stopMCPAirlockServer).toHaveBeenCalledWith('terraform-official');
    });
    expect(await screen.findByText('MCP server process stopped.')).toBeInTheDocument();
  });

  it('discovers tools and shows firewall decisions', async () => {
    const initial = server({
      command_available: true,
      state: 'available',
      summary: 'Command is available.',
    });
    const inventory = {
      server_id: 'terraform-official',
      discovered_at: '2026-06-13T10:00:00Z',
      checks: [{ name: 'tool_discovery', status: 'pass' as const, message: 'discovered 2 external MCP tools' }],
      tools: [
        {
          server_id: 'terraform-official',
          name: 'list_modules',
          description: 'List registry modules',
          input_schema_hash: 'sha256:abc',
          last_seen_at: '2026-06-13T10:00:00Z',
          schema_state: 'new' as const,
          risk: 'read_only' as const,
          decision: {
            status: 'allowed' as const,
            allowed: true,
            approval_required: false,
            risk: 'read_only' as const,
            reason: 'read-only',
            allowlisted: false,
            untrusted_output: true,
          },
        },
        {
          server_id: 'terraform-official',
          name: 'apply_workspace',
          description: 'Apply a Terraform workspace',
          input_schema_hash: 'sha256:def',
          last_seen_at: '2026-06-13T10:00:00Z',
          schema_state: 'new' as const,
          risk: 'cloud_mutation' as const,
          decision: {
            status: 'blocked' as const,
            allowed: false,
            approval_required: false,
            risk: 'cloud_mutation' as const,
            reason: 'requires allowlist',
            allowlisted: false,
            untrusted_output: true,
          },
        },
      ],
    };
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => inventory),
    };

    render(<MCPAirlockPanel client={client} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Tools' }));

    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
    expect(await screen.findByText('Tool Firewall')).toBeInTheDocument();
    expect(screen.getByText('list_modules')).toBeInTheDocument();
    expect(screen.getByText('apply_workspace')).toBeInTheDocument();
    expect(screen.getByText('cloud mutation')).toBeInTheDocument();
    expect(screen.getByText('blocked')).toBeInTheDocument();
  });

  it('disables tool discovery when the server is unavailable', async () => {
    const initial = server();
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    expect(toolsButton).toBeDisabled();

    fireEvent.click(toolsButton);

    expect(client.discoverMCPAirlockTools).not.toHaveBeenCalled();
  });

  it('allows tool discovery after a successful health check', async () => {
    const initial = server({
      ready: true,
      command_available: true,
      state: 'ready',
      summary: 'Health check completed without exposing cloud credentials.',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    expect(toolsButton).toBeEnabled();

    fireEvent.click(toolsButton);

    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
  });

  it('allows tool discovery in non-ready lifecycle states when the command is available', async () => {
    const initial = server({
      command_available: true,
      state: 'stopped',
      summary: 'MCP server process stopped.',
      last_exit_reason: 'stopped by user',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    expect(toolsButton).toBeEnabled();

    fireEvent.click(toolsButton);

    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
  });

  it('disables tool discovery when the command is unavailable', async () => {
    const initial = server({
      ready: true,
      command_available: false,
      state: 'ready',
      summary: 'Configured command was removed from PATH.',
    });
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    expect(toolsButton).toBeDisabled();

    fireEvent.click(toolsButton);
    expect(client.discoverMCPAirlockTools).not.toHaveBeenCalled();
  });

  it('disables tool discovery while another server action is busy', async () => {
    const initial = server({
      ready: true,
      command_available: true,
      state: 'ready',
      summary: 'Health check completed without exposing cloud credentials.',
    });
    let resolveCheck!: (_status: MCPAirlockServerStatus) => void;
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(() => new Promise<MCPAirlockServerStatus>(resolve => {
        resolveCheck = resolve;
      })),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    expect(toolsButton).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: 'Check' }));

    await waitFor(() => {
      expect(toolsButton).toBeDisabled();
    });

    fireEvent.click(toolsButton);
    expect(client.discoverMCPAirlockTools).not.toHaveBeenCalled();

    resolveCheck(initial);
    await waitFor(() => {
      expect(client.checkMCPAirlockServer).toHaveBeenCalledWith('terraform-official');
    });
  });

  it('disables health checks while tool discovery is running', async () => {
    const initial = server({
      ready: true,
      command_available: true,
      state: 'ready',
      summary: 'Health check completed without exposing cloud credentials.',
    });
    let resolveDiscovery!: (_inventory: { server_id: string; tools: []; checks: [] }) => void;
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(() => new Promise<{ server_id: string; tools: []; checks: [] }>(resolve => {
        resolveDiscovery = resolve;
      })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    const checkButton = screen.getByRole('button', { name: 'Check' });

    fireEvent.click(toolsButton);

    await waitFor(() => {
      expect(checkButton).toBeDisabled();
    });

    fireEvent.click(checkButton);
    expect(client.checkMCPAirlockServer).not.toHaveBeenCalled();

    resolveDiscovery({ server_id: 'terraform-official', tools: [], checks: [] });
    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
  });

  it('disables lifecycle actions while tool discovery is running', async () => {
    const initial = server({
      command_available: true,
      state: 'available',
      summary: 'Command is available.',
    });
    let resolveDiscovery!: (_inventory: { server_id: string; tools: []; checks: [] }) => void;
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(() => new Promise<{ server_id: string; tools: []; checks: [] }>(resolve => {
        resolveDiscovery = resolve;
      })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    const startButton = screen.getByRole('button', { name: 'Start Terraform MCP Server' });
    expect(startButton).toBeEnabled();

    fireEvent.click(toolsButton);

    await waitFor(() => {
      expect(startButton).toBeDisabled();
    });

    fireEvent.click(startButton);
    expect(client.startMCPAirlockServer).not.toHaveBeenCalled();

    resolveDiscovery({ server_id: 'terraform-official', tools: [], checks: [] });
    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
  });

  it('disables stop while tool discovery is running', async () => {
    const initial = server({
      ready: true,
      running: true,
      command_available: true,
      state: 'running',
      summary: 'MCP server process is running with cloud credentials withheld.',
    });
    let resolveDiscovery!: (_inventory: { server_id: string; tools: []; checks: [] }) => void;
    const client = {
      listMCPAirlockServers: vi.fn(async () => [initial]),
      checkMCPAirlockServer: vi.fn(async () => initial),
      approveMCPAirlockExecutable: vi.fn(async () => approval()),
      startMCPAirlockServer: vi.fn(async () => initial),
      stopMCPAirlockServer: vi.fn(async () => initial),
      getMCPAirlockTools: vi.fn(async () => ({ server_id: 'terraform-official', tools: [], checks: [] })),
      discoverMCPAirlockTools: vi.fn(() => new Promise<{ server_id: string; tools: []; checks: [] }>(resolve => {
        resolveDiscovery = resolve;
      })),
    };

    render(<MCPAirlockPanel client={client} />);

    const toolsButton = await screen.findByRole('button', { name: 'Tools' });
    const stopButton = screen.getByRole('button', { name: 'Stop Terraform MCP Server' });
    expect(stopButton).toBeEnabled();

    fireEvent.click(toolsButton);

    await waitFor(() => {
      expect(stopButton).toBeDisabled();
    });

    fireEvent.click(stopButton);
    expect(client.stopMCPAirlockServer).not.toHaveBeenCalled();

    resolveDiscovery({ server_id: 'terraform-official', tools: [], checks: [] });
    await waitFor(() => {
      expect(client.discoverMCPAirlockTools).toHaveBeenCalledWith('terraform-official');
    });
  });
});
