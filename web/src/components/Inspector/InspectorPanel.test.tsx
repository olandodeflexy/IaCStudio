import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { Edge } from '../../legacy';
import { InspectorPanel, type InspectorPanelProps, type InspectorResource } from './index';

vi.mock('../CodeEditor', () => ({
  CodeEditor: ({ value, filePath, onChange, onSave }: any) => (
    <div>
      <textarea
        aria-label="Code editor"
        data-filepath={filePath}
        value={value}
        onChange={event => onChange?.(event.target.value)}
      />
      <button onClick={() => onSave?.(value)}>Editor Save</button>
    </div>
  ),
}));

vi.mock('../CloudConnections', () => ({
  CloudConnectionsPanel: () => (
    <div>Cloud connections panel</div>
  ),
}));

vi.mock('../PolicyStudio', () => ({
  PolicyStudioPanel: ({ projectName, tool, env }: any) => (
    <div>Policy panel {projectName} {tool} {env}</div>
  ),
}));

vi.mock('../ScanPanel', () => ({
  ScanPanel: ({ projectName, tool }: any) => (
    <div>Scan panel {projectName} {tool}</div>
  ),
}));

vi.mock('../ModuleRegistry', () => ({
  ModuleRegistryPanel: ({ initialQuery }: any) => (
    <div>Module registry {initialQuery}</div>
  ),
}));

const nodes: InspectorResource[] = [
  {
    id: 'vpc',
    type: 'aws_vpc',
    name: 'main',
    properties: { cidr_block: '10.0.0.0/16', enable_dns_hostnames: true },
    icon: 'V',
    label: 'VPC',
  },
  {
    id: 'subnet',
    type: 'aws_subnet',
    name: 'app',
    properties: { cidr_block: '10.0.1.0/24' },
    icon: 'S',
    label: 'Subnet',
  },
];

const edges: Edge[] = [
  {
    id: 'subnet->vpc:vpc_id',
    from: 'subnet',
    to: 'vpc',
    fromType: 'aws_subnet',
    toType: 'aws_vpc',
    field: 'vpc_id',
    label: 'vpc id',
  },
];

function baseProps(): InspectorPanelProps {
  return {
    width: 320,
    activeTab: 'inspect',
    tool: 'terraform',
    toolMeta: { color: '#2FB5A8', ext: '.tf' },
    activeEnv: null,
    projectId: 'demo',
    nodes,
    edges,
    selectedNodeId: null,
    selectedEdgeId: null,
    syncCode: 'resource "aws_vpc" "main" {}',
    codeFileLabel: 'main.tf',
    codeEditorFilePath: 'main.tf',
    codeSaving: false,
    unresolvedHybridEnv: false,
    onTabChange: vi.fn(),
    onDeleteEdge: vi.fn(),
    onSelectEdge: vi.fn(),
    onUpdateNodeName: vi.fn(),
    onUpdateNodeProp: vi.fn(),
    onSyncCodeChange: vi.fn(),
    onSaveCode: vi.fn(),
    onCopyCode: vi.fn(),
  };
}

function renderInspector(overrides: Partial<InspectorPanelProps> = {}) {
  const props = { ...baseProps(), ...overrides };
  render(<InspectorPanel {...props} />);
  return props;
}

describe('InspectorPanel', () => {
  it('renders selected node properties and dispatches edits', () => {
    const props = renderInspector({ selectedNodeId: 'vpc' });

    expect(screen.getByText('V Properties')).toBeInTheDocument();

    const booleanToggle = screen.getByLabelText('enable_dns_hostnames');

    expect(booleanToggle).toHaveAttribute('aria-pressed', 'true');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'core' } });
    fireEvent.change(screen.getByLabelText('cidr_block'), { target: { value: '10.1.0.0/16' } });
    fireEvent.click(booleanToggle);
    fireEvent.click(screen.getByRole('button', { name: /app.*vpc_id/ }));

    expect(props.onUpdateNodeName).toHaveBeenCalledWith('vpc', 'core');
    expect(props.onUpdateNodeProp).toHaveBeenCalledWith('vpc', 'cidr_block', '10.1.0.0/16');
    expect(props.onUpdateNodeProp).toHaveBeenCalledWith('vpc', 'enable_dns_hostnames', false);
    expect(props.onSelectEdge).toHaveBeenCalledWith('subnet->vpc:vpc_id');
  });

  it('renders selected edge details and dispatches deletion', () => {
    const props = renderInspector({ selectedEdgeId: 'subnet->vpc:vpc_id' });

    expect(screen.getByText('🔗 Connection')).toBeInTheDocument();
    expect(screen.getByText('vpc_id = aws_vpc.main.id')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Delete Connection' }));

    expect(props.onDeleteEdge).toHaveBeenCalledWith('subnet->vpc:vpc_id');
  });

  it('renders code controls and dispatches code callbacks', () => {
    const props = renderInspector();

    expect(screen.getByText('FILE main.tf')).toBeInTheDocument();
    expect(screen.getByLabelText('Code editor')).toHaveAttribute('data-filepath', 'main.tf');

    fireEvent.change(screen.getByLabelText('Code editor'), { target: { value: 'resource "aws_s3_bucket" "logs" {}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    fireEvent.click(screen.getByRole('button', { name: 'Policy' }));

    expect(props.onSyncCodeChange).toHaveBeenCalledWith('resource "aws_s3_bucket" "logs" {}');
    expect(props.onSaveCode).toHaveBeenCalledWith('resource "aws_vpc" "main" {}');
    expect(props.onCopyCode).toHaveBeenCalledWith('resource "aws_vpc" "main" {}');
    expect(props.onTabChange).toHaveBeenCalledWith('policy');
  });

  it('keeps editor save disabled when the save button is disabled', () => {
    const props = renderInspector({ unresolvedHybridEnv: true });

    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Editor Save' }));

    expect(props.onSaveCode).not.toHaveBeenCalled();
  });

  it('handles clipboard fallback failures without unhandled rejections', async () => {
    const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });

    try {
      renderInspector({ onCopyCode: undefined });

      fireEvent.click(screen.getByRole('button', { name: 'Copy' }));

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith('resource "aws_vpc" "main" {}');
      });
      await waitFor(() => {
        expect(warn).toHaveBeenCalledWith('Failed to copy code to clipboard', expect.any(Error));
      });
    } finally {
      warn.mockRestore();
      if (originalClipboard) {
        Object.defineProperty(navigator, 'clipboard', originalClipboard);
      } else {
        Reflect.deleteProperty(navigator, 'clipboard');
      }
    }
  });

  it('renders tool tabs and mounted panels', () => {
    renderInspector({ activeTab: 'modules' });

    expect(screen.getByRole('button', { name: 'Cloud' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Modules' })).toBeInTheDocument();
    expect(screen.getByText('Module registry vpc')).toBeInTheDocument();
  });

  it('renders the cloud connections tab', () => {
    renderInspector({ activeTab: 'cloud' });

    expect(screen.getByText('Cloud connections panel')).toBeInTheDocument();
  });

  it('guards unresolved hybrid environments', () => {
    renderInspector({
      activeTab: 'policy',
      tool: 'multi',
      activeEnv: 'qa',
      unresolvedHybridEnv: true,
    });

    expect(screen.getByText('Environment "qa" has no configured IaC tool in .iac-studio.json.')).toBeInTheDocument();
  });

  it('hides modules for Ansible projects', () => {
    renderInspector({ tool: 'ansible' });

    expect(screen.queryByRole('button', { name: 'Modules' })).not.toBeInTheDocument();
  });
});
