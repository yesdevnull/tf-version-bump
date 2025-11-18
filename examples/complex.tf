# Complex Terraform configuration with varied formatting
# This file tests that the version bump tool preserves formatting

terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}

# VPC Module with inline comments
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0" # This should be updated

  name = "my-vpc"
  cidr = "10.0.0.0/16"

  # Availability zones configuration
  azs = ["us-east-1a", "us-east-1b", "us-east-1c"]

  # Private subnets
  private_subnets = [
    "10.0.1.0/24", # AZ a
    "10.0.2.0/24", # AZ b
    "10.0.3.0/24", # AZ c
  ]

  # Public subnets
  public_subnets = [
    "10.0.101.0/24", # AZ a
    "10.0.102.0/24", # AZ b
    "10.0.103.0/24", # AZ c
  ]

  enable_nat_gateway   = true
  enable_vpn_gateway   = true
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Terraform   = "true"
    Environment = "dev"
    Owner       = "DevOps"
  }
}

/*
 * Security Group Module
 * Multi-line comment block
 * This should be preserved
 */
module "security_group" {
  source  = "terraform-aws-modules/security-group/aws"
  version = "4.9.0"

  name        = "example-sg"
  description = "Security group for example usage"
  vpc_id      = module.vpc.vpc_id

  # Ingress rules
  ingress_cidr_blocks = ["10.0.0.0/8"]
  ingress_rules = [
    "http-80-tcp",
    "https-443-tcp",
  ]

  # Egress rules
  egress_rules = ["all-all"]

  tags = {
    Name = "example-security-group"
  }
}

# Local module - should not be updated
module "local_module" {
  source = "./modules/custom-module"

  name = "test"
  config = {
    enabled = true
    value   = 42
  }
}

# Resource blocks should remain untouched
resource "aws_instance" "example" {
  ami           = "ami-0c55b159cbfafe1f0"
  instance_type = "t2.micro"

  tags = {
    Name = "example-instance"
  }

  # User data script
  user_data = <<-EOF
              #!/bin/bash
              echo "Hello World"
              # This is a shell comment
              apt-get update
              EOF

  lifecycle {
    create_before_destroy = true
  }
}

# Data sources should remain untouched
data "aws_ami" "ubuntu" {
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-focal-20.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  owners = ["099720109477"] # Canonical
}

# Another module with the same source
module "vpc_secondary" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0" # Different indentation

  name = "secondary-vpc"
  cidr = "172.16.0.0/16"

  tags = {
    Name = "secondary"
  }
}

# Outputs should remain untouched
output "vpc_id" {
  description = "The ID of the VPC"
  value       = module.vpc.vpc_id
}

output "security_group_id" {
  description = "The ID of the security group"
  value       = module.security_group.security_group_id
  sensitive   = false
}
