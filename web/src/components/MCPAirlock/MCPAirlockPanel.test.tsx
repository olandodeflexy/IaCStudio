import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { MCPAirlockServerStatus } from '../../api';
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
});
