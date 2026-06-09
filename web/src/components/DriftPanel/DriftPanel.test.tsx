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

  it('drafts a remediation PR proposal for active findings', async () => {
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: true,
        state_path: '/tmp/demo/terraform.tfstate',
        drifted: [],
        findings: [
          {
            address: 'aws_security_group.web',
            type: 'aws_security_group',
            name: 'web',
            status: 'drifted',
            path: 'ingress',
            expected_value: [],
            current_value: [{ cidr_blocks: ['0.0.0.0/0'] }],
            classification: 'unauthorized_change',
            recommended_action: 'revert_or_codify_after_review',
            reason: 'Network drift can change reachability.',
          },
        ],
        missing: [],
        unmanaged: [],
        in_sync: 0,
        total: 1,
        classifications: { unauthorized_change: 1 },
        summary: '1 resources: 0 in sync, 1 drifted, 0 missing from state, 0 unmanaged',
      }),
      createDriftRemediation: vi.fn().mockResolvedValue({
        mode: 'revert',
        title: 'Revert unauthorized drift for demo',
        branch: 'iac-studio-drift-revert-demo-dev',
        commit_message: 'Document drift revert for demo',
        body: '## Summary',
        findings: [],
        file_changes: [
          {
            path: 'main.tf',
            line: 2,
            action: 'revert',
            address: 'aws_security_group.web',
            field: 'ingress',
            summary: 'Restore live aws_security_group.web ingress to the value declared in code.',
          },
        ],
        warnings: ['Revert drafts do not edit IaC files.'],
      }),
    };

    render(<DriftPanel projectName="demo" tool="terraform" env="dev" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));
    expect(await screen.findByText('aws_security_group.web')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Draft revert PR' }));

    await waitFor(() => {
      expect(client.createDriftRemediation).toHaveBeenCalledWith('demo', { tool: 'terraform', env: 'dev', mode: 'revert' });
    });
    expect(await screen.findByText('Revert unauthorized drift for demo')).toBeInTheDocument();
    expect(screen.getByText('branch iac-studio-drift-revert-demo-dev')).toBeInTheDocument();
    expect(screen.getByText('Restore live aws_security_group.web ingress to the value declared in code.')).toBeInTheDocument();
    expect(screen.getByText('Revert drafts do not edit IaC files.')).toBeInTheDocument();
  });

  it('writes remediation review artifacts for a drafted proposal', async () => {
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: true,
        state_path: '/tmp/demo/terraform.tfstate',
        drifted: [],
        findings: [
          {
            address: 'aws_security_group.web',
            type: 'aws_security_group',
            name: 'web',
            status: 'drifted',
            path: 'ingress',
            expected_value: [],
            current_value: [{ cidr_blocks: ['0.0.0.0/0'] }],
            classification: 'unauthorized_change',
            recommended_action: 'revert_or_codify_after_review',
            reason: 'Network drift can change reachability.',
          },
        ],
        missing: [],
        unmanaged: [],
        in_sync: 0,
        total: 1,
        classifications: { unauthorized_change: 1 },
        summary: '1 resources: 0 in sync, 1 drifted, 0 missing from state, 0 unmanaged',
      }),
      createDriftRemediation: vi.fn().mockResolvedValue({
        mode: 'revert',
        title: 'Revert unauthorized drift for demo',
        branch: 'iac-studio-drift-revert-demo-dev',
        commit_message: 'Document drift revert for demo',
        body: '## Summary',
        findings: [],
        file_changes: [
          {
            path: 'main.tf',
            line: 2,
            action: 'revert',
            address: 'aws_security_group.web',
            field: 'ingress',
            summary: 'Restore live aws_security_group.web ingress to the value declared in code.',
          },
        ],
      }),
      createDriftRemediationArtifacts: vi.fn().mockResolvedValue({
        id: 'iac-studio-drift-revert-demo-dev',
        root: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev',
        created_at: '2026-06-09T19:00:00Z',
        proposal: {
          mode: 'revert',
          title: 'Revert unauthorized drift for demo',
          branch: 'iac-studio-drift-revert-demo-dev',
          commit_message: 'Document drift revert for demo',
          body: '## Summary',
          findings: [],
          file_changes: [],
        },
        files: [
          {
            path: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev/README.md',
            kind: 'runbook',
            summary: 'Human-readable drift remediation runbook.',
            size: 100,
          },
          {
            path: '.iac-studio/remediations/iac-studio-drift-revert-demo-dev/pr-body.md',
            kind: 'pr_body',
            summary: 'Pull request body generated from the remediation proposal.',
            size: 20,
          },
        ],
      }),
    };

    render(<DriftPanel projectName="demo" tool="terraform" env="dev" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));
    expect(await screen.findByText('aws_security_group.web')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Draft revert PR' }));
    expect(await screen.findByText('Revert unauthorized drift for demo')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Write artifacts' }));

    await waitFor(() => {
      expect(client.createDriftRemediationArtifacts).toHaveBeenCalledWith('demo', { tool: 'terraform', env: 'dev', mode: 'revert' });
    });
    expect(await screen.findByText('Review artifacts written')).toBeInTheDocument();
    expect(screen.getByText('.iac-studio/remediations/iac-studio-drift-revert-demo-dev')).toBeInTheDocument();
    expect(screen.getByText('.iac-studio/remediations/iac-studio-drift-revert-demo-dev/README.md')).toBeInTheDocument();
  });

  it('shows all warnings and a +N overflow note when there are more than 3 file changes', async () => {
    const changes = Array.from({ length: 5 }, (_, i) => ({
      path: `main.tf`,
      line: i + 1,
      action: 'revert',
      address: `aws_instance.host${i}`,
      field: 'ami',
      summary: `Restore host${i} ami.`,
    }));
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: true,
        state_path: '/tmp/demo/terraform.tfstate',
        drifted: [],
        findings: changes.map((c) => ({
          address: c.address,
          type: 'aws_instance',
          name: c.address.split('.')[1],
          status: 'drifted',
          path: 'ami',
          expected_value: 'ami-old',
          current_value: 'ami-new',
          classification: 'unauthorized_change',
          recommended_action: 'revert_or_codify_after_review',
          reason: 'AMI drift.',
        })),
        missing: [],
        unmanaged: [],
        in_sync: 0,
        total: 5,
        classifications: { unauthorized_change: 5 },
        summary: '5 resources: 0 in sync, 5 drifted, 0 missing from state, 0 unmanaged',
      }),
      createDriftRemediation: vi.fn().mockResolvedValue({
        mode: 'revert',
        title: 'Revert unauthorized drift for demo',
        branch: 'iac-studio-drift-revert-demo',
        commit_message: 'Document drift revert for demo',
        body: '## Summary',
        findings: [],
        file_changes: changes,
        warnings: ['Revert drafts do not edit IaC files.', 'Review network changes carefully.'],
      }),
    };

    render(<DriftPanel projectName="demo" tool="terraform" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));
    expect(await screen.findByText('aws_instance.host0')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Draft revert PR' }));

    await waitFor(() => {
      expect(client.createDriftRemediation).toHaveBeenCalled();
    });

    // Only first 3 changes shown, with an overflow indicator
    expect((await screen.findAllByText('aws_instance.host0')).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('aws_instance.host1').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('aws_instance.host2').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('+2 more changes — see PR description')).toBeInTheDocument();

    // Both warnings are visible
    expect(screen.getByText('Revert drafts do not edit IaC files.')).toBeInTheDocument();
    expect(screen.getByText('Review network changes carefully.')).toBeInTheDocument();
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

  it('renders suppressed findings as muted known-noise items', async () => {
    const client = {
      runDrift: vi.fn().mockResolvedValue({
        has_state: true,
        state_path: '/tmp/demo/terraform.tfstate',
        drifted: [],
        findings: [],
        suppressed_findings: [
          {
            address: 'aws_s3_bucket.logs',
            type: 'aws_s3_bucket',
            name: 'logs',
            status: 'drifted',
            path: 'tags',
            classification: 'legitimate_config_change',
            recommended_action: 'codify_or_accept',
            reason: 'Only metadata fields drifted.',
            suppressed: true,
            suppression_reason: 'provider-managed owner tag',
          },
        ],
        suppressed: 1,
        missing: [],
        unmanaged: [],
        in_sync: 1,
        total: 1,
        classifications: {},
        summary: '1 resources: 1 in sync, 0 drifted, 0 missing from state, 0 unmanaged, 1 suppressed',
      }),
    };

    render(<DriftPanel projectName="demo" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));

    expect(await screen.findByText('No active drift findings.')).toBeInTheDocument();
    expect(screen.getByText('1 suppressed')).toBeInTheDocument();
    expect(screen.getByText('Suppressed')).toBeInTheDocument();
    expect(screen.getByText('aws_s3_bucket.logs')).toBeInTheDocument();
    expect(screen.getByText('provider-managed owner tag')).toBeInTheDocument();
  });

  it('shows the error banner when the API call fails', async () => {
    const client = {
      runDrift: vi.fn().mockRejectedValue(new Error('connection refused')),
    };

    render(<DriftPanel projectName="demo" tool="terraform" client={client} />);

    fireEvent.click(screen.getByRole('button', { name: 'Run drift' }));

    expect(await screen.findByText('Error: connection refused')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run drift' })).not.toBeDisabled();
  });

  it('disables drift for tools the backend does not support yet', () => {
    const client = { runDrift: vi.fn() };

    render(<DriftPanel projectName="demo" tool="ansible" client={client} />);

    expect(screen.getByRole('button', { name: 'Run drift' })).toBeDisabled();
    expect(screen.getByText('Drift detection currently supports Terraform and OpenTofu projects.')).toBeInTheDocument();
    expect(client.runDrift).not.toHaveBeenCalled();
  });
});
