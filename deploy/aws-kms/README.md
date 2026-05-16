# Mimir issuer — AWS KMS provisioning

Two paths to provision the AWS resources the issuer needs in production:

| Path | When to pick it |
|---|---|
| **Terraform** (`main.tf`) | You already use Terraform; you want idempotent reruns; you want the IAM + KMS state version-controlled. |
| **Shell** (`provision.sh`) | One-off setup; you don't have Terraform; you want to read the AWS-CLI commands literally. |

Both produce the same four AWS resources and emit the same env block at the end. Total runtime: ~30 seconds. Monthly cost: **$1/month** for the asymmetric KMS key, plus **$0.03 per 10k Sign calls**.

---

## Quick start (Terraform)

```bash
cd deploy/aws-kms
terraform init
terraform apply -var='environment=prod'
```

Terraform prints `kms_key_arn`, `issuer_iam_role_arn`, `issuer_env_snippet`. Copy the snippet into your deployment manifest:

```bash
terraform output -raw issuer_env_snippet
# →   KMS_MODE=aws
#     KMS_KEY_ARN=arn:aws:kms:us-east-1:<acct>:key/<uuid>
#     AWS_REGION=us-east-1
```

---

## Quick start (Shell)

```bash
cd deploy/aws-kms
AWS_REGION=us-east-1 ./provision.sh prod
```

The script prints the same env block at the end. The `prod` arg becomes the environment slug embedded in resource names.

---

## What this creates

1. **`aws_kms_key.issuer`** — a `ECC_NIST_EDWARDS25519` key with `KeyUsage: SIGN_VERIFY`. AWS rotation is intentionally **disabled** — Mimir rotates by publishing the old key with `status: retired` in the JWK Set (see [`scripts/rotate-key.py`](../../scripts/rotate-key.py)), not by KMS doing transparent rotation under the issuer.

2. **`aws_kms_alias.issuer`** — `alias/mimir-issuer-<env>`. Use this in environment manifests so you can swap underlying keys without redeploying.

3. **`aws_iam_policy.issuer_kms_use`** — minimum-privilege policy:
   ```json
   {
     "Effect": "Allow",
     "Action": ["kms:Sign", "kms:GetPublicKey", "kms:DescribeKey"],
     "Resource": "<the issuer key ARN>"
   }
   ```
   Nothing else. No `kms:Decrypt`, no `kms:*`, no wildcards.

4. **`aws_iam_role.issuer`** — the role your EC2 / EKS pod / Lambda assumes. Trust policy allows `ec2.amazonaws.com`, `pods.eks.amazonaws.com`, `lambda.amazonaws.com`. Add explicit principal ARNs via `var.issuer_principal_arns` if you need cross-account or other scenarios.

---

## After provisioning — point the issuer at the key

Take the env block and apply it wherever the issuer runs:

### Docker Compose (`docker-compose.yml`)
```yaml
services:
  issuer:
    image: mimir-issuer:v0.1.0
    environment:
      KMS_MODE: aws
      KMS_KEY_ARN: arn:aws:kms:us-east-1:123456789012:key/abcd...
      AWS_REGION: us-east-1
      # AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are picked up by the
      # AWS SDK's default credential chain. For production, use IRSA /
      # IAM instance profile instead.
```

### Kubernetes (EKS with IRSA)
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mimir-issuer
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/mimir-issuer-prod
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mimir-issuer
spec:
  template:
    spec:
      serviceAccountName: mimir-issuer
      containers:
      - name: issuer
        image: mimir-issuer:v0.1.0
        env:
          - name: KMS_MODE
            value: aws
          - name: KMS_KEY_ARN
            value: arn:aws:kms:us-east-1:123456789012:key/abcd...
          - name: AWS_REGION
            value: us-east-1
        livenessProbe:
          httpGet: { path: /v1/healthz, port: 8080 }
