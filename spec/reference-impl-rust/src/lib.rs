//! Independent Rust verifier for Mimir provenance envelopes.
//!
//! Implements spec §§ 9.2, 10.1, 10.2 from the Enchanter Labs Tool-Call Provenance
//! Envelope specification (2026-05-13).
//!
//! This implementation is deliberately independent: it does NOT import any code from
//! the Go issuer. The RFC 8785 canonicalization is derived from the RFC text and the
//! spec's §§ 9.2 prose, not from `../../issuer/canonicalize/`.

use base64ct::{Base64UrlUnpadded, Encoding};
use ed25519_dalek::{Signature, VerifyingKey};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::fmt;

// ─────────────────────────────────────────────────────────────────────────────
// Error taxonomy
// ─────────────────────────────────────────────────────────────────────────────

/// Verification error codes aligned with spec § 10 VE-xxx codes.
#[derive(Debug, thiserror::Error)]
pub enum VerifyError {
    /// VE-001 – envelope JSON is not a valid JSON object.
    #[error("VE-001: envelope JSON parse failed: {0}")]
    ParseError(String),

    /// VE-001 – missing required field.
    #[error("VE-001: missing required field '{0}'")]
    MissingField(&'static str),

    /// VE-006 – alg in protected_header does not match the profile in `version`.
    #[error("VE-006: alg mismatch: version declares '{declared}' but protected_header.alg is '{found}'")]
    AlgMismatch { declared: String, found: String },

    /// VE-007 – signature.value is absent.
    #[error("VE-007: signature.value is absent")]
    SignatureMissing,

    /// VE-007 – signature.value base64url decode failed.
    #[error("VE-007: signature.value base64url decode failed: {0}")]
    SignatureDecodeError(String),

    /// VE-007 – signature byte length is wrong for Ed25519 (must be 64).
    #[error("VE-007: signature byte length {0}, expected 64")]
    SignatureLengthError(usize),

    /// JWK parse error (no VE code — consumer-side key supply issue).
    #[error("JWK error: {0}")]
    JwkError(String),

    /// VE-008 – Ed25519 signature verification failed.
    #[error("VE-008: signature invalid — {0}")]
    SignatureInvalid(String),

    /// Canonical-form serialization error (internal).
    #[error("canonical form error: {0}")]
    CanonicalError(String),
}

// ─────────────────────────────────────────────────────────────────────────────
// Envelope / JWK structs (serde deserialization only)
// ─────────────────────────────────────────────────────────────────────────────

/// Minimal protected header — only the fields the verifier needs.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct ProtectedHeader {
    pub alg: String,
    pub key_id: String,
}

/// Signature sub-object.  `value` is Option because it is absent during signing.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct EnvelopeSignature {
    pub protected_header: ProtectedHeader,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub value: Option<String>,
}

/// Minimal JWK for an OKP/Ed25519 key as returned by the issuer's `/v1/key`.
#[derive(Debug, Deserialize)]
pub struct JwkPublicKey {
    /// `kty` MUST be "OKP".
    pub kty: String,
    /// `crv` MUST be "Ed25519".
    pub crv: String,
    /// Raw 32-byte public key, base64url-encoded (no padding), per RFC 8037.
    pub x: String,
    /// Key identifier.
    pub kid: Option<String>,
}

// ─────────────────────────────────────────────────────────────────────────────
// RFC 8785 JSON Canonicalization Scheme (JCS)
//
// Implemented from the RFC 8785 text (https://www.rfc-editor.org/rfc/rfc8785)
// and the spec § 9.2 prose.  NOT derived from the Go issuer's canonicalize/.
//
// Rules:
//   1. Serialise as UTF-8.
//   2. No insignificant whitespace.
//   3. Object keys are sorted in ascending Unicode code-point order.
//   4. The sort is applied recursively to nested objects.
//   5. String escaping: only the characters mandated by RFC 8785 § 3.2.2.2 are
//      escaped:
//        - U+0022  "   → \"
//        - U+005C  \   → \\
//        - U+0000–U+001F  control chars → \uXXXX (lower-case hex)
//   6. Numbers follow IEEE 754 double serialisation rules per RFC 8785 § 3.2.2.3.
//      For well-formed JSON that serde_json already parsed we rely on serde_json's
//      serialiser which passes through the original representation; the round-trip
//      through `Value` is sufficient for the integer/float inputs that appear in
//      Mimir envelopes.
// ─────────────────────────────────────────────────────────────────────────────

