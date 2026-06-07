import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { DriftPanel } from './DriftPanel';

describe('DriftPanel', () => {
  it('runs drift with tool and environment and renders classified findings', async () => {
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: true,
        state_path: '/tmp/demo/environments/dev/terraform.tfstate',
        drifted: [],
        findings: [
          {
            address: 'aws_security_group.web',
            type: 'aws_security_group',
            name: 'web',
            status: 'drifted',
            path: 'ingress.0.cidr_blocks',
            expected_value: ['10.0.0.0/8'],
            current_value: ['0.0.0.0/0'],
            classification: 'unauthorized_change',
            recommended_action: 'revert_or_codify_after_review',
            reason: 'Security group ingress widened outside code.',
          },
        ],
        missing: [],
        unmanaged: [],
        in_sync: 4,
        total: 5,
        classifications: { unauthorized_change: 1 },
        summary: '5 resources: 4 in sync, 1 drifted, 0 missing from state, 0 unmanaged',
      }),
    };

    render(<DriftPanel projectName="demo" tool="terraform" env="dev" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));

    await waitFor(() => {
      expect(client.runDrift).toHaveBeenCalledWith('demo', { tool: 'terraform', env: 'dev' });
    });

    expect(screen.getByText('aws_security_group.web')).toBeInTheDocument();
    expect(screen.getByText('unauthorized change')).toBeInTheDocument();
    expect(screen.getByText('revert or codify after review')).toBeInTheDocument();
    expect(screen.getByText('Security group ingress widened outside code.')).toBeInTheDocument();
    expect(screen.getByText('["10.0.0.0/8"]')).toBeInTheDocument();
    expect(screen.getByText('["0.0.0.0/0"]')).toBeInTheDocument();
  });

  it('shows a no-state result without fabricating findings', async () => {
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: false,
        state_path: '',
        drifted: [],
        findings: [],
        missing: [],
        unmanaged: [],
        in_sync: 0,
        total: 0,
        classifications: {},
        summary: 'No state file found',
      }),
    };

    render(<DriftPanel projectName="demo" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));

    expect(await screen.findByText('No state file was found for this project.')).toBeInTheDocument();
    expect(screen.getByText('0 findings')).toBeInTheDocument();
  });

  it('disables drift for tools the backend does not support yet', () => {
    const client = { runDrift: vi.fn() };

    render(<DriftPanel projectName="demo" tool="ansible" client={client} />);

    expect(screen.getByRole('button', { name: 'Run drift' })).toBeDisabled();
    expect(screen.getByText('Drift detection currently supports Terraform and OpenTofu projects.')).toBeInTheDocument();
    expect(client.runDrift).not.toHaveBeenCalled();
  });
});
