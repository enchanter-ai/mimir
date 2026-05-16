.PHONY: build run test demo tidy

BIN := bin/issuer

build:
	go build -o $(BIN) ./...

run: build
	./$(BIN)

test:
	go test ./... -v

tidy:
	go mod tidy

demo:
	@echo "--- Firing POST /v1/attest ---"
	curl -s -X POST http://localhost:8080/v1/attest \
	  -H "Content-Type: application/json" \
	  -d @sample/request.json | python3 -m json.tool || \
	  curl -s -X POST http://localhost:8080/v1/attest \
	  -H "Content-Type: application/json" \
	  -d @sample/request.json
	@echo ""
	@echo "--- Public key (JWK) ---"
	curl -s http://localhost:8080/v1/key | python3 -m json.tool || \
	  curl -s http://localhost:8080/v1/key
