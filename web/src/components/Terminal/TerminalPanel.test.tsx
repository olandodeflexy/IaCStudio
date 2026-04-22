import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

import { TerminalPanel } from './TerminalPanel';

describe('TerminalPanel', () => {
  const baseProps = {
    lines: [],
    onClear: vi.fn(),
    toolColor: '#2FB5A8',
  };

  it('shows the placeholder when no lines have arrived', () => {
    render(<TerminalPanel {...baseProps} />);
    expect(screen.getByText(/Run init, plan, or apply/)).toBeInTheDocument();
  });

  it('renders every line in order', () => {
    render(<TerminalPanel {...baseProps} lines={['$ plan', 'ok', 'Apply complete']} />);
    expect(screen.getByText('$ plan')).toBeInTheDocument();
    expect(screen.getByText('ok')).toBeInTheDocument();
    expect(screen.getByText('Apply complete')).toBeInTheDocument();
  });

  it('shows the "Fix with AI" button only when a lastError + onFix are provided', () => {
    const onFix = vi.fn();
    const { rerender } = render(<TerminalPanel {...baseProps} />);
    expect(screen.queryByText(/Fix with AI/)).not.toBeInTheDocument();

    rerender(
      <TerminalPanel
        {...baseProps}
        lastError={{ command: 'plan', output: 'Error: something' }}
        onFix={onFix}
      />,
    );
    const btn = screen.getByText(/Fix with AI/);
    fireEvent.click(btn);
    expect(onFix).toHaveBeenCalledTimes(1);
  });

  it('renders Analyzing copy while fix is in flight', () => {
    render(
      <TerminalPanel
        {...baseProps}
        lastError={{ command: 'plan', output: 'Error: X' }}
        onFix={vi.fn()}
        fixLoading
      />,
    );
    expect(screen.getByText(/Analyzing/)).toBeInTheDocument();
  });

  it('clear button calls onClear', () => {
    const onClear = vi.fn();
    render(<TerminalPanel {...baseProps} onClear={onClear} lines={['foo']} />);
    fireEvent.click(screen.getByText('Clear'));
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});
