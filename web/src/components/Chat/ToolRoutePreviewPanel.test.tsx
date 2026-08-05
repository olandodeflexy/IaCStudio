import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ToolRoutePreviewPanel } from './ToolRoutePreviewPanel';

const allowedResponse = {
  decision: {
    status: 'allowed' as const,
    reason: 'allowed' as const,
    allowed: true,
    approval_required: false,
    untrusted_output: true,
  },
};

const executionResponse = {
  route: {
    decision: allowedResponse.decision,
    run: {
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'read_only' as const,
      status: 'running' as const,
      prompt_preview: 'Inventory AWS resources',
      prompt_hash: 'sha256:abc',
      created_at: '2026-08-04T09:00:00Z',
      updated_at: '2026-08-04T09:00:01Z',
      canceled: false,
      logs: [],
      patches: [],
      approvals: [],
    },
  },
  invoked: true,
  result: {
    output: 'reports',
    is_error: false,
    untrusted_output: true,
    redacted: true,
    truncated: true,
  },
};

function fillRequiredFields() {
  fireEvent.change(screen.getByLabelText('Connection'), { target: { value: '  aws-prod  ' } });
  fireEvent.change(screen.getByLabelText('MCP server'), { target: { value: ' aws-official ' } });
  fireEvent.change(screen.getByLabelText('Tool'), { target: { value: ' list_resources ' } });
}

