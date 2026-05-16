#!/usr/bin/env bash
# provision.sh — AWS KMS + IAM provisioning for the Mimir issuer (CLI form).
#
# This is the AWS-CLI equivalent of main.tf for operators who prefer
# imperative scripts or don't use Terraform. Outputs the same env block
# (KMS_MODE / KMS_KEY_ARN / AWS_REGION) to STDOUT at the end.
#
# Prereqs:
#   - aws cli v2 in PATH
#   - AWS credentials configured (`aws sts get-caller-identity` works)
#   - IAM permissions in your caller account:
#       kms:CreateKey, kms:CreateAlias, kms:TagResource,
#       iam:CreatePolicy, iam:CreateRole, iam:AttachRolePolicy
#
# Usage:
#   ./provision.sh prod                                    # default region from AWS_REGION env
#   AWS_REGION=us-east-1 ./provision.sh staging
#
# Idempotency: re-running creates a SECOND key + role with timestamped
# names. To rerun cleanly, either tear down (see deprovision.sh) or use
# Terraform which is properly idempotent.

set -euo pipefail

ENV_SLUG="${1:-prod}"
REGION="${AWS_REGION:-$(aws configure get region)}"
if [[ -z "$REGION" ]]; then
  echo "ERROR: no AWS region; set AWS_REGION or `aws configure set region <region>`" >&2
  exit 1
fi

NAME="mimir-issuer-${ENV_SLUG}"
ALIAS="alias/${NAME}"

echo "==== Mimir AWS KMS provisioning ($ENV_SLUG, $REGION) ===="

# ─── 1. Create the asymmetric Ed25519 key. ──────────────────────────────
echo
echo "[1/5] Creating KMS asymmetric Ed25519 key"
KEY_JSON=$(aws kms create-key \
  --region "$REGION" \
  --description "Mimir issuer Ed25519 signing key ($ENV_SLUG)" \
  --key-usage SIGN_VERIFY \
  --customer-master-key-spec ECC_NIST_EDWARDS25519 \
  --tags TagKey=Project,TagValue=mimir TagKey=Component,TagValue=issuer-signing-key TagKey=Environment,TagValue="$ENV_SLUG" TagKey=ManagedBy,TagValue=cli-provision \
  --output json)
KEY_ID=$(echo "$KEY_JSON" | python -c "import sys,json; print(json.load(sys.stdin)['KeyMetadata']['KeyId'])")
KEY_ARN=$(echo "$KEY_JSON" | python -c "import sys,json; print(json.load(sys.stdin)['KeyMetadata']['Arn'])")
echo "      key id  : $KEY_ID"
echo "      key arn : $KEY_ARN"

# ─── 2. Alias the key. ──────────────────────────────────────────────────
echo
echo "[2/5] Creating alias $ALIAS"
aws kms create-alias --region "$REGION" --alias-name "$ALIAS" --target-key-id "$KEY_ID"
echo "      alias   : $ALIAS"

# ─── 3. Build the minimum-privilege IAM policy. ─────────────────────────
echo
echo "[3/5] Creating IAM policy mimir-issuer-kms-use-$ENV_SLUG"
POLICY_DOC=$(cat <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "AllowIssuerSignAndGetPublicKey",
    "Effect": "Allow",
    "Action": ["kms:Sign","kms:GetPublicKey","kms:DescribeKey"],
    "Resource": "$KEY_ARN"
  }]
}
EOF
)
POLICY_ARN=$(aws iam create-policy \
  --policy-name "mimir-issuer-kms-use-$ENV_SLUG" \
  --policy-document "$POLICY_DOC" \
  --description "Minimum-privilege KMS policy for the Mimir issuer ($ENV_SLUG)" \
  --output json | python -c "import sys,json; print(json.load(sys.stdin)['Policy']['Arn'])")
echo "      policy  : $POLICY_ARN"

# ─── 4. Create the IAM role. ────────────────────────────────────────────
echo
echo "[4/5] Creating IAM role $NAME"
TRUST_DOC='{
  "Version":"2012-10-17",
  "Statement":[{
    "Sid":"AllowEC2EKSLambdaAssume",
    "Effect":"Allow",
    "Action":"sts:AssumeRole",
    "Principal":{"Service":["ec2.amazonaws.com","lambda.amazonaws.com","pods.eks.amazonaws.com"]}
  }]
}'
ROLE_ARN=$(aws iam create-role \
  --role-name "$NAME" \
  --description "Role assumed by the Mimir issuer process; grants kms:Sign + kms:GetPublicKey on the issuer key only" \
  --assume-role-policy-document "$TRUST_DOC" \
  --output json | python -c "import sys,json; print(json.load(sys.stdin)['Role']['Arn'])")
echo "      role    : $ROLE_ARN"

aws iam attach-role-policy --role-name "$NAME" --policy-arn "$POLICY_ARN"

# ─── 5. Print the env snippet. ──────────────────────────────────────────
echo
echo "[5/5] Done — copy this env block into your issuer's deployment:"
echo
echo "    KMS_MODE=aws"
echo "    KMS_KEY_ARN=$KEY_ARN"
echo "    AWS_REGION=$REGION"
echo
echo "And attach the IAM role to whatever compute substrate runs the issuer:"
echo "    EC2:        IamInstanceProfile=$NAME (via aws ec2 associate-iam-instance-profile)"
echo "    EKS:        ServiceAccount annotation eks.amazonaws.com/role-arn=$ROLE_ARN"
echo "    Lambda:     Role=$ROLE_ARN"
echo
echo "To smoke-test (requires the issuer source from this repo):"
echo "    cd ../../issuer"
echo "    KMS_MODE=aws KMS_KEY_ARN=$KEY_ARN AWS_REGION=$REGION go run ."
echo "    curl http://localhost:8080/v1/key   # 'kid' should equal $KEY_ARN"
