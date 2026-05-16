// payloads.go generates synthetic request bodies for the MCP Provenance Issuer benchmark.
package main

import (
	"fmt"
	"strings"
)

// SizeClass labels the payload size bucket.
type SizeClass string

const (
	SizeSmall  SizeClass = "small"  // < 1 KB
	SizeMedium SizeClass = "medium" // 1–10 KB
	SizeLarge  SizeClass = "large"  // 10–100 KB
)

// Payload is a pre-built AttestRequest body ready for HTTP POST.
type Payload struct {
	Body      []byte
	SizeClass SizeClass
	Index     int
}

// GeneratePayloads builds n distinct sample payloads distributed across
// small (<1 KB), medium (1–10 KB), and large (10–100 KB) size classes.
// Distribution: 40% small, 40% medium, 20% large.
func GeneratePayloads(n int) []Payload {
	payloads := make([]Payload, 0, n)

	smallCount := n * 4 / 10
	mediumCount := n * 4 / 10
	largeCount := n - smallCount - mediumCount

	for i := 0; i < smallCount; i++ {
		payloads = append(payloads, buildPayload(i, SizeSmall))
	}
	for i := 0; i < mediumCount; i++ {
		payloads = append(payloads, buildPayload(smallCount+i, SizeMedium))
	}
	for i := 0; i < largeCount; i++ {
		payloads = append(payloads, buildPayload(smallCount+mediumCount+i, SizeLarge))
	}

	return payloads
}

func buildPayload(idx int, sc SizeClass) Payload {
	var resultText string
	switch sc {
	case SizeSmall:
		// ~200 bytes of content
		resultText = fmt.Sprintf("Short result for payload %d. This is a minimal response.", idx)
	case SizeMedium:
		// ~3 KB of content
		resultText = buildText(idx, 3000)
	case SizeLarge:
		// ~40 KB of content
		resultText = buildText(idx, 40000)
	}

	body := fmt.Sprintf(`{
  "tool_id": "did:web:example.com:tools:bench-tool-%d",
  "tool_version": "1.0.%d",
  "request": {
    "name": "bench_tool_%d",
    "arguments": {
      "query": "benchmark query number %d",
      "index": %d,
      "flags": ["flag_a_%d", "flag_b_%d", "flag_c_%d"]
    }
  },
  "result": {
    "content": [
      {
        "type": "text",
        "text": %q
      }
    ]
  }
}`, idx, idx, idx, idx, idx, idx, idx, idx, resultText)

	return Payload{
		Body:      []byte(body),
		SizeClass: sc,
		Index:     idx,
	}
}

// buildText constructs a deterministic text string of approximately targetLen bytes.
func buildText(seed, targetLen int) string {
	unit := fmt.Sprintf(
		"Payload %d word%d lorem ipsum dolor sit amet consectetur adipiscing elit "+
			"sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. ",
		seed, seed,
	)
	var sb strings.Builder
	sb.Grow(targetLen + len(unit))
	for sb.Len() < targetLen {
		sb.WriteString(unit)
	}
	return sb.String()[:targetLen]
}
