.PHONY: install build dev test demo lint

## Install dependencies (CI-clean install)
install:
	npm ci

## Compile TypeScript
build:
	npx tsc

## Run server in watch mode (development)
dev:
	npx tsx watch src/server.ts

## Run test suite
test:
	npx vitest run

## Type-check without emitting
lint:
	npx tsc --noEmit

## Demo: POST sample/request.json to /v1/score and print verdict
## Requires: server running on :9090 and ANTHROPIC_API_KEY set.
## Start the server first: make dev
demo:
	@echo "--- Sending sample/request.json to http://localhost:9090/v1/score ---"
	curl -s -X POST http://localhost:9090/v1/score \
	  -H "Content-Type: application/json" \
	  -d @sample/request.json | python3 -m json.tool || \
	  (echo "Tip: start the server first with 'make dev'" && exit 1)
