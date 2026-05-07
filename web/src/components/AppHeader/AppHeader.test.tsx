import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { AppHeader } from './AppHeader';

function baseProps() {
  return {
    tool: 'terraform',
    toolMeta: { color: '#7dd3fc', icon: 'TF', name: 'Terraform' },
    projectName: 'demo',
    projectId: 'demo',
    resourceCount: 2,
    wsConnected: true,
    canUndo: true,
    canRedo: false,
    onBack: vi.fn(),
    onProjectNameChange: vi.fn(),
    onRevealProject: vi.fn(),
    onUndo: vi.fn(),
    onRedo: vi.fn(),
    onRunCommand: vi.fn(),
    onOpenSettings: vi.fn(),
  };
}

type AppHeaderTestProps = ReturnType<typeof baseProps>;

function renderHeader(overrides: Partial<AppHeaderTestProps> = {}) {
  const props = { ...baseProps(), ...overrides };
  render(<AppHeader {...props} />);
  return props;
}

describe('AppHeader', () => {
  it('renders project status and dispatches terraform commands', () => {
    const props = renderHeader();

    expect(screen.getByRole('textbox', { name: 'Project name' })).toHaveValue('demo');
    expect(screen.getByText('2 resources')).toBeInTheDocument();
    expect(screen.getByText('● live')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Init/i }));
    fireEvent.click(screen.getByRole('button', { name: /Plan/i }));
    fireEvent.click(screen.getByRole('button', { name: /Apply/i }));

    expect(props.onRunCommand).toHaveBeenNthCalledWith(1, 'init');
    expect(props.onRunCommand).toHaveBeenNthCalledWith(2, 'plan');
    expect(props.onRunCommand).toHaveBeenNthCalledWith(3, 'apply');
  });

  it('uses ansible command names and keeps disabled redo disabled', () => {
    const props = renderHeader({
      tool: 'ansible',
      toolMeta: { color: '#f97316', icon: 'AN', name: 'Ansible' },
      resourceCount: 1,
      wsConnected: false,
    });

    expect(screen.getByText('1 resource')).toBeInTheDocument();
    expect(screen.getByText('● offline')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Redo' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: /Check/i }));
    fireEvent.click(screen.getByRole('button', { name: /Syntax/i }));
    fireEvent.click(screen.getByRole('button', { name: /Apply/i }));

    expect(props.onRunCommand).toHaveBeenNthCalledWith(1, 'check');
    expect(props.onRunCommand).toHaveBeenNthCalledWith(2, 'syntax');
    expect(props.onRunCommand).toHaveBeenNthCalledWith(3, 'playbook');
  });

  it('dispatches navigation, project, reveal, undo, and settings actions', () => {
    const props = renderHeader();

    fireEvent.click(screen.getByRole('button', { name: 'Back to projects' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Project name' }), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByTitle('Open in file manager'));
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    fireEvent.click(screen.getByRole('button', { name: 'SETTINGS' }));

    expect(props.onBack).toHaveBeenCalled();
    expect(props.onProjectNameChange).toHaveBeenCalledWith('renamed');
    expect(props.onRevealProject).toHaveBeenCalledWith('demo');
    expect(props.onUndo).toHaveBeenCalled();
    expect(props.onOpenSettings).toHaveBeenCalled();
  });
});
