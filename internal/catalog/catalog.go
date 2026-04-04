package catalog

// Resource describes a single IaC resource type with its metadata,
// default properties, and connection rules.
type Resource struct {
	Type        string            `json:"type"`
	Label       string            `json:"label"`
	Icon        string            `json:"icon"`
	Category    string            `json:"category"`
	Provider    string            `json:"provider"`
	Defaults    map[string]any    `json:"defaults"`
	Fields      []Field           `json:"fields"`
	ConnectsTo  []string          `json:"connects_to"`  // Resource types this can reference
	ConnectsVia map[string]string `json:"connects_via"` // field -> target resource type
}

// Field describes a single property on a resource.
type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | bool | number | select
	Required    bool     `json:"required"`
	Default     any      `json:"default"`
	Description string   `json:"description"`
	Options     []string `json:"options,omitempty"` // For select type
}

// Catalog holds all resource definitions for a tool.
type Catalog struct {
	Tool      string     `json:"tool"`
	Resources []Resource `json:"resources"`
}

// GetCatalog returns the full resource catalog for a tool.
func GetCatalog(tool string) Catalog {
	switch tool {
	case "terraform", "opentofu":
		return terraformCatalog()
	case "ansible":
		return ansibleCatalog()
	default:
		return terraformCatalog()
	}
}

