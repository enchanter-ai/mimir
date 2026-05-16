//! CLI entry point for the Mimir provenance envelope verifier.
//!
//! Usage:
//!   mimir-verify --envelope <path-to-envelope.json> --jwk <path-to-jwk.json>
//!
//! Exit codes:
//!   0  — PASS (signature verified)
//!   1  — FAIL (verification error; message printed to stderr)

use mimir_verifier::verify_envelope;
use std::fs;
use std::process;

fn usage() -> ! {
    eprintln!("Usage: mimir-verify --envelope <path> --jwk <path>");
    process::exit(2);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();

    let mut envelope_path: Option<String> = None;
    let mut jwk_path: Option<String> = None;

    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--envelope" => {
                i += 1;
                envelope_path = args.get(i).cloned();
            }
            "--jwk" => {
                i += 1;
                jwk_path = args.get(i).cloned();
            }
            _ => usage(),
        }
        i += 1;
    }

    let envelope_path = envelope_path.unwrap_or_else(|| usage());
    let jwk_path = jwk_path.unwrap_or_else(|| usage());

    let envelope_json = fs::read_to_string(&envelope_path).unwrap_or_else(|e| {
        eprintln!("ERROR: cannot read envelope file '{}': {}", envelope_path, e);
        process::exit(2);
    });

    let jwk_json = fs::read_to_string(&jwk_path).unwrap_or_else(|e| {
        eprintln!("ERROR: cannot read JWK file '{}': {}", jwk_path, e);
        process::exit(2);
    });

    match verify_envelope(&envelope_json, &jwk_json) {
        Ok(()) => {
            println!("PASS — signature verified");
            process::exit(0);
        }
        Err(e) => {
            eprintln!("FAIL — {}", e);
            process::exit(1);
        }
    }
}
