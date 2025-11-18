####################################
# Production Environment Configuration
# DO NOT MODIFY WITHOUT APPROVAL
####################################

/*
 * VPC Configuration
 * ==================
 * This module creates the production VPC
 * with multi-AZ configuration for high availability
 */
module "prod_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"

  # Basic configuration
  name = "production-vpc"
  cidr = "10.0.0.0/16"

  # Multi-AZ setup for HA
  azs = [
    "us-west-2a", # Primary AZ
    "us-west-2b", # Secondary AZ
    "us-west-2c", # Tertiary AZ
  ]

  # Subnet configuration
  # --------------------
  # Private subnets for internal resources
  private_subnets = [
    "10.0.1.0/24", # us-west-2a private
    "10.0.2.0/24", # us-west-2b private
    "10.0.3.0/24", # us-west-2c private
  ]

  # Public subnets for load balancers
  public_subnets = [
    "10.0.101.0/24", # us-west-2a public
    "10.0.102.0/24", # us-west-2b public
    "10.0.103.0/24", # us-west-2c public
  ]

  # Database subnets (isolated)
  database_subnets = [
    "10.0.201.0/24", # us-west-2a db
    "10.0.202.0/24", # us-west-2b db
    "10.0.203.0/24", # us-west-2c db
  ]

  # NAT Gateway configuration
  # -------------------------
  enable_nat_gateway     = true  # Required for private subnet internet access
  single_nat_gateway     = false # Use multiple NAT gateways for HA
  one_nat_gateway_per_az = true  # One NAT gateway per AZ

  # DNS configuration
  enable_dns_hostnames = true
  enable_dns_support   = true

  # VPN configuration
  # -----------------
  enable_vpn_gateway = true

  # Tags for resource management
  tags = {
    Terraform   = "true"
    Environment = "production"
    CostCenter  = "engineering"
    Compliance  = "pci-dss"
    ManagedBy   = "terraform"
    # Contact information
    Owner = "platform-team@example.com"
    Team  = "Platform Engineering"
  }

  # Subnet-specific tags
  public_subnet_tags = {
    Type = "public"
    Tier = "dmz"
  }

  private_subnet_tags = {
    Type                              = "private"
    Tier                              = "application"
    "kubernetes.io/role/internal-elb" = 1
  }

  database_subnet_tags = {
    Type = "database"
    Tier = "data"
  }
}

/**
 * RDS Module
 * ==========
 * PostgreSQL database for production workloads
 *
 * Features:
 * - Multi-AZ deployment
 * - Automated backups
 * - Encryption at rest
 * - Enhanced monitoring
 */
module "rds" {
  source  = "terraform-aws-modules/rds/aws"
  version = "5.0.0"

  identifier = "production-postgres"

  # Engine configuration
  engine               = "postgres"
  engine_version       = "14.7"
  family               = "postgres14"
  major_engine_version = "14"
  instance_class       = "db.r5.xlarge"

  # Storage
  allocated_storage     = 100
  max_allocated_storage = 1000
  storage_encrypted     = true
  # kms_key_id          = aws_kms_key.rds.arn  # Uncomment when KMS key is created

  # Database settings
  db_name  = "production"
  username = "admin"
  port     = 5432

  # High availability
  multi_az = true

  # Network
  db_subnet_group_name   = module.prod_vpc.database_subnet_group_name
  vpc_security_group_ids = [module.database_sg.security_group_id]

  # Maintenance
  maintenance_window      = "Mon:00:00-Mon:03:00"
  backup_window           = "03:00-06:00"
  backup_retention_period = 30
  skip_final_snapshot     = false
  deletion_protection     = true

  # Monitoring
  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]
  performance_insights_enabled    = true
  monitoring_interval             = 60

  tags = {
    Environment = "production"
    Application = "main-db"
  }
}

# Security group for database
module "database_sg" {
  source  = "terraform-aws-modules/security-group/aws"
  version = "4.9.0"

  name        = "production-database-sg"
  description = "Security group for production RDS instance"
  vpc_id      = module.prod_vpc.vpc_id

  # PostgreSQL ingress from private subnets only
  ingress_with_cidr_blocks = [
    {
      from_port   = 5432
      to_port     = 5432
      protocol    = "tcp"
      description = "PostgreSQL access from private subnets"
      cidr_blocks = join(",", module.prod_vpc.private_subnets_cidr_blocks)
    },
  ]

  tags = {
    Name = "production-database-sg"
  }
}

################################
# Local variables for DRY configuration
################################
locals {
  # Common tags to apply to all resources
  common_tags = {
    Project     = "production-infrastructure"
    ManagedBy   = "terraform"
    Repository  = "infrastructure-repo"
    LastUpdated = timestamp()
  }

  # Environment-specific settings
  env = "production"

  # Region configuration
  region = "us-west-2"
}

################################
# Outputs
################################
output "vpc_id" {
  description = "The ID of the production VPC"
  value       = module.prod_vpc.vpc_id
}

output "private_subnets" {
  description = "List of IDs of private subnets"
  value       = module.prod_vpc.private_subnets
}

output "database_endpoint" {
  description = "The connection endpoint for the RDS instance"
  value       = module.rds.db_instance_endpoint
  sensitive   = true
}
