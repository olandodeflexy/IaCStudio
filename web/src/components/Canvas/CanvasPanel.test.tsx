import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { RefObject } from 'react';

import type { Edge } from '../../legacy';
import type { LayeredProject } from '../../types';

import { CanvasPanel, type CanvasPanelProps, type CanvasResource } from './index';

const nodes: CanvasResource[] = [
  {
    id: 'vpc',
    type: 'aws_vpc',
    name: 'main',
    properties: {},
    x: 100,
    y: 80,
    icon: 'V',
    label: 'VPC',
  },
  {
    id: 'subnet',
    type: 'aws_subnet',
    name: 'app',
    properties: {},
    x: 180,
    y: 200,
    icon: 'S',
    label: 'Subnet',
  },
];

const edges: Edge[] = [
  {
    id: 'vpc->subnet:vpc_id',
    from: 'vpc',
    to: 'subnet',
    fromType: 'aws_vpc',
    toType: 'aws_subnet',
    field: 'vpc_id',
    label: 'vpc id',
  },
];

const layeredProject: LayeredProject = {
  layout: 'layered-v1',
  environments: ['dev', 'prod'],
  environmentTools: { dev: 'pulumi', prod: 'terraform' },
  modules: [{ name: 'root', path: 'environments', environments: ['dev', 'prod'] }],
};

function makeCanvasRef(): RefObject<HTMLDivElement> {
  const element = document.createElement('div');
  element.getBoundingClientRect = vi.fn(() => ({
    left: 10,
    top: 20,
    right: 510,
    bottom: 420,
    width: 500,
    height: 400,
    x: 10,
    y: 20,
    toJSON: () => ({}),
  }));
  return { current: element };
}

function baseProps(): CanvasPanelProps {
  return {
    canvasRef: makeCanvasRef(),
    nodes,
    edges,
    selectedNodeId: null,
    selectedEdgeId: null,
    connecting: null,
    layeredProject: null,
    showEnvironmentSelector: false,
    activeEnvironment: null,
    canvasMode: 'freeform',
    isSwimlaneMode: false,
    toolMeta: { color: '#2FB5A8' },
    onMouseMove: vi.fn(),
    onDragEnd: vi.fn(),
    onConnectionCancel: vi.fn(),
    onNodeDragStart: vi.fn(),
    onStartConnection: vi.fn(),
    onCompleteConnection: vi.fn(),
    onSelectNode: vi.fn(),
    onSelectEdge: vi.fn(),
    onClearSelection: vi.fn(),
    onDeleteNode: vi.fn(),
    onActiveEnvironmentChange: vi.fn(),
    onCanvasModeChange: vi.fn(),
  };
}

function renderCanvas(overrides: Partial<CanvasPanelProps> = {}) {
  const props = { ...baseProps(), ...overrides };
  render(<CanvasPanel {...props} />);
  return props;
}

describe('CanvasPanel', () => {
  it('renders freeform nodes and dispatches node, edge, and connection actions', () => {
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      left: 10,
      top: 20,
      right: 510,
      bottom: 420,
      width: 500,
      height: 400,
      x: 10,
      y: 20,
      toJSON: () => ({}),
    });
    const props = renderCanvas();

    try {
      fireEvent.click(screen.getByText('VPC'));
      fireEvent.keyDown(screen.getByRole('button', { name: 'Select connection vpc_id' }), { key: 'Enter' });
      fireEvent.click(screen.getByRole('button', { name: 'Delete VPC' }));
      fireEvent.mouseDown(screen.getByRole('button', { name: 'Start connection from VPC' }), { clientX: 110, clientY: 140 });
      fireEvent.mouseUp(screen.getByText('Subnet'));

      expect(props.onSelectNode).toHaveBeenCalledWith('vpc');
      expect(props.onSelectEdge).toHaveBeenCalledWith('vpc->subnet:vpc_id');
      expect(props.onDeleteNode).toHaveBeenCalledWith('vpc');
      expect(props.onStartConnection).toHaveBeenCalledWith('vpc', { x: 100, y: 120 });
      expect(props.onCompleteConnection).toHaveBeenCalledWith('subnet');
    } finally {
      rect.mockRestore();
    }
  });

  it('renders the empty state and cancels in-progress connection drags on canvas mouseup', () => {
    const props = renderCanvas({
      nodes: [],
      edges: [],
      connecting: { fromId: 'missing', x: 40, y: 50 },
    });

    expect(screen.getByText('Drag resources from the palette')).toBeInTheDocument();

    fireEvent.mouseUp(screen.getByRole('main'));
    fireEvent.click(screen.getByRole('main'));

    expect(props.onDragEnd).toHaveBeenCalled();
    expect(props.onConnectionCancel).toHaveBeenCalled();
    expect(props.onClearSelection).toHaveBeenCalled();
  });

  it('dispatches layered environment and canvas mode changes', () => {
    const props = renderCanvas({
      layeredProject,
      showEnvironmentSelector: true,
      activeEnvironment: 'prod',
      canvasMode: 'swimlane',
    });

    fireEvent.click(screen.getByRole('button', { name: 'dev (pulumi)' }));
    fireEvent.click(screen.getByRole('button', { name: 'Freeform' }));

    expect(props.onActiveEnvironmentChange).toHaveBeenCalledWith('dev');
    expect(props.onCanvasModeChange).toHaveBeenCalledWith('freeform');
  });
});
