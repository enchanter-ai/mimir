# Mimir issuer — AWS KMS provisioning (Terraform).
#
# Creates exactly the four AWS resources the issuer needs to run in
# KMS_MODE=aws:
#
#   1. KMS asymmetric Ed25519 key (Sign/Verify).
#   2. KMS alias pointing at the key (stable handle across rotations).
#   3. IAM policy granting the minimum-privilege subset of operations
#      (kms:Sign + kms:GetPublicKey) that the issuer process needs.
#   4. IAM role + instance profile (or assumed-role policy) the issuer
#      assumes at runtime.
#
# Usage:
#
#   cd deploy/aws-kms
#   terraform init
#   terraform apply -var='environment=prod'
#
# Output:
#   kms_key_arn          → set KMS_KEY_ARN on the issuer
#   issuer_iam_role_arn  → attach to the EC2/EKS workload running the issuer
#   alias_name           → kms alias for human-readable references
#
# Cost: KMS asymmetric keys are $1/month each. Sign requests are $0.03/10k.
# A 1500 RPS issuer signs ~3.9B times/month = ~$11.7k/month in KMS Sign
# costs at v0 — production deployments at that volume should evaluate
# Cloud HSM (lower per-call cost) or aggregating signatures via Merkle
# batching before tagging this as a budget item.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# ─── Inputs ──────────────────────────────────────────────────────────────

variable "environment" {
  type        = string
  description = "Environment slug (prod, staging, dev) — embedded in resource names + tags."
  default     = "prod"
}

variable "key_alias" {
  type        = string
  description = "KMS alias to create. Defaults to mimir-issuer-<environment>."
  default     = ""
}

variable "issuer_principal_arns" {
  type        = list(string)
  description = "ARNs that are allowed to assume the issuer's IAM role (EC2 / EKS / Lambda). Leave empty to use the default service-principal trust only."
  default     = []
}

variable "tags" {
  type    = map(string)
  default = {}
}

locals {
  alias_name = var.key_alias != "" ? var.key_alias : "mimir-issuer-${var.environment}"
  base_tags = merge({
    "Project"     = "mimir"
    "Component"   = "issuer-signing-key"
    "Environment" = var.environment
    "ManagedBy"   = "terraform"
  }, var.tags)
}

# ─── KMS key ────────────────────────────────────────────────────────────

resource "aws_kms_key" "issuer" {
  description              = "Mimir issuer Ed25519 signing key (${var.environment})"
  customer_master_key_spec = "ECC_NIST_EDWARDS25519"
  key_usage                = "SIGN_VERIFY"

  # Standard 30-day window before deletion takes effect. Production keys
  # should never be deleted in practice (rotate via JWK Set + historical
  # keys file — see scripts/rotate-key.py).
  deletion_window_in_days = 30

  # Disable AWS's built-in rotation — the issuer rotates by publishing the
  # old key with status:retired in historical-keys.json and pointing at a
  # new key, not by KMS doing transparent rotation under us.
  enable_key_rotation = false

  multi_region = false

  tags = local.base_tags
}

resource "aws_kms_alias" "issuer" {
  name          = "alias/${local.alias_name}"
  target_key_id = aws_kms_key.issuer.id
}

# ─── IAM: policy granting kms:Sign + kms:GetPublicKey ────────────────────

data "aws_iam_policy_document" "issuer_kms_use" {
  statement {
    sid    = "AllowIssuerSignAndGetPublicKey"
    effect = "Allow"
    actions = [
      "kms:Sign",
      "kms:GetPublicKey",
      "kms:DescribeKey",
    ]
    resources = [aws_kms_key.issuer.arn]
  }
}

resource "aws_iam_policy" "issuer_kms_use" {
  name        = "mimir-issuer-kms-use-${var.environment}"
  description = "Minimum-privilege KMS policy for the Mimir issuer (${var.environment})"
  policy      = data.aws_iam_policy_document.issuer_kms_use.json
  tags        = local.base_tags
}

# ─── IAM: role for the workload running the issuer ──────────────────────

data "aws_iam_policy_document" "issuer_assume_role" {
  statement {
    sid     = "AllowEC2EKSLambdaAssume"
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type = "Service"
      identifiers = [
        "ec2.amazonaws.com",
        "lambda.amazonaws.com",
        "pods.eks.amazonaws.com",
      ]
    }
  }

  dynamic "statement" {
    for_each = length(var.issuer_principal_arns) > 0 ? [1] : []
    content {
      sid     = "AllowExplicitPrincipalAssume"
      effect  = "Allow"
      actions = ["sts:AssumeRole"]
      principals {
        type        = "AWS"
        identifiers = var.issuer_principal_arns
      }
    }
  }
}

resource "aws_iam_role" "issuer" {
  name               = "mimir-issuer-${var.environment}"
  description        = "Role assumed by the Mimir issuer process; grants kms:Sign + kms:GetPublicKey on the issuer key only."
  assume_role_policy = data.aws_iam_policy_document.issuer_assume_role.json
  tags               = local.base_tags
}

resource "aws_iam_role_policy_attachment" "issuer_kms" {
  role       = aws_iam_role.issuer.name
  policy_arn = aws_iam_policy.issuer_kms_use.arn
}

# ─── Outputs ────────────────────────────────────────────────────────────

data "aws_region" "current" {}

output "kms_key_arn" {
  description = "Pass this to the issuer as KMS_KEY_ARN env var."
  value       = aws_kms_key.issuer.arn
}

output "kms_alias" {
  description = "Alias (alias/mimir-issuer-<env>) — usable in place of the ARN."
  value       = aws_kms_alias.issuer.name
}

output "issuer_iam_role_arn" {
  description = "Attach this role to the EC2 instance / EKS pod / Lambda running the issuer."
  value       = aws_iam_role.issuer.arn
}

output "issuer_env_snippet" {
  description = "Copy-paste env block for the running issuer."
  value = <<-EOT
    KMS_MODE=aws
    KMS_KEY_ARN=${aws_kms_key.issuer.arn}
    AWS_REGION=${data.aws_region.current.name}
  EOT
}
