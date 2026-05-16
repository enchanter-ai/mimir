# Mimir — Operations Runbook

Production-incident playbooks for the operator on call. Each entry covers a single failure mode: **symptom**, **immediate action**, **root-cause analysis**, **post-incident**.

Last revised: 2026-05-16. Pin a specific commit SHA before running these in anger — runbooks rot fast.

---

## R1 — Signing key compromise

**Symptom.** The KMS key material has been exposed (employee laptop seized, leaked logs, AWS access-key compromise, supply-chain breach in `aws-sdk-go-v2`), OR an audit finds the key was used to sign an envelope you cannot account for.

**Immediate action (target: <15 minutes from detection).**

1. **Rotate the AWS KMS key state.**
   ```
   aws kms disable-key --key-id <ARN>
   ```
   This stops further `Sign` operations. Envelopes already signed remain on-chain; they are now suspect, not invalidated.
2. **Generate a replacement key.**
   ```
   aws kms create-key --key-spec ECC_NIST_EDWARDS25519 --key-usage SIGN_VERIFY \
       --description "mimir-issuer post-incident-$(date +%s)"
   ```
3. **Append the compromised key to `historical-keys.json`** with `"status":"revoked"` (not `"retired"` — revoked means downstream verifiers should reject envelopes signed under it).
4. **Restart the issuer** with `KMS_KEY_ARN=<new-arn>`. The new key's JWK appears in `/v1/keys` as `status:"active"`; the old one as `status:"revoked"`.
5. **Public disclosure.** Post the compromised `kid` to a public location (GitHub Discussions + status page). Verifiers that fetch `/v1/keys` will automatically reject the revoked key; verifiers that have it cached must invalidate.

**Root cause.** Document in the incident postmortem:
- How the key was exposed.
- Time window between exposure and detection.
- All envelopes signed in that window — they need a manual fraud audit.

**Post-incident.**
- File an audit finding under category `key-management`.
- Rotate any IAM credentials that touched `kms:Sign` on the old key.
- Update [`docs/deployments.md`](deployments.md) noting the rotated `kid`.
- Update [`SECURITY.md`](../SECURITY.md) acknowledging the incident in a hall-of-shame section.

---

## R2 — Anthropic API hard-down

**Symptom.** `/v1/score` returns 502 Bad Gateway for >60 seconds; Claude API status page reports an outage; `quality-oracle-scoring` logs show `503 from anthropic.com` repeatedly.

**Immediate action.**

1. **Flip `MOCK_MODE=1`** on the scoring service to keep the pipeline functional for non-DEPLOY traffic. **Do NOT publish MOCK_MODE envelopes as DEPLOY-tier** — the verdict will be a deterministic 9.2 stub which is fraud-equivalent if signed under the production key.
2. **Pause the issuer's signing path** OR add a header check that rejects any attest request whose accompanying score was generated in MOCK_MODE.
3. **Communicate.** Post to the status page that DEPLOY-tier signing is paused; cryptographic-only signing remains available.

**Root cause check.**
- Is Anthropic returning a recognised 5xx, or are we hitting a 4xx that suggests credential failure? `400 invalid_request_error / your credit balance is too low` requires topping up; not the same incident.
- Is the issue confined to Sonnet 4.6 or all models? If only one model is impacted, the calibration data may not transfer to a fallback model.

**Post-incident.**
- Add the incident window to the calibration set's known-skipped period.
- If outage > 4 hours, evaluate adding a secondary judge model (Sonnet via another provider, Mistral, etc.) as a fallback. **Calibrate it first** — don't silently swap judges without re-running `scoring/calibration/run_calibration.py`.

---

## R3 — Sepolia / mainnet reorg invalidates an anchor

**Symptom.** A transaction confirmed at block N is re-org'd; the `register()` we sent in that block no longer exists on the canonical chain.

**Immediate action.**

1. **Detect.** A canonical-chain `eth_getTransactionReceipt(<txhash>)` returns `null` after previously succeeding.
2. **Re-anchor.** Re-submit the same `register(digest, issuer, expiry)` from the same nonce — the call is idempotent on the contract side (it reverts with `DigestAlreadyRegistered` if the prior version persists; succeeds if it was orphaned).
3. **Log.** Emit a structured warn-level event with both the original tx hash and the replacement.

**Root cause.** Most likely either:
- The deploying chain is reorg-prone (Holesky has had several reorgs > 3 blocks).
- Our wait was too short — we accepted the receipt before block finality (Ethereum L1: ~12 blocks; Sepolia: ~64 epochs / ~6 minutes).

**Post-incident.**
- Increase the receipt-confirmation depth in [`anchor/go/anchor.go:WaitMined`](../anchor/go/anchor.go) (currently waits for receipt only; add `BlocksToConfirm` config).
- Update [`docs/deployments.md`](deployments.md) noting the reorg event + the replacement tx hash.

---

## R4 — Issuer pod OOM-killed or panic-crashes mid-sign

**Symptom.** Kubernetes / Docker reports the issuer container restarting; `/v1/healthz` 5xx for ~5 seconds during pod re-spawn.

**Immediate action.**

1. **Confirm the pipeline.** Run `python demo.py` against the restarted pod. If it returns `[OK] SIGNATURE VERIFIED`, traffic can resume.
2. **Check for in-flight envelopes that were signing when the crash happened.** Mimir doesn't persist envelopes server-side; an in-flight envelope is lost — the client must retry. Idempotency is the client's responsibility (re-sign with the same canonical content produces the same digests but a different signature because Ed25519 is deterministic on private-key + message — actually identical sig — so retries are safe).

