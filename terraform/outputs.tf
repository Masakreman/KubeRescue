output "cluster_endpoint" {
  value = aws_eks_cluster.main.endpoint
}

output "cluster_certificate" {
  value = aws_eks_cluster.main.certificate_authority[0].data
}

output "kubeconfig_command" {
  value = "aws eks update-kubeconfig --name kuberescue --region us-east-1"
}