module github.com/enchanter-ai/mimir/bench

go 1.24

require (
	github.com/enchanter-ai/mimir/issuer v0.0.0
	github.com/oasisprotocol/curve25519-voi v0.0.0-20230904125328-1f23a7beb09a
)

require (
	golang.org/x/crypto v0.0.0-20220321153916-2c7772ba3064 // indirect
	golang.org/x/sys v0.0.0-20220325203850-36772127a21f // indirect
)

replace github.com/enchanter-ai/mimir/issuer => ../issuer
