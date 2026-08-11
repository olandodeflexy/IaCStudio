import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

const approvalRequiredResponse = {
  decision: {
    status: 'approval_required' as const,
    reason: 'approval_required' as const,
    allowed: false,
    approval_required: true,
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

const approvalExecutionResponse = {
  route: {
    decision: approvalRequiredResponse.decision,
    run: {
      id: 'run_000001',
      project: 'demo',
      provider_id: 'codex',
      mode: 'approved_execute' as const,
      status: 'waiting_approval' as const,
      prompt_preview: 'Create an AWS reports bucket',
      prompt_hash: 'sha256:def',
      created_at: '2026-08-11T09:00:00Z',
      updated_at: '2026-08-11T09:00:01Z',
      canceled: false,
      logs: [],
      patches: [],
      approvals: [{
        id: 'approval_000001',
        kind: 'cloud_write' as const,
        status: 'pending' as const,
        summary: 'Allow AWS cloud mutation through MCP Airlock',
        created_at: '2026-08-11T09:00:01Z',
      }],
    },
  },
  invoked: false,
};

const approvedApprovalRunResponse = {
  ...approvalExecutionResponse.route.run,
  status: 'running' as const,
  updated_at: '2026-08-11T09:01:00Z',
  approvals: [{
    ...approvalExecutionResponse.route.run.approvals[0],
    status: 'approved' as const,
    decided_at: '2026-08-11T09:01:00Z',
  }],
};

const rejectedApprovalRunResponse = {
  ...approvalExecutionResponse.route.run,
  status: 'failed' as const,
  updated_at: '2026-08-11T09:01:00Z',
  approvals: [{
    ...approvalExecutionResponse.route.run.approvals[0],
    status: 'rejected' as const,
    decided_at: '2026-08-11T09:01:00Z',
  }],
};

function fillRequiredFields() {
  fireEvent.change(screen.getByLabelText('Connection'), { target: { value: '  aws-prod  ' } });
  fireEvent.change(screen.getByLabelText('MCP server'), { target: { value: ' aws-official ' } });
  fireEvent.change(screen.getByLabelText('Tool'), { target: { value: ' list_resources ' } });
}

async function renderPendingApproval(runResponse: unknown) {
  const client = {
    previewAgentToolRoute: vi.fn().mockResolvedValue(approvalRequiredResponse),
  };
  const executionClient = {
    executeAgentToolRoute: vi.fn().mockResolvedValue(approvalExecutionResponse),
  };
  const runClient = {
    getAgentRun: vi.fn().mockResolvedValue(runResponse),
  };
  render(
    <ToolRoutePreviewPanel
      projectName="demo"
      runId="run_000001"
      client={client}
      executionClient={executionClient}
      runClient={runClient}
      idempotencyKeyFactory={() => 'approval-key'}
    />,
  );

  fillRequiredFields();
  fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
  fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
  expect(await screen.findByText('Approval required')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Request approval' }));
  const approval = await screen.findByLabelText('MCP approval request');
  expect(approval).toHaveTextContent('Approval requested');
  return { approval, executionClient, runClient };
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

  it('submits an approval-required operation without treating it as executed', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(approvalRequiredResponse),
    };
    let resolveExecution!: (_value: typeof approvalExecutionResponse) => void;
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockReturnValue(
        new Promise<typeof approvalExecutionResponse>(resolve => { resolveExecution = resolve; }),
      ),
    };
    const keyFactory = vi.fn().mockReturnValue('approval-key');
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
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Approval required')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Arguments (JSON)'), {
      target: { value: '{"bucket":"reports"}' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Request approval' }));
    expect(screen.getByRole('button', { name: 'Requesting...' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Preview access' })).toBeDisabled();
    expect(screen.getByLabelText('Connection')).toBeDisabled();
    expect(screen.getByLabelText('MCP server')).toBeDisabled();
    expect(screen.getByLabelText('Tool')).toBeDisabled();
    expect(screen.getByLabelText('Risk')).toBeDisabled();
    expect(screen.getByLabelText('Arguments (JSON)')).toBeDisabled();

    await waitFor(() => {
      expect(executionClient.executeAgentToolRoute).toHaveBeenCalledWith(
        'demo',
        'run_000001',
        {
          connection_id: 'aws-prod',
          server_id: 'aws-official',
          tool_name: 'list_resources',
          arguments: { bucket: 'reports' },
        },
        'approval-key',
      );
    });
    await act(async () => {
      resolveExecution(approvalExecutionResponse);
      await Promise.resolve();
    });
    const approval = await screen.findByLabelText('MCP approval request');
    expect(approval).toHaveTextContent('Approval requested');
    expect(approval).toHaveTextContent('Allow AWS cloud mutation through MCP Airlock');
    expect(approval).toHaveTextContent('approval_000001');
    expect(screen.getByRole('button', { name: 'Waiting for approval' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Preview access' })).toBeDisabled();
    expect(screen.getByLabelText('Connection')).toBeDisabled();
    expect(screen.getByLabelText('Arguments (JSON)')).toBeDisabled();
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
    expect(keyFactory).toHaveBeenCalledTimes(1);
  });

  it('reuses the approval request identity on retry after a transport failure', async () => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(approvalRequiredResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn()
        .mockRejectedValueOnce(new Error('gateway timeout'))
        .mockResolvedValueOnce(approvalExecutionResponse),
    };
    const keyFactory = vi.fn().mockReturnValue('approval-key');
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
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Approval required')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Request approval' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('gateway timeout');
    fireEvent.click(screen.getByRole('button', { name: 'Retry approval request' }));

    const approval = await screen.findByLabelText('MCP approval request');
    expect(approval).toHaveTextContent('Approval requested');
    expect(keyFactory).toHaveBeenCalledTimes(1);
    expect(executionClient.executeAgentToolRoute).toHaveBeenCalledTimes(2);
    expect(executionClient.executeAgentToolRoute.mock.calls[0][3]).toBe('approval-key');
    expect(executionClient.executeAgentToolRoute.mock.calls[1][3]).toBe('approval-key');
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
  });

  it.each([
    ['approved', approvedApprovalRunResponse, 'Approval granted'],
    ['rejected', rejectedApprovalRunResponse, 'Approval rejected'],
  ])('refreshes an %s gate without invoking the MCP tool again', async (_status, runResponse, label) => {
    const { approval, executionClient, runClient } = await renderPendingApproval(runResponse);

    fireEvent.click(within(approval).getByRole('button', { name: 'Refresh approval' }));

    await waitFor(() => {
      expect(runClient.getAgentRun).toHaveBeenCalledWith('demo', 'run_000001');
      expect(approval).toHaveTextContent(label);
    });
    expect(screen.getByRole('button', { name: label })).toBeDisabled();
    expect(within(approval).queryByRole('button', { name: 'Refresh approval' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Preview access' })).toBeDisabled();
    expect(screen.getByLabelText('Arguments (JSON)')).toBeDisabled();
    expect(executionClient.executeAgentToolRoute).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
  });

  it.each([
    ['a cross-project run', { ...approvedApprovalRunResponse, project: 'other' }],
    ['an approved gate in a waiting lifecycle state', {
      ...approvedApprovalRunResponse,
      status: 'waiting_approval' as const,
    }],
    ['a rejected gate in a running lifecycle state', {
      ...rejectedApprovalRunResponse,
      status: 'running' as const,
    }],
    ['mutated gate metadata', {
      ...approvedApprovalRunResponse,
      approvals: [{
        ...approvedApprovalRunResponse.approvals[0],
        summary: 'A different operation',
      }],
    }],
  ])('fails closed when an approval refresh returns %s', async (_label, runResponse) => {
    const { approval, executionClient } = await renderPendingApproval(runResponse);

    fireEvent.click(within(approval).getByRole('button', { name: 'Refresh approval' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Agent run returned an invalid approval state.');
    expect(approval).toHaveTextContent('Approval requested');
    expect(within(approval).getByRole('button', { name: 'Refresh approval' })).toBeEnabled();
    expect(executionClient.executeAgentToolRoute).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
  });

  it('discards a late approval refresh after the run scope changes', async () => {
    let resolveRun!: (_value: typeof approvedApprovalRunResponse) => void;
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(approvalRequiredResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockResolvedValue(approvalExecutionResponse),
    };
    const runClient = {
      getAgentRun: vi.fn().mockReturnValue(
        new Promise<typeof approvedApprovalRunResponse>(resolve => { resolveRun = resolve; }),
      ),
    };
    const { rerender } = render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        runClient={runClient}
        idempotencyKeyFactory={() => 'approval-key'}
      />,
    );

    fillRequiredFields();
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Approval required')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Request approval' }));
    const approval = await screen.findByLabelText('MCP approval request');
    fireEvent.click(within(approval).getByRole('button', { name: 'Refresh approval' }));
    expect(within(approval).getByRole('button', { name: 'Refreshing...' })).toBeDisabled();

    rerender(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000002"
        client={client}
        executionClient={executionClient}
        runClient={runClient}
        idempotencyKeyFactory={() => 'approval-key'}
      />,
    );

    await act(async () => {
      resolveRun(approvedApprovalRunResponse);
      await Promise.resolve();
    });

    expect(screen.queryByText('Approval granted')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('MCP approval request')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Preview access' })).toBeEnabled();
  });

  it.each([
    ['claimed invocation', { ...approvalExecutionResponse, invoked: true }],
    ['missing pending gate', {
      ...approvalExecutionResponse,
      route: {
        ...approvalExecutionResponse.route,
        run: { ...approvalExecutionResponse.route.run, approvals: [] },
      },
    }],
    ['malformed approval history', {
      ...approvalExecutionResponse,
      route: {
        ...approvalExecutionResponse.route,
        run: {
          ...approvalExecutionResponse.route.run,
          approvals: [
            ...approvalExecutionResponse.route.run.approvals,
            { status: 'approved' },
          ],
        },
      },
    }],
  ])('fails closed on an approval response with %s', async (_label, response) => {
    const client = {
      previewAgentToolRoute: vi.fn().mockResolvedValue(approvalRequiredResponse),
    };
    const executionClient = {
      executeAgentToolRoute: vi.fn().mockResolvedValue(response),
    };
    render(
      <ToolRoutePreviewPanel
        projectName="demo"
        runId="run_000001"
        client={client}
        executionClient={executionClient}
        idempotencyKeyFactory={() => 'approval-key'}
      />,
    );

    fillRequiredFields();
    fireEvent.change(screen.getByLabelText('Risk'), { target: { value: 'cloud_mutation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview access' }));
    expect(await screen.findByText('Approval required')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Request approval' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Tool execution returned an invalid response.');
    expect(screen.queryByLabelText('MCP approval request')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Untrusted MCP output')).not.toBeInTheDocument();
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