func terraformCatalog() Catalog {
	return Catalog{
		Tool: "terraform",
		Resources: []Resource{
			// ─── Networking ───
			{
				Type: "aws_vpc", Label: "VPC", Icon: "🌐", Category: "Networking", Provider: "aws",
				Defaults: map[string]any{"cidr_block": "10.0.0.0/16", "enable_dns_support": true, "enable_dns_hostnames": true},
				Fields: []Field{
					{Name: "cidr_block", Type: "string", Required: true, Default: "10.0.0.0/16", Description: "The CIDR block for the VPC"},
					{Name: "enable_dns_support", Type: "bool", Default: true, Description: "Enable DNS support in the VPC"},
					{Name: "enable_dns_hostnames", Type: "bool", Default: true, Description: "Enable DNS hostnames in the VPC"},
					{Name: "instance_tenancy", Type: "select", Default: "default", Options: []string{"default", "dedicated", "host"}},
				},
				ConnectsTo: []string{},
			},
			{
				Type: "aws_subnet", Label: "Subnet", Icon: "📡", Category: "Networking", Provider: "aws",
				Defaults: map[string]any{"cidr_block": "10.0.1.0/24", "availability_zone": "us-east-1a", "map_public_ip_on_launch": false},
				Fields: []Field{
					{Name: "cidr_block", Type: "string", Required: true, Default: "10.0.1.0/24"},
					{Name: "availability_zone", Type: "select", Default: "us-east-1a", Options: []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-west-2a", "us-west-2b", "eu-west-1a", "eu-west-1b"}},
					{Name: "map_public_ip_on_launch", Type: "bool", Default: false},
				},
				ConnectsTo:  []string{"aws_vpc"},
				ConnectsVia: map[string]string{"vpc_id": "aws_vpc"},
			},
			{
				Type: "aws_internet_gateway", Label: "Internet Gateway", Icon: "🌍", Category: "Networking", Provider: "aws",
				Defaults:    map[string]any{},
				ConnectsTo:  []string{"aws_vpc"},
				ConnectsVia: map[string]string{"vpc_id": "aws_vpc"},
			},
			{
				Type: "aws_nat_gateway", Label: "NAT Gateway", Icon: "🔄", Category: "Networking", Provider: "aws",
				Defaults:    map[string]any{"allocation_id": "", "connectivity_type": "public"},
				ConnectsTo:  []string{"aws_subnet", "aws_eip"},
				ConnectsVia: map[string]string{"subnet_id": "aws_subnet", "allocation_id": "aws_eip"},
			},
			{
				Type: "aws_route_table", Label: "Route Table", Icon: "🗺️", Category: "Networking", Provider: "aws",
				Defaults:    map[string]any{},
				ConnectsTo:  []string{"aws_vpc"},
				ConnectsVia: map[string]string{"vpc_id": "aws_vpc"},
			},
			{
				Type: "aws_eip", Label: "Elastic IP", Icon: "📌", Category: "Networking", Provider: "aws",
				Defaults: map[string]any{"domain": "vpc"},
			},

			// ─── Compute ───
			{
				Type: "aws_instance", Label: "EC2 Instance", Icon: "🖥️", Category: "Compute", Provider: "aws",
				Defaults: map[string]any{"ami": "ami-0c55b159cbfafe1f0", "instance_type": "t3.micro", "associate_public_ip_address": false},
				Fields: []Field{
					{Name: "ami", Type: "string", Required: true, Default: "ami-0c55b159cbfafe1f0", Description: "AMI ID"},
					{Name: "instance_type", Type: "select", Required: true, Default: "t3.micro",
						Options: []string{"t3.nano", "t3.micro", "t3.small", "t3.medium", "t3.large", "t3.xlarge", "m5.large", "m5.xlarge", "c5.large", "r5.large"}},
					{Name: "associate_public_ip_address", Type: "bool", Default: false},
					{Name: "key_name", Type: "string", Description: "SSH key pair name"},
				},
				ConnectsTo:  []string{"aws_subnet", "aws_security_group"},
				ConnectsVia: map[string]string{"subnet_id": "aws_subnet", "vpc_security_group_ids": "aws_security_group"},
			},
			{
				Type: "aws_lambda_function", Label: "Lambda Function", Icon: "⚡", Category: "Compute", Provider: "aws",
				Defaults: map[string]any{"function_name": "my_function", "runtime": "nodejs18.x", "handler": "index.handler", "memory_size": 128, "timeout": 30},
				Fields: []Field{
					{Name: "function_name", Type: "string", Required: true},
					{Name: "runtime", Type: "select", Required: true, Default: "nodejs18.x",
						Options: []string{"nodejs18.x", "nodejs20.x", "python3.11", "python3.12", "go1.x", "java17", "ruby3.2", "dotnet6"}},
					{Name: "handler", Type: "string", Required: true, Default: "index.handler"},
					{Name: "memory_size", Type: "number", Default: 128},
					{Name: "timeout", Type: "number", Default: 30},
				},
				ConnectsTo:  []string{"aws_iam_role"},
				ConnectsVia: map[string]string{"role": "aws_iam_role"},
			},
			{
				Type: "aws_ecs_cluster", Label: "ECS Cluster", Icon: "🚢", Category: "Compute", Provider: "aws",
				Defaults: map[string]any{"name": "my-cluster"},
			},
			{
				Type: "aws_launch_template", Label: "Launch Template", Icon: "📋", Category: "Compute", Provider: "aws",
				Defaults: map[string]any{"name_prefix": "lt-", "image_id": "ami-0c55b159cbfafe1f0", "instance_type": "t3.micro"},
			},

			// ─── Storage ───
			{
				Type: "aws_s3_bucket", Label: "S3 Bucket", Icon: "🪣", Category: "Storage", Provider: "aws",
				Defaults: map[string]any{"bucket": "my-bucket"},
				Fields: []Field{
					{Name: "bucket", Type: "string", Required: true, Description: "Globally unique bucket name"},
					{Name: "force_destroy", Type: "bool", Default: false},
				},
			},
			{
				Type: "aws_ebs_volume", Label: "EBS Volume", Icon: "💾", Category: "Storage", Provider: "aws",
				Defaults: map[string]any{"availability_zone": "us-east-1a", "size": 20, "type": "gp3"},
				Fields: []Field{
					{Name: "size", Type: "number", Required: true, Default: 20, Description: "Size in GiB"},
					{Name: "type", Type: "select", Default: "gp3", Options: []string{"gp2", "gp3", "io1", "io2", "st1", "sc1"}},
				},
			},
			{
				Type: "aws_efs_file_system", Label: "EFS", Icon: "📁", Category: "Storage", Provider: "aws",
				Defaults: map[string]any{"encrypted": true, "performance_mode": "generalPurpose"},
			},

			// ─── Database ───
			{
				Type: "aws_rds_instance", Label: "RDS Database", Icon: "🗄️", Category: "Database", Provider: "aws",
				Defaults: map[string]any{"engine": "postgres", "engine_version": "15.4", "instance_class": "db.t3.micro", "allocated_storage": 20, "skip_final_snapshot": true},
				Fields: []Field{
					{Name: "engine", Type: "select", Required: true, Default: "postgres", Options: []string{"postgres", "mysql", "mariadb", "oracle-ee", "sqlserver-ex"}},
					{Name: "engine_version", Type: "string", Default: "15.4"},
					{Name: "instance_class", Type: "select", Required: true, Default: "db.t3.micro",
						Options: []string{"db.t3.micro", "db.t3.small", "db.t3.medium", "db.r5.large", "db.r5.xlarge"}},
					{Name: "allocated_storage", Type: "number", Required: true, Default: 20},
					{Name: "username", Type: "string", Required: true, Default: "admin"},
					{Name: "skip_final_snapshot", Type: "bool", Default: true},
				},
				ConnectsTo:  []string{"aws_security_group", "aws_subnet"},
				ConnectsVia: map[string]string{"vpc_security_group_ids": "aws_security_group"},
			},
			{
				Type: "aws_dynamodb_table", Label: "DynamoDB Table", Icon: "📊", Category: "Database", Provider: "aws",
				Defaults: map[string]any{"name": "my-table", "billing_mode": "PAY_PER_REQUEST", "hash_key": "id"},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "billing_mode", Type: "select", Default: "PAY_PER_REQUEST", Options: []string{"PAY_PER_REQUEST", "PROVISIONED"}},
					{Name: "hash_key", Type: "string", Required: true, Default: "id"},
				},
			},
			{
				Type: "aws_elasticache_cluster", Label: "ElastiCache", Icon: "⚡", Category: "Database", Provider: "aws",
				Defaults: map[string]any{"cluster_id": "my-cache", "engine": "redis", "node_type": "cache.t3.micro", "num_cache_nodes": 1},
			},

			// ─── Security ───
			{
				Type: "aws_security_group", Label: "Security Group", Icon: "🛡️", Category: "Security", Provider: "aws",
				Defaults: map[string]any{"name": "my-sg", "description": "Managed by IaC Studio"},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "description", Type: "string", Default: "Managed by IaC Studio"},
				},
				ConnectsTo:  []string{"aws_vpc"},
				ConnectsVia: map[string]string{"vpc_id": "aws_vpc"},
			},
			{
				Type: "aws_iam_role", Label: "IAM Role", Icon: "🔑", Category: "Security", Provider: "aws",
				Defaults: map[string]any{"name": "my-role"},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
				},
			},
			{
				Type: "aws_iam_policy", Label: "IAM Policy", Icon: "📜", Category: "Security", Provider: "aws",
				Defaults: map[string]any{"name": "my-policy", "description": "Managed by IaC Studio"},
			},
			{
				Type: "aws_kms_key", Label: "KMS Key", Icon: "🔐", Category: "Security", Provider: "aws",
				Defaults: map[string]any{"description": "Managed by IaC Studio", "deletion_window_in_days": 30, "enable_key_rotation": true},
			},

			// ─── Load Balancing ───
			{
				Type: "aws_lb", Label: "Load Balancer", Icon: "⚖️", Category: "Load Balancing", Provider: "aws",
				Defaults: map[string]any{"name": "my-alb", "internal": false, "load_balancer_type": "application"},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "internal", Type: "bool", Default: false},
					{Name: "load_balancer_type", Type: "select", Default: "application", Options: []string{"application", "network", "gateway"}},
				},
				ConnectsTo:  []string{"aws_subnet", "aws_security_group"},
				ConnectsVia: map[string]string{"subnets": "aws_subnet", "security_groups": "aws_security_group"},
			},
			{
				Type: "aws_lb_target_group", Label: "Target Group", Icon: "🎯", Category: "Load Balancing", Provider: "aws",
				Defaults:    map[string]any{"name": "my-tg", "port": 80, "protocol": "HTTP"},
				ConnectsTo:  []string{"aws_vpc"},
				ConnectsVia: map[string]string{"vpc_id": "aws_vpc"},
			},

			// ─── DNS ───
			{
				Type: "aws_route53_zone", Label: "Route53 Zone", Icon: "🌐", Category: "DNS", Provider: "aws",
				Defaults: map[string]any{"name": "example.com"},
			},
			{
				Type: "aws_route53_record", Label: "DNS Record", Icon: "📝", Category: "DNS", Provider: "aws",
				Defaults:    map[string]any{"name": "www", "type": "A", "ttl": 300},
				ConnectsTo:  []string{"aws_route53_zone"},
				ConnectsVia: map[string]string{"zone_id": "aws_route53_zone"},
			},

			// ─── Monitoring ───
			{
				Type: "aws_cloudwatch_log_group", Label: "CloudWatch Logs", Icon: "📋", Category: "Monitoring", Provider: "aws",
				Defaults: map[string]any{"name": "/app/my-service", "retention_in_days": 14},
			},
			{
				Type: "aws_sns_topic", Label: "SNS Topic", Icon: "📢", Category: "Monitoring", Provider: "aws",
				Defaults: map[string]any{"name": "my-alerts"},
			},
			{
				Type: "aws_sqs_queue", Label: "SQS Queue", Icon: "📬", Category: "Monitoring", Provider: "aws",
				Defaults: map[string]any{"name": "my-queue", "visibility_timeout_seconds": 30},
			},
		},
	}
}

