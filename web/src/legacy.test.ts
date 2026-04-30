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

  it('renders repeated plural Pulumi edges as arrays', () => {
    const edges: Edge[] = [
      {
        id: 'app->web:vpc_security_group_ids',
        from: 'app',
        to: 'web',
        fromType: 'aws_instance',
        toType: 'aws_security_group',
        field: 'vpc_security_group_ids',
        label: 'Security group',
      },
      {
        id: 'app->admin:vpc_security_group_ids',
        from: 'app',
        to: 'admin',
        fromType: 'aws_instance',
        toType: 'aws_security_group',
        field: 'vpc_security_group_ids',
        label: 'Security group',
      },
    ];

    const code = generateLocalCode('pulumi', [
      {
        id: 'web',
        type: 'aws_security_group',
        name: 'web',
        properties: {},
      },
      {
        id: 'admin',
        type: 'aws_security_group',
        name: 'admin',
        properties: {},
      },
      {
        id: 'app',
        type: 'aws_instance',
        name: 'app',
        properties: { ami: 'ami-123', vpc_security_group_ids: ['sg-literal'] },
      },
    ], edges);

    expect(code).toContain('vpcSecurityGroupIds: [web.id, admin.id]');
    expect(code).not.toContain('vpcSecurityGroupIds: ["sg-literal"]');
  });
});
