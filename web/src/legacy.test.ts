import { describe, expect, it } from 'vitest';

import { generateLocalCode, type Edge } from './legacy';

describe('generateLocalCode pulumi preview', () => {
  it('uses guessed provider packages for fallback Pulumi resource types', () => {
    const code = generateLocalCode('pulumi', [
      {
        id: 'table-item',
        type: 'aws_dynamodb_table_item',
        name: 'item',
        properties: { hash_key: 'id' },
      },
    ], []);

    expect(code).toContain('new (aws as any).dynamodb.DynamodbTableItem("item"');
    expect(code).not.toContain('.resources.');
  });

  it('renders edge references before literal Pulumi properties', () => {
    const edges: Edge[] = [{
      id: 'subnet->vpc:vpc_id',
      from: 'subnet',
      to: 'vpc',
      fromType: 'aws_subnet',
      toType: 'aws_vpc',
      field: 'vpc_id',
      label: 'VPC',
    }];

    const code = generateLocalCode('pulumi', [
      {
        id: 'vpc',
        type: 'aws_vpc',
        name: 'main',
        properties: { cidr_block: '10.0.0.0/16' },
      },
      {
        id: 'subnet',
        type: 'aws_subnet',
        name: 'app',
        properties: { cidr_block: '10.0.1.0/24', vpc_id: 'literal-vpc-id' },
      },
    ], edges);

    expect(code).toContain('vpcId: main.id');
    expect(code).not.toContain('vpcId: "literal-vpc-id"');
  });
});
