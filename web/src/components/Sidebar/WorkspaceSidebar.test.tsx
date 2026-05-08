import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { CatalogResource, Suggestion } from '../../api';
import { WorkspaceSidebar, type WorkspaceSidebarProps } from './WorkspaceSidebar';

const resources: CatalogResource[] = [
  { type: 'aws_vpc', label: 'VPC', icon: 'V', category: 'Networking' },
  { type: 'aws_subnet', label: 'Subnet', icon: 'S', category: 'Networking' },
  { type: 'aws_s3_bucket', label: 'S3 Bucket', icon: 'B', category: 'Storage' },
];

const suggestions: Suggestion[] = [
  { type: 'aws_subnet', label: 'Add subnet', reason: 'VPCs usually need subnets.', priority: 1 },
];

function baseProps(): WorkspaceSidebarProps {
  return {
    width: 240,
    activePanel: 'palette',
    tool: 'terraform',
    toolMeta: { color: '#2FB5A8', ext: '.tf' },
    projectName: 'demo',
    provider: 'aws',
    resources,
    suggestions: [],
    searchQuery: '',
    onActivePanelChange: vi.fn(),
    onSearchQueryChange: vi.fn(),
    onAddResource: vi.fn(),
    onResourceHover: vi.fn(),
    onResourceHoverEnd: vi.fn(),
  };
}

function renderSidebar(overrides: Partial<WorkspaceSidebarProps> = {}) {
  const props = { ...baseProps(), ...overrides };
  render(<WorkspaceSidebar {...props} />);
  return props;
}

describe('WorkspaceSidebar', () => {
  it('filters palette resources and dispatches resource interactions', () => {
    const props = renderSidebar({ searchQuery: 'bucket' });

    expect(screen.getByText('1 result')).toBeInTheDocument();
    expect(screen.getByText('Storage')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add S3 Bucket' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add VPC' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole('textbox', { name: 'Search resources' }), { target: { value: 'vpc' } });
    fireEvent.mouseEnter(screen.getByRole('button', { name: 'Add S3 Bucket' }));
    fireEvent.mouseLeave(screen.getByRole('button', { name: 'Add S3 Bucket' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add S3 Bucket' }));

    expect(props.onSearchQueryChange).toHaveBeenCalledWith('vpc');
    expect(props.onResourceHover).toHaveBeenCalledWith(resources[2], expect.objectContaining({ x: expect.any(Number), y: expect.any(Number) }));
    expect(props.onResourceHoverEnd).toHaveBeenCalled();
    expect(props.onAddResource).toHaveBeenCalledWith(resources[2]);
  });

  it('shows empty search results', () => {
    renderSidebar({ searchQuery: 'database' });

    expect(screen.getByText('0 results')).toBeInTheDocument();
    expect(screen.getByText('No resources matching "database"')).toBeInTheDocument();
  });

  it('renders suggestions and dispatches suggestion resource adds', () => {
    const props = renderSidebar({ activePanel: 'suggest', suggestions });

    expect(screen.getByRole('button', { name: /Resources/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Next 1/i })).toBeInTheDocument();
    expect(screen.getByText('VPCs usually need subnets.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Add suggested resource Add subnet' }));

    expect(props.onAddResource).toHaveBeenCalledWith(resources[1]);
  });

  it('renders provider-specific guide content and switches to suggestions', () => {
    const props = renderSidebar({ activePanel: 'guide', provider: 'google' });

    expect(screen.getByText('Start with a VPC Network (google_compute_network)')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'View Suggestions →' }));

    expect(props.onActivePanelChange).toHaveBeenCalledWith('suggest');
  });

  it('renders file names for the project tool extension', () => {
    renderSidebar({ activePanel: 'files', toolMeta: { color: '#8A63D2', ext: '.ts' } });

    expect(screen.getByText('DIR demo/')).toBeInTheDocument();
    expect(screen.getByText('FILE main.ts')).toBeInTheDocument();
    expect(screen.getByText('FILE variables.ts')).toBeInTheDocument();
  });
});
