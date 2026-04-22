import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { ModuleRegistryPanel } from './ModuleRegistryPanel';

const modules = [
  {
    id: 'terraform-aws-modules/vpc/aws/5.0.0',
    namespace: 'terraform-aws-modules',
    name: 'vpc',
    provider: 'aws',
    version: '5.0.0',
    description: 'Terraform module which creates VPC resources on AWS',
    source: 'https://github.com/terraform-aws-modules/terraform-aws-vpc',
    published_at: '2024-01-01T00:00:00Z',
    downloads: 123456,
    verified: true,
  },
];

describe('ModuleRegistryPanel', () => {
  it('shows the start-typing hint when no query is entered', async () => {
    const client = { searchModules: vi.fn() };
    render(<ModuleRegistryPanel client={client} />);
    expect(await screen.findByText(/Start typing to search/)).toBeInTheDocument();
    expect(client.searchModules).not.toHaveBeenCalled();
  });

  it('debounces the search and renders results', async () => {
    vi.useFakeTimers();
    const client = { searchModules: vi.fn().mockResolvedValue({ modules }) };
    render(<ModuleRegistryPanel client={client} />);

    fireEvent.change(screen.getByPlaceholderText(/Search Terraform modules/), {
      target: { value: 'vpc' },
    });

    // Debounce window is 250ms. Fire all timers + flush micro-tasks so
    // the pending fetch promise resolves within the test.
    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    vi.useRealTimers();

    await waitFor(() => expect(client.searchModules).toHaveBeenCalledWith('vpc'));
    expect(await screen.findByText(/terraform-aws-modules\/vpc\/aws/)).toBeInTheDocument();
    expect(screen.getByText(/verified/i)).toBeInTheDocument();
  });

  it('exposes the Adopt button when onAdopt is provided', async () => {
    vi.useFakeTimers();
    const onAdopt = vi.fn();
    const client = { searchModules: vi.fn().mockResolvedValue({ modules }) };
    render(<ModuleRegistryPanel client={client} onAdopt={onAdopt} initialQuery="vpc" />);

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    vi.useRealTimers();

    const btn = await screen.findByText('Adopt');
    fireEvent.click(btn);
    expect(onAdopt).toHaveBeenCalledWith(modules[0]);
  });
});