**Root cause check.**
- OOM: was the request body unusually large? Add a max-request-size limit (the spec implies 4096 bytes for KMS-signed messages; HTTP body can be much larger because the canonical form is what's signed, not the raw body).
- Panic: structured logs should show the stack. Common cause was a nil `keystore` in early versions — now fixed; if you see it again, file a P1.

**Post-incident.**
- If OOM, set a Kubernetes memory limit + add a `httpReadLimit` middleware (out of scope for v0.1; track as feature work).

---

## R5 — Sepolia public RPC down

**Symptom.** `cmd/deploy` or `cmd/verify` fails with "dial timeout" or "connection refused". Existing on-chain state is unaffected; only new transactions blocked.

**Immediate action.**

1. **Switch RPC endpoint.** `HOLESKY_RPC_URL=https://eth-sepolia.g.alchemy.com/v2/<key>` (Alchemy) or any other Sepolia provider.
2. **Re-run the failed command.** Idempotent: `cmd/deploy` produces a new deployment per invocation (different `nonce` if the previous tx made it on-chain; new contract address otherwise). `cmd/verify` is read-only and safe.

**Root cause.** Public-node providers throttle aggressively. Production should not depend on `publicnode.com`.

**Post-incident.**
- Switch to a paid RPC provider with an SLA for any deployment intended to persist (Alchemy, Infura, QuickNode, Tenderly).

---

## R6 — DPoP / ClientIdentityProof verification breaks (false rejects)

**Symptom.** Legitimate MCP clients report 400s with `client_identity_proof rejected: iat outside acceptable window`.

**Immediate action.**

1. **Check the issuer's clock.** Compare `date -u` on the issuer host vs UTC; > 60 seconds drift is the typical root cause.
2. **Resync NTP.** On Linux: `systemctl restart systemd-timesyncd && timedatectl status`.

**Root cause.**
- Time drift between issuer host and client. Clients sign with their local time; issuer rejects if the gap exceeds `MaxClockSkew` (default 60 s).
- Cloud VMs occasionally lose NTP after a host migration.

**Post-incident.**
- Add NTP-drift monitoring to the deployment.
- If a regression: did `clientid.Verify` start enforcing something it didn't before? Diff `issuer/clientid/dpop.go` against the last known-good commit.

---

## R7 — Slasher's wadSlashed wraps or behaves unexpectedly

**Symptom.** `Slashed` events show wadSlashed > 1e18, or operator's `totalSlashed` grows unboundedly across legitimate slashes.

**Immediate action.**

1. **Pause the registry contract** if a pause guardian exists (v0 does not — see `MimirValidationRegistry.sol` notes). Otherwise: deploy a NEW contract instance and update clients to point at it; the old contract's bad state stays on-chain as a forensic record.
2. **Snapshot the affected operator's stake** at the bad-event block.

**Root cause check.**
- The mocks intentionally don't enforce per-operator caps; the real EigenLayer `AllocationManager` does. If you see this on mainnet, you're not actually hitting our slasher — investigate the AVS wiring.
- v0 contract has no upper bound on cumulative `totalSlashed` per operator. That's correct: a single operator can be slashed multiple times (e.g., from independent fraud proofs), and the total accumulates. Each individual `slash()` call IS bounded at 1e18 = 100%.

**Post-incident.**
- If desired, add a per-operator cap to the registry contract (would require a new contract version + deploy + migrate; out of v0.1 scope).

---

## R8 — σ-bound DEPLOY rate suddenly drops to 0%

**Symptom.** Calibration probe (`poc_translate.py` or the 50-case run) reports `verdict=HOLD` on previously-DEPLOY cases.

**Immediate action.**

1. **Re-run calibration on the latest commit.** `python scoring/calibration/run_calibration.py && python scoring/calibration/analyze_calibration.py`. Check `calibration-report.md` confusion matrix.
2. **Check Anthropic model behavior.** Sonnet 4.6 → 4.7 silent upgrade? Same model with retrained rubric judgment? Either is possible.
3. **Roll back if needed.** Pin to a specific Sonnet version via the `model` parameter in `scoring/src/score.ts` if Anthropic offers it.

**Root cause.**
- σ threshold (0.75) was calibrated against Sonnet 4.6 specifically. If the model changes, recalibrate.
- σ computed over content axes only: if the rubric definitions in `scoring/src/rubric.ts` drifted, the σ shape changes.

**Post-incident.**
- Treat calibration as a CI artifact: run it weekly on a small canary set, alert on > 10% verdict drift.

---

## Escalation contact tree

| Severity | Action |
|---|---|
| **P0** (signing key compromised, contract drained, mass-fraud detected) | Page on-call + execute R1 immediately; do not wait for written escalation. |
| **P1** (one service hard-down, no clean fallback) | Page on-call; coordinated comms via status page within 30 min. |
| **P2** (degraded — DEPLOY rate drops, scoring API slow, calibration drift) | Open incident ticket; investigate within 4 hours. |
| **P3** (single failed envelope, cosmetic issue, doc bug) | File a regular issue; address in normal sprint cadence. |

For the v0 / open-source maintainer phase: escalations route to GitHub Issues with the `incident` label + `security@enchanter.ai` for P0/P1.