/// Canonicalize a `serde_json::Value` per RFC 8785 JCS.
/// Returns UTF-8 bytes.
pub fn jcs_canonicalize(v: &Value) -> Result<Vec<u8>, VerifyError> {
    let mut buf = String::with_capacity(256);
    write_jcs(v, &mut buf)?;
    Ok(buf.into_bytes())
}

fn write_jcs(v: &Value, out: &mut String) -> Result<(), VerifyError> {
    match v {
        Value::Null => out.push_str("null"),
        Value::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
        Value::Number(n) => {
            // RFC 8785 § 3.2.2.3: serialise using the same rules as ECMAScript
            // `JSON.stringify`.  serde_json's Display impl for Number already does this
            // (integers → no decimal point, floats → shortest round-trip representation).
            out.push_str(&n.to_string());
        }
        Value::String(s) => {
            write_jcs_string(s, out);
        }
        Value::Array(arr) => {
            out.push('[');
            for (i, item) in arr.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_jcs(item, out)?;
            }
            out.push(']');
        }
        Value::Object(map) => {
            // RFC 8785 § 3.2.3: sort keys by Unicode code-point order.
            // serde_json's `Map` uses a BTreeMap when the "preserve_order" feature
            // is absent (default), so keys are already sorted.  We sort explicitly
            // here to be safe regardless of feature flags.
            let mut pairs: Vec<(&String, &Value)> = map.iter().collect();
            pairs.sort_by(|a, b| {
                // RFC 8785 sort order: compare UTF-16 code units (same as ECMAScript).
                // For BMP-only strings this is identical to Unicode scalar value order,
                // but for supplementary characters the sort by UTF-16 surrogate pairs
                // differs.  Mimir envelope keys are all ASCII so both are equivalent.
                a.0.encode_utf16()
                    .collect::<Vec<_>>()
                    .cmp(&b.0.encode_utf16().collect::<Vec<_>>())
            });
            out.push('{');
            for (i, (k, val)) in pairs.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_jcs_string(k, out);
                out.push(':');
                write_jcs(val, out)?;
            }
            out.push('}');
        }
    }
    Ok(())
}

