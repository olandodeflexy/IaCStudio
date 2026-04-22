import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { ScanPanel } from './ScanPanel';

const scannerList = [
  { name: 'checkov', available: true },
  { name: 'trivy', available: true },
];

const scanResponse = {
  results: [
    { engine: 'checkov', available: true },
    { engine: 'trivy', available: true },
  ],
  findings: [
    {
      engine: 'checkov',
      policy_id: 'CKV_AWS_20',
      policy_name: 'S3 public access',
      severity: 'error' as const,
      message: 'Ensure the S3 bucket does not allow public access',
      resource: 'aws_s3_bucket.public',
    },
  ],
  blocking: true,
};

describe('ScanPanel', () => {
  it('renders scanner toggles and runs the scan', async () => {
    const client = {
      listSecurityScanners: vi.fn().mockResolvedValue(scannerList),
      runScanners: vi.fn().mockResolvedValue(scanResponse),
    };
    render(<ScanPanel projectName="demo" client={client} />);

    await waitFor(() => expect(client.listSecurityScanners).toHaveBeenCalled());
    expect(screen.getByLabelText('Toggle checkov')).toBeChecked();
    expect(screen.getByLabelText('Toggle trivy')).toBeChecked();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Run scanners/ }));
    });

    expect(client.runScanners).toHaveBeenCalledWith('demo', {
      scanners: ['checkov', 'trivy'],
      tool: 'terraform',
    });
    await waitFor(() => {
      expect(screen.getByText('S3 public access')).toBeInTheDocument();
    });
  });

  it('shows an empty state until the user runs', async () => {
    const client = {
      listSecurityScanners: vi.fn().mockResolvedValue(scannerList),
      runScanners: vi.fn(),
    };
    render(<ScanPanel projectName="demo" client={client} />);
    await waitFor(() => expect(client.listSecurityScanners).toHaveBeenCalled());
    expect(screen.getByText('Run scanners to see findings.')).toBeInTheDocument();
  });
});
