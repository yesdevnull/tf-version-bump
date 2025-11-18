# Test file with unusual formatting
# Tests edge cases in HCL formatting

module "vpc1" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
  name    = "vpc1"
}

module "vpc2" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
  name    = "vpc2"
}

# Module with minimal spacing
module "vpc3" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
  name    = "vpc3"
}

# Module with lots of spacing
module "vpc4" {

  source = "terraform-aws-modules/vpc/aws"

  version = "5.0.0"

  name = "vpc4"

}

# Module with different spacing styles
module "s3" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "3.0.0"
  bucket  = "my-bucket"
}

# Module with heredoc
module "lambda" {
  source  = "terraform-aws-modules/lambda/aws"
  version = "4.0.0"

  function_name = "my-function"
  handler       = "index.handler"
  runtime       = "nodejs18.x"

  environment_variables = {
    KEY = <<-EOT
      This is a heredoc string
      with multiple lines
      EOT
  }
}

# Nested module with complex attributes
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "19.0.0"

  cluster_name = "my-cluster"

  cluster_addons = {
    coredns = {
      most_recent = true
    }
    kube-proxy = {
      most_recent = true
    }
  }

  vpc_id     = module.vpc1.vpc_id
  subnet_ids = module.vpc1.private_subnets

  # Complex nested structure
  eks_managed_node_groups = {
    default = {
      min_size     = 1
      max_size     = 3
      desired_size = 2

      instance_types = ["t3.medium"]
      capacity_type  = "SPOT"
    }
  }
}
