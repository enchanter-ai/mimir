package kms

import (
	"crypto/rand"
	"encoding/hex"
)

// randomHex8 returns 8 random hex characters (4 bytes of entropy).
// Used to generate short unique suffixes for ephemeral key IDs.
func randomHex8() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is a catastrophic OS error; panic is appropriate.
		panic("kms: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
