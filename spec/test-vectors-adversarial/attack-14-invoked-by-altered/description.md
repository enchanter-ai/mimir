## Attack 14 — invoked_by altered post-signing

The envelope's `invoked_by` has been changed from the original signed value (`did:enchanter:unverified` in the baseline) to a fabricated `did:jwk:fake…`. An attacker who captures an honest envelope cannot re-attribute the request to a different client without breaking the signature. `invoked_by` is part of the canonical bytes the Producer signed (§6.4); any post-signing modification fails verification with `VE-008`. Verdict: **REJECT**. Spec §6.4, §6.11 (ClientIdentityProof extension binding), §10.2.
