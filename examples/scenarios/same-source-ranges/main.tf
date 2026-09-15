module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"
}

module "pinned_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.1.0"
}

module "transit_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 4.0"
}

module "legacy_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 4.0"
}

module "sandbox_vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "3.19.0"
}
