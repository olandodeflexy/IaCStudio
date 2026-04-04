package templates

import (
	"github.com/iac-studio/iac-studio/internal/parser"
)

// Template is a reusable infrastructure pattern.
type Template struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Icon        string            `json:"icon"`
	Category    string            `json:"category"`
	Tool        string            `json:"tool"` // terraform | ansible | all
	Resources   []parser.Resource `json:"resources"`
	Connections []TemplateConn    `json:"connections"`
}

// TemplateConn defines a connection between resources within a template.
type TemplateConn struct {
	FromIndex int    `json:"from_index"` // Index into Resources
	ToIndex   int    `json:"to_index"`
	Field     string `json:"field"`
}

// GetTemplates returns all available templates for a tool.
func GetTemplates(tool string) []Template {
	all := allTemplates()
	var filtered []Template
	for _, t := range all {
		if t.Tool == tool || t.Tool == "all" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func allTemplates() []Template {
	return []Template{
		// ─── Networking ───
		{
			ID: "vpc-public-private", Name: "VPC with Public & Private Subnets",
			Description: "Production-ready VPC with public subnets (NAT gateway) and private subnets across 2 AZs.",
			Icon: "🌐", Category: "Networking", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_vpc", Type: "aws_vpc", Name: "main", Properties: map[string]any{
					"cidr_block": "10.0.0.0/16", "enable_dns_support": true, "enable_dns_hostnames": true,
				}},
				{ID: "t_pub_1", Type: "aws_subnet", Name: "public_1", Properties: map[string]any{
					"cidr_block": "10.0.1.0/24", "availability_zone": "us-east-1a", "map_public_ip_on_launch": true,
				}},
				{ID: "t_pub_2", Type: "aws_subnet", Name: "public_2", Properties: map[string]any{
					"cidr_block": "10.0.2.0/24", "availability_zone": "us-east-1b", "map_public_ip_on_launch": true,
				}},
				{ID: "t_priv_1", Type: "aws_subnet", Name: "private_1", Properties: map[string]any{
					"cidr_block": "10.0.10.0/24", "availability_zone": "us-east-1a",
				}},
				{ID: "t_priv_2", Type: "aws_subnet", Name: "private_2", Properties: map[string]any{
					"cidr_block": "10.0.11.0/24", "availability_zone": "us-east-1b",
				}},
				{ID: "t_igw", Type: "aws_internet_gateway", Name: "main", Properties: map[string]any{}},
				{ID: "t_eip", Type: "aws_eip", Name: "nat", Properties: map[string]any{"domain": "vpc"}},
				{ID: "t_nat", Type: "aws_nat_gateway", Name: "main", Properties: map[string]any{}},
			},
			Connections: []TemplateConn{
				{FromIndex: 1, ToIndex: 0, Field: "vpc_id"},
				{FromIndex: 2, ToIndex: 0, Field: "vpc_id"},
				{FromIndex: 3, ToIndex: 0, Field: "vpc_id"},
				{FromIndex: 4, ToIndex: 0, Field: "vpc_id"},
				{FromIndex: 5, ToIndex: 0, Field: "vpc_id"},
				{FromIndex: 7, ToIndex: 1, Field: "subnet_id"},
				{FromIndex: 7, ToIndex: 6, Field: "allocation_id"},
			},
		},

		// ─── Web Application ───
		{
			ID: "web-app-alb", Name: "Web Application with ALB",
			Description: "EC2 instances behind an Application Load Balancer with security groups.",
			Icon: "🌍", Category: "Web", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_sg_alb", Type: "aws_security_group", Name: "alb_sg", Properties: map[string]any{
					"name": "alb-sg", "description": "Allow HTTP/HTTPS to ALB",
				}},
				{ID: "t_sg_app", Type: "aws_security_group", Name: "app_sg", Properties: map[string]any{
					"name": "app-sg", "description": "Allow traffic from ALB only",
				}},
				{ID: "t_alb", Type: "aws_lb", Name: "web", Properties: map[string]any{
					"name": "web-alb", "internal": false, "load_balancer_type": "application",
				}},
				{ID: "t_tg", Type: "aws_lb_target_group", Name: "web", Properties: map[string]any{
					"name": "web-tg", "port": 80, "protocol": "HTTP",
				}},
				{ID: "t_ec2_1", Type: "aws_instance", Name: "web_1", Properties: map[string]any{
					"ami": "ami-0c55b159cbfafe1f0", "instance_type": "t3.small",
				}},
				{ID: "t_ec2_2", Type: "aws_instance", Name: "web_2", Properties: map[string]any{
					"ami": "ami-0c55b159cbfafe1f0", "instance_type": "t3.small",
				}},
			},
			Connections: []TemplateConn{
				{FromIndex: 2, ToIndex: 0, Field: "security_groups"},
				{FromIndex: 4, ToIndex: 1, Field: "vpc_security_group_ids"},
				{FromIndex: 5, ToIndex: 1, Field: "vpc_security_group_ids"},
			},
		},

		// ─── Database ───
		{
			ID: "rds-ha", Name: "High-Availability RDS",
			Description: "Multi-AZ PostgreSQL RDS with a DB subnet group, security group, and parameter group.",
			Icon: "🗄️", Category: "Database", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_db_sg", Type: "aws_security_group", Name: "db_sg", Properties: map[string]any{
					"name": "db-sg", "description": "PostgreSQL access",
				}},
				{ID: "t_rds", Type: "aws_rds_instance", Name: "main", Properties: map[string]any{
					"engine": "postgres", "engine_version": "15.4", "instance_class": "db.r5.large",
					"allocated_storage": 100, "multi_az": true, "storage_encrypted": true,
					"skip_final_snapshot": false, "backup_retention_period": 7, "username": "admin",
				}},
			},
			Connections: []TemplateConn{
				{FromIndex: 1, ToIndex: 0, Field: "vpc_security_group_ids"},
			},
		},

		// ─── Serverless ───
		{
			ID: "serverless-api", Name: "Serverless REST API",
			Description: "Lambda function with API Gateway, IAM role, and CloudWatch logs.",
			Icon: "⚡", Category: "Serverless", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_role", Type: "aws_iam_role", Name: "lambda_role", Properties: map[string]any{
					"name": "lambda-execution-role",
				}},
				{ID: "t_lambda", Type: "aws_lambda_function", Name: "api", Properties: map[string]any{
					"function_name": "api-handler", "runtime": "nodejs20.x", "handler": "index.handler",
					"memory_size": 256, "timeout": 30,
				}},
				{ID: "t_logs", Type: "aws_cloudwatch_log_group", Name: "api_logs", Properties: map[string]any{
					"name": "/aws/lambda/api-handler", "retention_in_days": 14,
				}},
			},
			Connections: []TemplateConn{
				{FromIndex: 1, ToIndex: 0, Field: "role"},
			},
		},

		// ─── Static Site ───
		{
			ID: "static-site", Name: "Static Website (S3 + CloudFront)",
			Description: "S3 bucket for hosting with CloudFront CDN distribution.",
			Icon: "🌎", Category: "Web", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_bucket", Type: "aws_s3_bucket", Name: "site", Properties: map[string]any{
					"bucket": "my-static-site",
				}},
				{ID: "t_oai", Type: "aws_iam_policy", Name: "s3_read", Properties: map[string]any{
					"name": "cloudfront-s3-read", "description": "Allow CloudFront to read S3",
				}},
			},
		},

		// ─── Monitoring ───
		{
			ID: "monitoring-stack", Name: "Monitoring & Alerting",
			Description: "CloudWatch alarms, SNS topic for alerts, and log groups.",
			Icon: "📊", Category: "Monitoring", Tool: "terraform",
			Resources: []parser.Resource{
				{ID: "t_topic", Type: "aws_sns_topic", Name: "alerts", Properties: map[string]any{
					"name": "infrastructure-alerts",
				}},
				{ID: "t_logs", Type: "aws_cloudwatch_log_group", Name: "app", Properties: map[string]any{
					"name": "/app/production", "retention_in_days": 30,
				}},
			},
		},

		// ─── Ansible Templates ───
		{
			ID: "ansible-webserver", Name: "NGINX Web Server",
			Description: "Install and configure NGINX with a custom site config.",
			Icon: "🌐", Category: "Web", Tool: "ansible",
			Resources: []parser.Resource{
				{ID: "t_apt", Type: "apt", Name: "Install nginx", Properties: map[string]any{
					"name": "nginx", "state": "present", "update_cache": true,
				}},
				{ID: "t_copy", Type: "template", Name: "Deploy site config", Properties: map[string]any{
					"src": "templates/nginx-site.conf.j2", "dest": "/etc/nginx/sites-available/default",
				}},
				{ID: "t_svc", Type: "service", Name: "Start nginx", Properties: map[string]any{
					"name": "nginx", "state": "started", "enabled": true,
				}},
			},
		},
		{
			ID: "ansible-docker", Name: "Docker Host Setup",
			Description: "Install Docker, configure daemon, and deploy a container.",
			Icon: "🐳", Category: "Containers", Tool: "ansible",
			Resources: []parser.Resource{
				{ID: "t_deps", Type: "apt", Name: "Install Docker deps", Properties: map[string]any{
					"name": "docker.io", "state": "present", "update_cache": true,
				}},
				{ID: "t_svc", Type: "service", Name: "Start Docker", Properties: map[string]any{
					"name": "docker", "state": "started", "enabled": true,
				}},
				{ID: "t_user", Type: "user", Name: "Add deploy to docker group", Properties: map[string]any{
					"name": "deploy", "groups": "docker", "append": true,
				}},
				{ID: "t_app", Type: "docker_container", Name: "Run application", Properties: map[string]any{
					"name": "webapp", "image": "nginx:latest", "ports": "80:80",
					"restart_policy": "unless-stopped",
				}},
			},
		},
		{
			ID: "ansible-hardening", Name: "Server Hardening",
			Description: "Basic security hardening: firewall, SSH config, fail2ban, unattended upgrades.",
			Icon: "🔒", Category: "Security", Tool: "ansible",
			Resources: []parser.Resource{
				{ID: "t_ufw_ssh", Type: "ufw", Name: "Allow SSH", Properties: map[string]any{
					"rule": "allow", "port": "22", "proto": "tcp",
				}},
				{ID: "t_ufw_http", Type: "ufw", Name: "Allow HTTP", Properties: map[string]any{
					"rule": "allow", "port": "80", "proto": "tcp",
				}},
				{ID: "t_ufw_https", Type: "ufw", Name: "Allow HTTPS", Properties: map[string]any{
					"rule": "allow", "port": "443", "proto": "tcp",
				}},
				{ID: "t_f2b", Type: "apt", Name: "Install fail2ban", Properties: map[string]any{
					"name": "fail2ban", "state": "present",
				}},
				{ID: "t_f2b_svc", Type: "service", Name: "Start fail2ban", Properties: map[string]any{
					"name": "fail2ban", "state": "started", "enabled": true,
				}},
				{ID: "t_upgrades", Type: "apt", Name: "Install unattended-upgrades", Properties: map[string]any{
					"name": "unattended-upgrades", "state": "present",
				}},
			},
		},
	}
}
