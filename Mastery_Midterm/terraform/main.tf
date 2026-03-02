terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

module "network" {
  source         = "./modules/network"
  service_name   = var.service_name
  container_port = var.container_port
}

module "ecr" {
  source          = "./modules/ecr"
  repository_name = var.ecr_repository_name
}

module "logging" {
  source            = "./modules/logging"
  service_name      = var.service_name
  retention_in_days = var.log_retention_days
}

module "alb" {
  source         = "./modules/alb"
  service_name   = var.service_name
  container_port = var.container_port
  subnet_ids     = module.network.subnet_ids
  alb_sg_id      = module.network.alb_sg_id
  vpc_id         = module.network.vpc_id
}

data "aws_iam_role" "lab_role" {
  name = "LabRole"
}

module "ecs" {
  source             = "./modules/ecs"
  service_name       = var.service_name
  image              = "${module.ecr.repository_url}:latest"
  container_port     = var.container_port
  subnet_ids         = module.network.subnet_ids
  security_group_ids = [module.network.security_group_id]
  execution_role_arn = data.aws_iam_role.lab_role.arn
  task_role_arn      = data.aws_iam_role.lab_role.arn
  log_group_name     = module.logging.log_group_name
  ecs_count          = var.ecs_count
  region             = var.aws_region
  target_group_arn   = module.alb.target_group_arn
}

output "alb_dns_name" {
  value       = module.alb.alb_dns_name
  description = "Use this as your Locust host"
}

output "test_curl" {
  value = "curl 'http://${module.alb.alb_dns_name}/products/search?q=electronics'"
}

output "locust_command" {
  value = "python3 -m locust -f locustfile.py --host=http://${module.alb.alb_dns_name}"
}