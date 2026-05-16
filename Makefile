# Mimir top-level Makefile — convenience entry points for the common ops.
#
# All targets are PHONY; nothing depends on file timestamps. Targets in this
# file are stable; per-component Makefiles (issuer/Makefile, scoring/Makefile)
# stay focused on their language ecosystem.

.PHONY: all test test-issuer test-anchor test-rust test-adversarial \
        compile docker docker-issuer docker-scoring \
        verify-build sbom \
        clean help

help:
	@echo "Mimir — available targets:"
	@echo "  make test               run the full test trio (issuer + anchor + rust + adversarial)"
	@echo "  make test-issuer        Go tests in issuer/"
	@echo "  make test-anchor        Go tests in anchor/go/ (12/12 simulated EVM)"
	@echo "  make test-rust          cargo test in spec/reference-impl-rust/ (6/6)"
	@echo "  make test-adversarial   verify-all.py against 12 attack vectors"
	@echo "  make compile            recompile contracts via solc-js"
	@echo "  make docker             build both Docker images (issuer + scoring)"
	@echo "  make verify-build       reproducibility check (re-emit bytecode, compare on-chain)"
	@echo "  make sbom               generate CycloneDX SBOMs for every ecosystem"
	@echo "  make clean              remove generated artifacts (sbom/, dist/, target/)"

# ─── Tests ──────────────────────────────────────────────────────────────

test: test-issuer test-anchor test-rust test-adversarial

test-issuer:
	cd issuer && go test -timeout 120s ./...

test-anchor:
	cd anchor/go && CGO_ENABLED=0 go test -timeout 120s ./...

test-rust:
	cd spec/reference-impl-rust && cargo test --release

test-adversarial:
	python spec/test-vectors-adversarial/verify-all.py

# ─── Contract compile + reproducibility ─────────────────────────────────

compile:
	cd anchor && node compile.js

verify-build:
	bash scripts/verify-build.sh \
		CONTRACT_ADDRESS=$${CONTRACT_ADDRESS:-} \
		RPC_URL=$${RPC_URL:-}

sbom:
	bash scripts/gen-sbom.sh

# ─── Docker ─────────────────────────────────────────────────────────────

docker: docker-issuer docker-scoring

docker-issuer:
	docker build -f issuer/Dockerfile -t mimir-issuer:dev issuer/

docker-scoring:
	docker build -f scoring/Dockerfile -t mimir-scoring:dev scoring/

# ─── Cleanup ────────────────────────────────────────────────────────────

clean:
	rm -rf sbom/
	rm -rf scoring/dist/
	rm -rf spec/reference-impl-rust/target/
	rm -f issuer/issuer issuer/issuer.exe
	rm -f anchor/go/anchor anchor/go/anchor.exe
