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
});