func ansibleCatalog() Catalog {
	return Catalog{
		Tool: "ansible",
		Resources: []Resource{
			// ─── Packages ───
			{Type: "apt", Label: "Install Package (apt)", Icon: "📦", Category: "Packages",
				Defaults: map[string]any{"name": "nginx", "state": "present", "update_cache": true},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true, Description: "Package name"},
					{Name: "state", Type: "select", Default: "present", Options: []string{"present", "absent", "latest"}},
					{Name: "update_cache", Type: "bool", Default: true},
				},
			},
			{Type: "yum", Label: "Install Package (yum)", Icon: "📦", Category: "Packages",
				Defaults: map[string]any{"name": "httpd", "state": "present"},
			},
			{Type: "pip", Label: "Python Package (pip)", Icon: "🐍", Category: "Packages",
				Defaults: map[string]any{"name": "flask", "state": "present"},
			},

			// ─── System ───
			{Type: "service", Label: "Manage Service", Icon: "⚙️", Category: "System",
				Defaults: map[string]any{"name": "nginx", "state": "started", "enabled": true},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "state", Type: "select", Default: "started", Options: []string{"started", "stopped", "restarted", "reloaded"}},
					{Name: "enabled", Type: "bool", Default: true},
				},
			},
			{Type: "systemd", Label: "Systemd Service", Icon: "🔧", Category: "System",
				Defaults: map[string]any{"name": "myapp", "state": "started", "daemon_reload": true},
			},
			{Type: "user", Label: "User Account", Icon: "👤", Category: "System",
				Defaults: map[string]any{"name": "deploy", "shell": "/bin/bash", "create_home": true},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "shell", Type: "select", Default: "/bin/bash", Options: []string{"/bin/bash", "/bin/sh", "/bin/zsh", "/usr/sbin/nologin"}},
					{Name: "groups", Type: "string", Description: "Comma-separated list of groups"},
					{Name: "create_home", Type: "bool", Default: true},
				},
			},
			{Type: "group", Label: "Group", Icon: "👥", Category: "System",
				Defaults: map[string]any{"name": "deploy", "state": "present"},
			},
			{Type: "cron", Label: "Cron Job", Icon: "⏰", Category: "System",
				Defaults: map[string]any{"name": "backup", "minute": "0", "hour": "2", "job": "/usr/local/bin/backup.sh"},
			},
			{Type: "sysctl", Label: "Sysctl", Icon: "🎛️", Category: "System",
				Defaults: map[string]any{"name": "net.ipv4.ip_forward", "value": "1", "sysctl_set": true},
			},

			// ─── Files ───
			{Type: "copy", Label: "Copy File", Icon: "📄", Category: "Files",
				Defaults: map[string]any{"src": "files/app.conf", "dest": "/etc/app/app.conf", "mode": "0644"},
				Fields: []Field{
					{Name: "src", Type: "string", Required: true},
					{Name: "dest", Type: "string", Required: true},
					{Name: "mode", Type: "string", Default: "0644"},
					{Name: "owner", Type: "string", Default: "root"},
				},
			},
			{Type: "template", Label: "Template", Icon: "📝", Category: "Files",
				Defaults: map[string]any{"src": "templates/app.conf.j2", "dest": "/etc/app/app.conf", "mode": "0644"},
			},
			{Type: "file", Label: "File/Directory", Icon: "📂", Category: "Files",
				Defaults: map[string]any{"path": "/opt/myapp", "state": "directory", "mode": "0755"},
				Fields: []Field{
					{Name: "path", Type: "string", Required: true},
					{Name: "state", Type: "select", Default: "directory", Options: []string{"file", "directory", "link", "absent", "touch"}},
					{Name: "mode", Type: "string", Default: "0755"},
				},
			},
			{Type: "lineinfile", Label: "Line in File", Icon: "✏️", Category: "Files",
				Defaults: map[string]any{"path": "/etc/config", "regexp": "^key=", "line": "key=value"},
			},
			{Type: "unarchive", Label: "Unarchive", Icon: "📦", Category: "Files",
				Defaults: map[string]any{"src": "files/app.tar.gz", "dest": "/opt/", "remote_src": false},
			},

			// ─── Containers ───
			{Type: "docker_container", Label: "Docker Container", Icon: "🐳", Category: "Containers",
				Defaults: map[string]any{"name": "webapp", "image": "nginx:latest", "state": "started", "ports": "80:80"},
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "image", Type: "string", Required: true, Default: "nginx:latest"},
					{Name: "state", Type: "select", Default: "started", Options: []string{"started", "stopped", "absent", "present"}},
					{Name: "ports", Type: "string", Description: "Port mapping (host:container)"},
					{Name: "restart_policy", Type: "select", Default: "unless-stopped", Options: []string{"no", "always", "unless-stopped", "on-failure"}},
				},
			},
			{Type: "docker_image", Label: "Docker Image", Icon: "🏗️", Category: "Containers",
				Defaults: map[string]any{"name": "myapp", "source": "build", "build_path": "."},
			},
			{Type: "docker_compose", Label: "Docker Compose", Icon: "🐙", Category: "Containers",
				Defaults: map[string]any{"project_src": "/opt/myapp", "state": "present"},
			},
			{Type: "docker_network", Label: "Docker Network", Icon: "🔗", Category: "Containers",
				Defaults: map[string]any{"name": "app-network", "driver": "bridge"},
			},

			// ─── Cloud ───
			{Type: "ec2_instance", Label: "AWS EC2", Icon: "☁️", Category: "Cloud",
				Defaults: map[string]any{"instance_type": "t3.micro", "image_id": "ami-0c55b159cbfafe1f0", "state": "running"},
			},
			{Type: "gcp_compute_instance", Label: "GCP Instance", Icon: "☁️", Category: "Cloud",
				Defaults: map[string]any{"name": "my-instance", "machine_type": "e2-micro", "zone": "us-central1-a"},
			},

			// ─── Commands ───
			{Type: "command", Label: "Run Command", Icon: "💻", Category: "Commands",
				Defaults: map[string]any{"cmd": "echo hello"},
			},
			{Type: "shell", Label: "Shell Script", Icon: "🐚", Category: "Commands",
				Defaults: map[string]any{"cmd": "bash /opt/scripts/deploy.sh"},
			},
			{Type: "git", Label: "Git Clone", Icon: "📥", Category: "Commands",
				Defaults: map[string]any{"repo": "https://github.com/user/repo.git", "dest": "/opt/app", "version": "main"},
			},

			// ─── Networking ───
			{Type: "ufw", Label: "UFW Firewall", Icon: "🧱", Category: "Networking",
				Defaults: map[string]any{"rule": "allow", "port": "22", "proto": "tcp"},
			},
			{Type: "iptables", Label: "iptables Rule", Icon: "🔥", Category: "Networking",
				Defaults: map[string]any{"chain": "INPUT", "protocol": "tcp", "destination_port": "80", "jump": "ACCEPT"},
			},
		},
	}
}
