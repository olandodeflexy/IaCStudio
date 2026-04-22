import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { PolicyStudioPanel } from './PolicyStudioPanel';

const engineList = [
  { name: 'opa', available: true },
  { name: 'sentinel', available: false },
];

const findingsResponse = {
  results: [
    { engine: 'opa', available: true, findings: [] },
  ],
  findings: [
    {
      engine: 'opa',
      policy_id: 'tags.required',
      policy_name: 'Required tags',
      severity: 'error' as const,
      message: 'Missing Owner tag on aws_s3_bucket.data',
      resource: 'aws_s3_bucket.data',
    },
  ],
  blocking: true,
};

describe('PolicyStudioPanel', () => {
  it('lists engines and greys out unavailable ones', async () => {
    const client = {
      listPolicyEngines: vi.fn().mockResolvedValue(engineList),
      runPolicy: vi.fn(),
    };
    render(<PolicyStudioPanel projectName="demo" client={client} />);

    await waitFor(() => expect(client.listPolicyEngines).toHaveBeenCalled());
    expect(screen.getByLabelText('Toggle opa')).not.toBeDisabled();
    expect(screen.getByLabelText('Toggle sentinel')).toBeDisabled();
    expect(screen.getByText('(not installed)')).toBeInTheDocument();
  });

  it('runs the selected engines and renders findings', async () => {
    const client = {
      listPolicyEngines: vi.fn().mockResolvedValue(engineList),
      runPolicy: vi.fn().mockResolvedValue(findingsResponse),
    };
    render(<PolicyStudioPanel projectName="demo" client={client} />);

    await waitFor(() => expect(client.listPolicyEngines).toHaveBeenCalled());
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Run policies/ }));
    });

    expect(client.runPolicy).toHaveBeenCalledWith('demo', {
      engines: ['opa'],
      tool: 'terraform',
    });
    await waitFor(() => {
      expect(screen.getByText('Required tags')).toBeInTheDocument();
    });
    expect(screen.getByText(/Missing Owner tag/)).toBeInTheDocument();
    expect(screen.getByText(/Blocking/)).toBeInTheDocument();
  });

  it('surfaces client errors inline', async () => {
    const client = {
      listPolicyEngines: vi.fn().mockRejectedValue(new Error('network down')),
      runPolicy: vi.fn(),
    };
    render(<PolicyStudioPanel projectName="demo" client={client} />);
    await waitFor(() => {
      expect(screen.getByText(/network down/)).toBeInTheDocument();
    });
  });
});