```

### Local dev (your laptop, against a real KMS key)
```bash
cd issuer
AWS_PROFILE=mimir-prod KMS_MODE=aws KMS_KEY_ARN=arn:... AWS_REGION=us-east-1 go run .
```

---

## How the issuer invokes KMS (under the hood)

The Go code path is in [`issuer/kms/aws.go`](../../issuer/kms/aws.go). On startup:

1. `aws-sdk-go-v2/config.LoadDefaultConfig(ctx, WithRegion(region))` resolves credentials from the standard SDK chain (env vars → IAM instance profile → IRSA → `~/.aws/credentials`).
2. The issuer calls `kms.GetPublicKey(KeyId: <arn>)` ONCE, parses the DER-encoded `SubjectPublicKeyInfo`, asserts the result is `ed25519.PublicKey`, and caches it for the process lifetime.
3. For every `/v1/attest`, the issuer calls `kms.Sign(KeyId, Message: <canonical bytes>, MessageType: RAW, SigningAlgorithm: ED25519_SHA_512)`. KMS handles SHA-512 internally; the issuer passes raw canonical-form bytes.
4. KMS returns a raw 64-byte Ed25519 signature (NOT DER-encoded), directly compatible with the envelope format.

Latency budget per envelope: ~25-40 ms (one cross-region KMS round-trip; us-east-1 ↔ us-east-1 is ~10ms).

---

## Verification — confirm everything wires correctly

After provisioning + pointing the issuer at the key:

```bash
# 1. Issuer comes up clean
curl -s http://your-issuer-host:8080/v1/healthz
# → {"status":"ok"}

# 2. JWK shows the KMS ARN as the key_id
curl -s http://your-issuer-host:8080/v1/key | python -m json.tool
# → "kid": "arn:aws:kms:us-east-1:123456789012:key/abcd...",
#    "kty": "OKP", "crv": "Ed25519",
#    "x": "<32-byte ed25519 pubkey as base64url>",
#    "status": "active"

# 3. Sign a sample envelope end-to-end
cd <repo>
python demo.py
# → [OK] SIGNATURE VERIFIED -- envelope is cryptographically valid
#   (note: demo.py uses MOCK_MODE for scoring; signing IS real KMS)
```

If step 2 returns `kid: ephemeral-...`, the issuer is still on the dev keypair — check `KMS_MODE=aws` is set.

If step 3 fails with `kms: AccessDenied`, the runtime principal doesn't have the policy attached. Check `aws sts get-caller-identity` from inside the pod / instance and confirm it matches the role you provisioned.

---

## Rotation

When you need to rotate (scheduled or after compromise):

```bash
# Retire the current key (normal scheduled rotation):
python scripts/rotate-key.py --issuer https://issuer.example.com --historical ./historical-keys.json

# Provision a new key:
cd deploy/aws-kms && terraform apply -var='environment=prod-v2'

# Mount the historical-keys.json into the new issuer pod, point KMS_KEY_ARN at the new key, restart.
```

The old envelopes remain verifiable because `/v1/keys` continues serving the retired JWK.

For compromise rotation, add `--revoke` to the rotate-key call — the old key gets `status: revoked` and downstream verifiers will REJECT all envelopes signed under it.

See [`docs/RUNBOOK.md`](../../docs/RUNBOOK.md) § R1 for the full incident playbook.

---

## Cost guardrails

KMS asymmetric Sign costs $0.03 per 10,000 signatures. At Mimir's peak measured throughput (1500 RPS):

| Volume | KMS Sign cost |
|---|---|
| 1k envelopes/day | ~$0.003/day |
| 1M envelopes/day | ~$3/day |
| 100M envelopes/day | ~$300/day |
| 1B envelopes/day | ~$3000/day |

If you're approaching the bottom rows, evaluate:
1. **CloudHSM** — flat-rate hardware-bound HSM, ~$1k/month, cheaper at very high volume.
2. **Merkle batching** — one KMS Sign per N envelopes; envelopes get a Merkle inclusion proof. ROADMAP item Q2 2027.

Until then, **$3/day at 1M envelopes/day is the production budget line item** — comfortable for a beta launch.