describe('ToolRoutePreviewPanel', () => {
  it('submits a normalized route and renders the decision', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    render(<ToolRoutePreviewPanel projectName="demo" runId="run_000001" client={client} />);

    const submit = screen.getByRole('button', { name: 'Preview access' });
    expect(submit).toBeDisabled();
    fillRequiredFields();
    fireEvent.click(submit);

    await waitFor(() => {
      expect(client.previewAgentToolRoute).toHaveBeenCalledWith('demo', 'run_000001', {
        connection_id: 'aws-prod',
        server_id: 'aws-official',
        tool_name: 'list_resources',
        risk: 'read_only',
      });
    });
    expect(await screen.findByText('Allowed')).toBeInTheDocument();
    expect(screen.getByText('Untrusted output')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Tool'), { target: { value: 'get_resources' } });
    expect(screen.queryByText('Allowed')).not.toBeInTheDocument();
  });

  it('discards a late decision after the run scope changes', async () => {
    let resolvePreview!: (_value: typeof allowedResponse) => void;
    const client = {
      previewAgentToolRoute: vi.fn().mockReturnValue(
        new Promise<typeof allowedResponse>(resolve => { resolvePreview = resolve; }),
      ),
    };
    const { rerender } = render(
      <ToolRoutePreviewPanel projectName="demo" runId="run_000001" client={client} />,
    );

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(screen.getByRole('button', { name: 'Checking...' })).toBeDisabled();
    rerender(<ToolRoutePreviewPanel projectName="demo" runId="run_000002" client={client} />);

    await act(async () => {
      resolvePreview(allowedResponse);
      await Promise.resolve();
    });

    expect(screen.queryByText('Allowed')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Preview access' })).toBeEnabled();
  });

  it('fails closed when the preview response is contradictory', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue({
        decision: {
          ...allowedResponse.decision,
          untrusted_output: false,
        },
      }),
    };
    render(<ToolRoutePreviewPanel projectName="demo" runId="run_000001" client={client} />);

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Route preview returned an invalid decision.');
    expect(screen.queryByText('Allowed')).not.toBeInTheDocument();
  });

  it('never displays a non-read-only route as directly allowed', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    render(<ToolRoutePreviewPanel projectName="demo" runId="run_000001" client={client} />);

    fillRequiredFields();
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Route preview returned an invalid decision.');
    expect(screen.queryByText('Allowed')).not.toBeInTheDocument();
  });

  it('previews unknown-risk tools through the fail-closed route', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue({
        decision: {
          status: 'denied',
          reason: 'airlock_blocked',
          allowed: false,
          approval_required: false,
          untrusted_output: true,
        },
      }),
    };
    render(<ToolRoutePreviewPanel projectName="demo" runId="run_000001" client={client} />);

    fillRequiredFields();
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'unknown' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));

    await waitFor(() => {
      expect(client.previewAgentToolRoute).toHaveBeenCalledWith('demo', 'run_000001', {
        connection_id: 'aws-prod',
        server_id: 'aws-official',
        tool_name: 'list_resources',
        risk: 'unknown',
      });
    });
    expect(await screen.findByText('Denied')).toBeInTheDocument();
  });

  it('executes an allowed read-only route and renders sanitized output metadata', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockResolvedValue(executionResponse),
    };
    render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        idempotencyKeyFactory={() => 'execution-key'}
      />,
    );

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Allowed')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Arguments (JSON)'), {
      target: { value: '{"service":"s3"}' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Execute read-only' }));

    await waitFor(() => {
      expect(executionClient.executeAgentToolRoute).toHaveBeenCalledWith(
        'demo',
        'run_000001',
        {
          connection_id: 'aws-prod',
          server_id: 'aws-official',
          tool_name: 'list_resources',
          arguments: { service: 's3' },
        },
        'execution-key',
      );
    });
    expect(await screen.findByLabelText('Untrusted MCP output')).toHaveTextContent('reports');
    expect(screen.getByText('Redacted')).toBeInTheDocument();
    expect(screen.getByText('Truncated')).toBeInTheDocument();
  });

  it('fails closed on a malformed execution response', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockResolvedValue({
        ...executionResponse,
        result: {
          ...executionResponse.result,
          output: { unsafe: 'shape' },
        },
      }),
    };
    render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        idempotencyKeyFactory={() => 'execution-key'}
      />,
    );

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Allowed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Execute read-only' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Tool execution returned an invalid response.');
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
  });

  it.each([
    ['denied', {
      status: 'denied',
      reason: 'policy_denied',
      allowed: false,
      approval_required: false,
      untrusted_output: true,
    }],
    ['approval-required', {
      status: 'approval_required',
      reason: 'approval_required',
      allowed: false,
      approval_required: true,
      untrusted_output: true,
    }],
  ] as const)('fails closed on an invoked response with a %s decision', async (_label, decision) => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockResolvedValue({
        ...executionResponse,
        route: {
          ...executionResponse.route,
          decision,
        },
      }),
    };
    render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        idempotencyKeyFactory={() => 'execution-key'}
      />,
    );

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Allowed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Execute read-only' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Tool execution returned an invalid response.');
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
  });

  it('reuses the execution identity on retry and rejects non-object arguments locally', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(allowedResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn()
        .mockRejectedValueOnce(new Error('gateway timeout'))
        .mockResolvedValueOnce(executionResponse),
    };
    const keyFactory = vi.fn().mockReturnValue('stable-key');
    render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        idempotencyKeyFactory={keyFactory}
      />,
    );

    fillRequiredFields();
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Allowed')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Arguments (JSON)'), { target: { value: '[]' } });
    fireEvent.click(screen.getByRole('button', { name: 'Execute read-only' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Arguments must be a JSON object.');
    expect(executionClient.executeAgentToolRoute).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText('Arguments (JSON)'), { target: { value: '{}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Execute read-only' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('gateway timeout');
    fireEvent.click(screen.getByRole('button', { name: 'Retry execution' }));

    await waitFor(() => expect(executionClient.executeAgentToolRoute).toHaveBeenCalledTimes(2));
    expect(keyFactory).toHaveBeenCalledTimes(1);
    expect(executionClient.executeAgentToolRoute.mock.calls[0][3]).toBe('stable-key');
    expect(executionClient.executeAgentToolRoute.mock.calls[1][3]).toBe('stable-key');
  });
});
