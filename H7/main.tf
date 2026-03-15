terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

provider "aws" {
  region = var.aws_region
}

variable "aws_region"   { default = "us-east-1" }
variable "project_name" { default = "ecommerce-lab" }
variable "image_uri"    { description = "ECR image URI" }
variable "num_workers"  { default = 5 }

locals {
  name     = var.project_name
  tags     = { Project = local.name, ManagedBy = "terraform" }
  lab_role = "arn:aws:iam::989461485811:role/LabRole"
}

# ── VPC ───────────────────────────────────────────────────────────────────────

resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags = merge(local.tags, { Name = "${local.name}-vpc" })
}

resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id
  tags   = merge(local.tags, { Name = "${local.name}-igw" })
}

resource "aws_subnet" "public" {
  for_each = {
    "a" = { cidr = "10.0.1.0/24",  az = "${var.aws_region}a" }
    "b" = { cidr = "10.0.2.0/24",  az = "${var.aws_region}b" }
  }
  vpc_id                  = aws_vpc.main.id
  cidr_block              = each.value.cidr
  availability_zone       = each.value.az
  map_public_ip_on_launch = true
  tags = merge(local.tags, { Name = "${local.name}-public-${each.key}" })
}

resource "aws_subnet" "private" {
  for_each = {
    "a" = { cidr = "10.0.10.0/24", az = "${var.aws_region}a" }
    "b" = { cidr = "10.0.11.0/24", az = "${var.aws_region}b" }
  }
  vpc_id            = aws_vpc.main.id
  cidr_block        = each.value.cidr
  availability_zone = each.value.az
  tags = merge(local.tags, { Name = "${local.name}-private-${each.key}" })
}

resource "aws_eip" "nat" {
  domain = "vpc"
}

resource "aws_nat_gateway" "nat" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public["a"].id
  tags          = merge(local.tags, { Name = "${local.name}-nat" })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }
  tags = merge(local.tags, { Name = "${local.name}-rt-public" })
}

resource "aws_route_table_association" "public" {
  for_each       = aws_subnet.public
  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.nat.id
  }
  tags = merge(local.tags, { Name = "${local.name}-rt-private" })
}

resource "aws_route_table_association" "private" {
  for_each       = aws_subnet.private
  subnet_id      = each.value.id
  route_table_id = aws_route_table.private.id
}

# ── Security Groups ───────────────────────────────────────────────────────────

resource "aws_security_group" "alb" {
  name   = "${local.name}-alb-sg"
  vpc_id = aws_vpc.main.id
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = merge(local.tags, { Name = "${local.name}-alb-sg" })
}

resource "aws_security_group" "ecs" {
  name   = "${local.name}-ecs-sg"
  vpc_id = aws_vpc.main.id
  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = merge(local.tags, { Name = "${local.name}-ecs-sg" })
}

# ── ALB ───────────────────────────────────────────────────────────────────────

resource "aws_lb" "main" {
  name               = "${local.name}-alb"
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = [for s in aws_subnet.public : s.id]
  tags               = local.tags
}

resource "aws_lb_target_group" "app" {
  name        = "${local.name}-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = aws_vpc.main.id
  target_type = "ip"
  health_check {
    path                = "/health"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
    matcher             = "200"
  }
  tags = local.tags
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# ── SNS + SQS ─────────────────────────────────────────────────────────────────

resource "aws_sns_topic" "orders" {
  name = "order-processing-events"
  tags = local.tags
}

resource "aws_sqs_queue" "orders" {
  name                       = "order-processing-queue"
  visibility_timeout_seconds = 30
  message_retention_seconds  = 345600
  receive_wait_time_seconds  = 20
  tags                       = local.tags
}

resource "aws_sqs_queue_policy" "orders" {
  queue_url = aws_sqs_queue.orders.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "sns.amazonaws.com" }
      Action    = "sqs:SendMessage"
      Resource  = aws_sqs_queue.orders.arn
      Condition = {
        ArnEquals = { "aws:SourceArn" = aws_sns_topic.orders.arn }
      }
    }]
  })
}

resource "aws_sns_topic_subscription" "orders_sqs" {
  topic_arn = aws_sns_topic.orders.arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.orders.arn
}

# ── ECS ───────────────────────────────────────────────────────────────────────
# Using pre-existing LabRole — Vocareum does not allow iam:CreateRole

resource "aws_ecs_cluster" "main" {
  name = "${local.name}-cluster"
  tags = local.tags
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/${local.name}"
  retention_in_days = 7
  tags              = local.tags
}

