
output "subnet_ids"        { value = data.aws_subnets.default.ids }
output "security_group_id" { value = aws_security_group.ecs.id }
output "alb_sg_id"         { value = aws_security_group.alb.id }
output "vpc_id"            { value = data.aws_vpc.default.id }