/// RFC 8785 § 3.2.2.2 string serialisation.
fn write_jcs_string(s: &str, out: &mut String) {
    out.push('"');
    for ch in s.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\u{0008}' => out.push_str("\\b"),
            '\u{0009}' => out.push_str("\\t"),
            '\u{000A}' => out.push_str("\\n"),
            '\u{000C}' => out.push_str("\\f"),
            '\u{000D}' => out.push_str("\\r"),
            c if (c as u32) < 0x20 => {
                // Other C0 controls — \uXXXX lower-case
                out.push_str(&format!("\\u{:04x}", c as u32));
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

// ─────────────────────────────────────────────────────────────────────────────
// Spec § 9.2 – Compute Canonical Form
//
// 1. Take the envelope JSON value.
// 2. Remove `signature.value` (keep `signature.protected_header`).
// 3. RFC 8785 canonicalize.
// 4. Return UTF-8 bytes.
// ─────────────────────────────────────────────────────────────────────────────

pub fn compute_canonical_form(envelope_json: &str) -> Result<Vec<u8>, VerifyError> {
    let mut v: Value = serde_json::from_str(envelope_json)
        .map_err(|e| VerifyError::ParseError(e.to_string()))?;

    // Step 2: remove signature.value only — keep signature.protected_header.
    if let Some(sig_obj) = v.get_mut("signature").and_then(Value::as_object_mut) {
        sig_obj.remove("value");
    }

    jcs_canonicalize(&v)
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

/// Verify a Mimir provenance envelope against a JWK public key.
///
/// Implements spec §§ 9.2, 10.1, 10.2.
///
/// # Arguments
///
/// * `envelope_json` – the full envelope JSON string (as returned by `/v1/attest`
///   under the `envelope` key).
/// * `jwk_json` – the JWK public key JSON string (as returned by `/v1/key`).
///
/// # Returns
///
/// `Ok(())` on success (PASS).  `Err(VerifyError)` on any failure (FAIL).
pub fn verify_envelope(envelope_json: &str, jwk_json: &str) -> Result<(), VerifyError> {
    // ── §10.1: Syntactically well-formed ──────────────────────────────────────
    let envelope: Value = serde_json::from_str(envelope_json)
        .map_err(|e| VerifyError::ParseError(e.to_string()))?;

    // Required top-level fields per § 6.1.
    for field in &[
        "version",
        "tool_call_id",
        "tool_id",
        "tool_version",
        "invoked_at",
        "invoked_by",
        "request_digest",
        "result_digest",
        "sources",
        "signature",
    ] {
        if envelope.get(field).is_none() {
            return Err(VerifyError::MissingField(field));
        }
    }

    let sig_obj = envelope
        .get("signature")
        .and_then(Value::as_object)
        .ok_or(VerifyError::MissingField("signature"))?;

    // §10.1 (5): protected_header must be present with alg and key_id.
    let protected = sig_obj
        .get("protected_header")
        .and_then(Value::as_object)
        .ok_or(VerifyError::MissingField("signature.protected_header"))?;

    let _alg = protected
        .get("alg")
        .and_then(Value::as_str)
        .ok_or(VerifyError::MissingField("signature.protected_header.alg"))?;

    let _key_id = protected
        .get("key_id")
        .and_then(Value::as_str)
        .ok_or(VerifyError::MissingField("signature.protected_header.key_id"))?;

    // §10.2 step 2: signature.value must be present.
    let sig_value = sig_obj
        .get("value")
        .and_then(Value::as_str)
        .ok_or(VerifyError::SignatureMissing)?;

    // §10.2 step 3: alg in protected_header must match version profile.
    let version = envelope
        .get("version")
        .and_then(Value::as_str)
        .unwrap_or("");
    let expected_alg = alg_from_version(version);
    if let Some(expected) = expected_alg {
        if _alg != expected {
            return Err(VerifyError::AlgMismatch {
                declared: expected.to_string(),
                found: _alg.to_string(),
            });
        }
    }

    // ── §9.2: Compute canonical form ──────────────────────────────────────────
    let canonical_bytes = compute_canonical_form(envelope_json)?;

    // ── §10.2 step 9: decode signature bytes ─────────────────────────────────
    let sig_bytes = Base64UrlUnpadded::decode_vec(sig_value)
        .map_err(|e| VerifyError::SignatureDecodeError(e.to_string()))?;

    if sig_bytes.len() != 64 {
        return Err(VerifyError::SignatureLengthError(sig_bytes.len()));
    }

    // ── Parse JWK ─────────────────────────────────────────────────────────────
    let jwk: JwkPublicKey = serde_json::from_str(jwk_json)
        .map_err(|e| VerifyError::JwkError(e.to_string()))?;

    if jwk.kty != "OKP" {
        return Err(VerifyError::JwkError(format!(
            "expected kty=OKP, got '{}'",
            jwk.kty
        )));
    }
    if jwk.crv != "Ed25519" {
        return Err(VerifyError::JwkError(format!(
            "expected crv=Ed25519, got '{}'",
            jwk.crv
        )));
    }

    let pubkey_bytes = Base64UrlUnpadded::decode_vec(&jwk.x)
        .map_err(|e| VerifyError::JwkError(format!("JWK.x base64url decode: {e}")))?;

    let pubkey_arr: [u8; 32] = pubkey_bytes
        .try_into()
        .map_err(|_| VerifyError::JwkError("JWK.x must be 32 bytes".to_string()))?;

    let verifying_key = VerifyingKey::from_bytes(&pubkey_arr)
        .map_err(|e| VerifyError::JwkError(format!("invalid Ed25519 public key: {e}")))?;

    let sig_arr: [u8; 64] = sig_bytes.try_into().unwrap(); // length already checked
    let signature = Signature::from_bytes(&sig_arr);

    // ── §10.2 step 10: verify ─────────────────────────────────────────────────
    use ed25519_dalek::Verifier;
    verifying_key
        .verify(&canonical_bytes, &signature)
        .map_err(|e| VerifyError::SignatureInvalid(e.to_string()))?;

    Ok(())
}

/// Extract the expected algorithm string from a profile identifier.
/// Profile: `mcp-provenance/YYYY-MM-DD-{alg-suite}`.
fn alg_from_version(version: &str) -> Option<&'static str> {
    if version.ends_with("-ed25519") {
        Some("Ed25519")
    } else if version.ends_with("-ecdsa-p256") {
        Some("ES256")
    } else {
        None
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// Unit tests for the JCS canonicalizer (independent of the issuer)
// ─────────────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod jcs_tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn empty_object() {
        let v = json!({});
        let out = jcs_canonicalize(&v).unwrap();
        assert_eq!(out, b"{}");
    }

    #[test]
    fn single_string_field() {
        let v = json!({"z": "a", "a": "z"});
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        assert_eq!(out, r#"{"a":"z","z":"a"}"#);
    }

    #[test]
    fn nested_object_keys_sorted() {
        let v = json!({"b": {"d": 1, "c": 2}, "a": true});
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        // top-level: a before b; nested: c before d
        assert_eq!(out, r#"{"a":true,"b":{"c":2,"d":1}}"#);
    }

    #[test]
    fn string_escaping() {
        // tab and newline must be \t and \n
        let v = json!({"k": "a\tb\nc"});
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        assert_eq!(out, r#"{"k":"a\tb\nc"}"#);
    }

    #[test]
    fn string_escape_control_char() {
        let v = Value::String("\u{0001}".to_string());
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        assert_eq!(out, "\"\\u0001\"");
    }

    #[test]
    fn null_bool_number() {
        let v = json!({"a": null, "b": true, "c": false, "d": 42, "e": 3.14});
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        // Keys sorted: a b c d e
        assert_eq!(out, r#"{"a":null,"b":true,"c":false,"d":42,"e":3.14}"#);
    }

    #[test]
    fn array_preserves_order() {
        let v = json!({"arr": [3, 1, 2]});
        let out = String::from_utf8(jcs_canonicalize(&v).unwrap()).unwrap();
        assert_eq!(out, r#"{"arr":[3,1,2]}"#);
    }

    #[test]
    fn canonical_form_strips_signature_value_only() {
        let envelope_json = r#"{
            "version": "mcp-provenance/2026-05-13-ed25519",
            "signature": {
                "protected_header": {"alg": "Ed25519", "key_id": "kid-1"},
                "value": "AAAA"
            },
            "tool_call_id": "abc"
        }"#;
        let canonical = compute_canonical_form(envelope_json).unwrap();
        let s = String::from_utf8(canonical).unwrap();
        // value must be absent, protected_header must be present
        assert!(!s.contains("AAAA"), "signature.value must be stripped");
        assert!(
            s.contains("protected_header"),
            "protected_header must be retained"
        );
        assert!(s.contains("Ed25519"), "alg must be retained");
    }
}
