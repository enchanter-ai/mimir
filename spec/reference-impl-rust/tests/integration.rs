//! Integration tests for the Mimir provenance envelope verifier.
//!
//! Test 1 – `fixed_fixture`: verifies a hard-coded envelope + JWK captured from a
//! real Go issuer run.  This is the core independence test: the Rust verifier must
//! agree with an envelope it had no part in producing.
//!
//! Test 2 – `round_trip_real_issuer`: starts the Go issuer, POSTs a fresh
//! attestation request, fetches the JWK, and verifies.  Skipped gracefully if `go`
//! is not on PATH.

use mimir_verifier::verify_envelope;

// ─────────────────────────────────────────────────────────────────────────────
// Test 1: fixed fixture (hard-coded from a real Go issuer run 2026-05-13)
// ─────────────────────────────────────────────────────────────────────────────

/// Envelope captured from the live Go issuer (see fixtures/ dir for full JSON).
/// The key is ephemeral and was live at the time of capture.
const FIXTURE_ENVELOPE: &str = include_str!("../fixtures/envelope.json");
const FIXTURE_JWK: &str = include_str!("../fixtures/jwk.json");

#[test]
fn fixed_fixture_passes() {
    let result = verify_envelope(FIXTURE_ENVELOPE, FIXTURE_JWK);
    assert!(
        result.is_ok(),
        "fixed fixture verification failed: {:?}",
        result.unwrap_err()
    );
}

#[test]
fn tampered_signature_fails() {
    // Flip one base64 character in signature.value → should fail VE-008
    let tampered = FIXTURE_ENVELOPE.replace(
        // replace first char of sig value with a different char
        "\"value\":",
        "\"value\":\"AAAA_tampered_",
    );
    // The replacement produces malformed JSON — use a more surgical tamper.
    // Instead: parse, mutate, re-serialize.
    let mut env: serde_json::Value = serde_json::from_str(FIXTURE_ENVELOPE).unwrap();
    let original_sig = env["signature"]["value"]
        .as_str()
        .unwrap()
        .to_string();
    // Flip last character
    let mut chars: Vec<char> = original_sig.chars().collect();
    let last = chars.last_mut().unwrap();
    *last = if *last == 'A' { 'B' } else { 'A' };
    let tampered_sig: String = chars.iter().collect();
    env["signature"]["value"] = serde_json::Value::String(tampered_sig);
    let tampered_json = serde_json::to_string(&env).unwrap();

    let result = verify_envelope(&tampered_json, FIXTURE_JWK);
    assert!(
        result.is_err(),
        "expected tampered signature to fail, but it passed"
    );
    let err_str = result.unwrap_err().to_string();
    assert!(
        err_str.contains("VE-008") || err_str.contains("signature invalid"),
        "expected VE-008 error, got: {}",
        err_str
    );
}

#[test]
fn tampered_field_fails() {
    // Modify a signed field (invoked_by) → canonical form changes → sig fails
    let mut env: serde_json::Value = serde_json::from_str(FIXTURE_ENVELOPE).unwrap();
    env["invoked_by"] = serde_json::Value::String("did:attacker:injected".to_string());
    let tampered_json = serde_json::to_string(&env).unwrap();

    let result = verify_envelope(&tampered_json, FIXTURE_JWK);
    assert!(
        result.is_err(),
        "envelope with tampered invoked_by should fail"
    );
}

#[test]
fn missing_signature_value_returns_ve007() {
    let mut env: serde_json::Value = serde_json::from_str(FIXTURE_ENVELOPE).unwrap();
    // Remove signature.value
    env["signature"]
        .as_object_mut()
        .unwrap()
        .remove("value");
    let json = serde_json::to_string(&env).unwrap();

    let result = verify_envelope(&json, FIXTURE_JWK);
    assert!(result.is_err());
    let err_str = result.unwrap_err().to_string();
    assert!(
        err_str.contains("VE-007"),
        "expected VE-007, got: {}",
        err_str
    );
}

#[test]
fn wrong_public_key_fails() {
    // A different random JWK (valid OKP/Ed25519 format but wrong key)
    let wrong_jwk = r#"{
        "kty": "OKP",
        "crv": "Ed25519",
        "x": "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo",
        "kid": "wrong-key",
        "use": "sig"
    }"#;
    let result = verify_envelope(FIXTURE_ENVELOPE, wrong_jwk);
    assert!(
        result.is_err(),
        "wrong public key should fail verification"
    );
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 2: round-trip via real Go issuer
// ─────────────────────────────────────────────────────────────────────────────

/// Check if `go` is available on PATH.
fn go_available() -> bool {
    std::process::Command::new("go")
        .arg("version")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Start the Go issuer on a free port, run the closure, then kill it.
fn with_issuer<F: FnOnce(u16)>(f: F) {
    let port: u16 = 18181; // use a non-default port to avoid collision
    let issuer_dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../issuer");

    let mut child = std::process::Command::new("go")
        .arg("run")
        .arg(".")
        .current_dir(&issuer_dir)
        .env("ISSUER_PORT", port.to_string())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .expect("failed to spawn go issuer");

    // Poll /v1/healthz until ready (max 30 s)
    let base = format!("http://localhost:{}", port);
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(30);
    let mut ready = false;
    while std::time::Instant::now() < deadline {
        if let Ok(resp) = reqwest::blocking::get(format!("{}/v1/healthz", base)) {
            if resp.status().is_success() {
                ready = true;
                break;
            }
        }
        std::thread::sleep(std::time::Duration::from_millis(500));
    }

    if ready {
        f(port);
    }

    // Kill issuer regardless of test outcome.
    let _ = child.kill();
    let _ = child.wait();

    assert!(ready, "Go issuer did not become healthy within 30s");
}

#[test]
fn round_trip_real_issuer() {
    if !go_available() {
        eprintln!("SKIP: `go` not found on PATH — skipping round-trip test");
        return;
    }

    with_issuer(|port| {
        let base = format!("http://localhost:{}", port);
        let client = reqwest::blocking::Client::new();

        // POST /v1/attest
        let attest_body = serde_json::json!({
            "request": {
                "name": "rust_integration_test",
                "arguments": {"query": "independent verifier test 2026-05-13"}
            },
            "result": {
                "tool_use_id": "tu_rust_integration_001",
                "content": [{"type": "text", "text": "Rust independent verifier is running."}]
            },
            "tool_id": "did:web:demo.enchanter-labs.io:tools:rust-test",
            "tool_version": "0.1.0"
        });

        let attest_resp: serde_json::Value = client
            .post(format!("{}/v1/attest", base))
            .json(&attest_body)
            .send()
            .expect("POST /v1/attest failed")
            .json()
            .expect("attest response not JSON");

        let envelope = &attest_resp["envelope"];
        let envelope_json = serde_json::to_string(envelope).expect("serialize envelope");

        // GET /v1/key
        let jwk: serde_json::Value = client
            .get(format!("{}/v1/key", base))
            .send()
            .expect("GET /v1/key failed")
            .json()
            .expect("key response not JSON");
        let jwk_json = serde_json::to_string(&jwk).expect("serialize JWK");

        // Verify using the independent Rust verifier
        let result = verify_envelope(&envelope_json, &jwk_json);
        assert!(
            result.is_ok(),
            "round-trip verification failed: {:?}",
            result.unwrap_err()
        );

        println!(
            "round_trip_real_issuer PASS — tool_call_id={}",
            envelope["tool_call_id"].as_str().unwrap_or("?")
        );
    });
}
