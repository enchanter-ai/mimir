## Attack 03 — Signature replaced with 64 zero bytes

The `signature.value` field has been replaced with the base64url encoding of 64 zero bytes (`AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`). This is the trivially forgeable 'null signature'. A correct verifier **MUST** reject this via `VE-008` (signature invalid). Spec §9.3 step 10 verifies the signature bytes over the canonical form; §10.2 step 11 maps failure to `VE-008`. A verifier that accepts all-zero signatures is trivially bypassed.