resource "aws_ecs_task_definition" "order_service" {
  family                   = "${local.name}-order-service"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = local.lab_role
  task_role_arn            = local.lab_role

  container_definitions = jsonencode([{
    name      = "order-service"
    image     = var.image_uri
    essential = true
    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]
    environment = [
      { name = "PORT",          value = "8080" },
      { name = "AWS_REGION",    value = var.aws_region },
      { name = "SNS_TOPIC_ARN", value = aws_sns_topic.orders.arn }
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.app.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "order-service"
      }
    }
    healthCheck = {
      command     = ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
      interval    = 30
      timeout     = 5
      retries     = 3
      startPeriod = 10
    }
  }])
  tags = local.tags
}

resource "aws_ecs_service" "order_service" {
  name            = "${local.name}-order-service"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.order_service.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = [for s in aws_subnet.private : s.id]
    security_groups  = [aws_security_group.ecs.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = "order-service"
    container_port   = 8080
  }

  depends_on = [aws_lb_listener.http]
  tags       = local.tags
}

resource "aws_ecs_task_definition" "order_processor" {
  family                   = "${local.name}-order-processor"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = local.lab_role
  task_role_arn            = local.lab_role

  container_definitions = jsonencode([{
    name      = "order-processor"
    image     = var.image_uri
    essential = true
    portMappings = [{
      containerPort = 8081
      protocol      = "tcp"
    }]
    environment = [
      { name = "PORT",          value = "8081" },
      { name = "AWS_REGION",    value = var.aws_region },
      { name = "SNS_TOPIC_ARN", value = aws_sns_topic.orders.arn },
      { name = "SQS_QUEUE_URL", value = aws_sqs_queue.orders.url },
      { name = "NUM_WORKERS",   value = tostring(var.num_workers) }
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.app.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "order-processor"
      }
    }
    healthCheck = {
      command     = ["CMD-SHELL", "wget -qO- http://localhost:8081/health || exit 1"]
      interval    = 30
      timeout     = 5
      retries     = 3
      startPeriod = 10
    }
  }])
  tags = local.tags
}

resource "aws_ecs_service" "order_processor" {
  name            = "${local.name}-order-processor"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.order_processor.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = [for s in aws_subnet.private : s.id]
    security_groups  = [aws_security_group.ecs.id]
    assign_public_ip = false
  }

  tags = local.tags
}

# ── Lambda (Part III) ─────────────────────────────────────────────────────────
# Replaces ECS order-processor. Subscribes directly to SNS — no SQS needed.
# AWS manages scaling, retries, and execution environment entirely.

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${local.name}-order-processor"
  retention_in_days = 7
  tags              = local.tags
}

resource "aws_lambda_function" "order_processor" {
  function_name = "${local.name}-order-processor"
  description   = "Part III: serverless order processor — SNS trigger, no SQS"
  role          = local.lab_role
  runtime       = "provided.al2"
  architectures = ["x86_64"]
  handler       = "bootstrap"          # required field; ignored by provided.al2
  filename      = "${path.module}/lambda/bootstrap.zip"
  memory_size   = 512
  timeout       = 30                   # 3s processing + headroom

  environment {
    variables = {
      AWS_REGION_NAME = var.aws_region  # informational; Lambda sets AWS_REGION automatically
    }
  }

  # Ensure CloudWatch log group exists before the function creates its own.
  depends_on = [aws_cloudwatch_log_group.lambda]

  tags = local.tags
}

# Allow SNS to invoke the Lambda function.
resource "aws_lambda_permission" "sns_invoke" {
  statement_id  = "AllowSNSInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.order_processor.function_name
  principal     = "sns.amazonaws.com"
  source_arn    = aws_sns_topic.orders.arn
}

# Subscribe Lambda directly to the existing SNS topic (bypasses SQS entirely).
resource "aws_sns_topic_subscription" "orders_lambda" {
  topic_arn = aws_sns_topic.orders.arn
  protocol  = "lambda"
  endpoint  = aws_lambda_function.order_processor.arn

  depends_on = [aws_lambda_permission.sns_invoke]
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "alb_dns_name" {
  value = aws_lb.main.dns_name
}

output "async_endpoint" {
  value = "http://${aws_lb.main.dns_name}/orders/async"
}

output "sync_endpoint" {
  value = "http://${aws_lb.main.dns_name}/orders/sync"
}

output "sns_topic_arn" {
  value = aws_sns_topic.orders.arn
}

output "sqs_queue_url" {
  value = aws_sqs_queue.orders.url
}

output "lambda_function_name" {
  value = aws_lambda_function.order_processor.function_name
}

output "lambda_log_group" {
  value = aws_cloudwatch_log_group.lambda.name
}
