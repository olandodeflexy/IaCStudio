import { describe, expect, it } from 'vitest';

import type { Resource } from '../../types';

import { buildSwimlaneLayout, cellKey, groupResourcesByCell, LAYOUT } from './swimlaneLayout';

const r = (id: string, type: string): Resource => ({
  id,
  type,
  name: id,
  properties: {},
});

describe('buildSwimlaneLayout', () => {
  it('emits one header + one column per environment', () => {
    const { nodes } = buildSwimlaneLayout({
      environments: ['dev', 'stage', 'prod'],
      modules: ['network'],
      cells: {},
    });

    const headers = nodes.filter((n) => n.type === 'envHeader');
    const columns = nodes.filter((n) => n.type === 'envColumn');
    expect(headers.map((n) => n.data.label)).toEqual(['dev', 'stage', 'prod']);
    expect(columns.map((n) => (n.data as { environment: string }).environment)).toEqual([
      'dev',
      'stage',
      'prod',
    ]);
  });

  it('places environment headers at incrementing x positions', () => {
    const { nodes } = buildSwimlaneLayout({
      environments: ['dev', 'prod'],
      modules: ['network'],
      cells: {},
    });

    const headers = nodes.filter((n) => n.type === 'envHeader');
    expect(headers[0].position).toEqual({ x: LAYOUT.gutterWidth, y: 0 });
    expect(headers[1].position).toEqual({
      x: LAYOUT.gutterWidth + LAYOUT.envWidth + LAYOUT.envGap,
      y: 0,
    });
  });

  it('places module labels stacked in the left gutter', () => {
    const { nodes } = buildSwimlaneLayout({
      environments: ['dev'],
      modules: ['network', 'compute', 'data'],
      cells: {},
    });

    const labels = nodes.filter((n) => n.type === 'moduleLabel');
    expect(labels.map((n) => n.data.label)).toEqual(['network', 'compute', 'data']);
    expect(labels[0].position).toEqual({ x: 0, y: LAYOUT.headerHeight });
    expect(labels[1].position.y).toBe(LAYOUT.headerHeight + LAYOUT.rowHeight + LAYOUT.rowGap);
  });

  it('packs resources inside the correct (env, module) cell', () => {
    const vpc = r('vpc-1', 'aws_vpc');
    const subnet = r('subnet-1', 'aws_subnet');
    const { nodes } = buildSwimlaneLayout({
      environments: ['dev', 'prod'],
      modules: ['network'],
      cells: {
        [cellKey('prod', 'network')]: [vpc, subnet],
      },
    });

    const resNodes = nodes.filter((n) => n.type === 'resource');
    expect(resNodes).toHaveLength(2);

    const prodColumnX = LAYOUT.gutterWidth + LAYOUT.envWidth + LAYOUT.envGap;
    expect(resNodes[0].position.x).toBe(prodColumnX + LAYOUT.padding);
    expect(resNodes[0].position.y).toBe(LAYOUT.headerHeight + LAYOUT.padding);

    // Second resource packs to the right of the first (grid fills
    // horizontally until perRow is exhausted, then wraps).
    expect(resNodes[1].position.x).toBe(
      prodColumnX + LAYOUT.padding + LAYOUT.resourceWidth + LAYOUT.resourceGap,
    );
    expect(resNodes[1].position.y).toBe(resNodes[0].position.y);
  });

  it('does not emit resource nodes for empty cells', () => {
    const { nodes } = buildSwimlaneLayout({
      environments: ['dev'],
      modules: ['network', 'compute'],
      cells: {
        [cellKey('dev', 'network')]: [r('vpc-1', 'aws_vpc')],
      },
    });
    expect(nodes.filter((n) => n.type === 'resource')).toHaveLength(1);
  });
});

describe('groupResourcesByCell', () => {
  it('groups via the classifier callback', () => {
    const resources = [
      { ...r('a', 'aws_vpc'), file: 'environments/dev/network.tf' },
      { ...r('b', 'aws_instance'), file: 'environments/prod/compute.tf' },
    ];
    const grouped = groupResourcesByCell(resources, (res) => {
      const parts = res.file?.split('/') ?? [];
      if (parts[0] !== 'environments') return null;
      return { environment: parts[1], module: parts[2].replace(/\.tf$/, '') };
    });
    expect(grouped[cellKey('dev', 'network')]).toHaveLength(1);
    expect(grouped[cellKey('prod', 'compute')]).toHaveLength(1);
  });

  it('skips resources whose classifier returns null', () => {
    const resources = [
      { ...r('a', 'aws_vpc'), file: 'rogue.tf' },
      { ...r('b', 'aws_instance'), file: 'environments/dev/compute.tf' },
    ];
    const grouped = groupResourcesByCell(resources, (res) =>
      res.file?.startsWith('environments/') ? { environment: 'dev', module: 'compute' } : null,
    );
    expect(Object.values(grouped).flat()).toHaveLength(1);
  });
});
