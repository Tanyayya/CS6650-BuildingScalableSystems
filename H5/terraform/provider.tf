terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.7.0"
    }
  }
}

# Configure AWS credentials & region
provider "aws" {
  region = var.aws_region
}