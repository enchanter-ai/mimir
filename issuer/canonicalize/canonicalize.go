// Package canonicalize implements RFC 8785 JSON Canonicalization Scheme (JCS).
//
// The algorithm:
//  1. Deserialise JSON into a generic Go value (map/slice/scalar).
//  2. Recursively re-serialise with map keys sorted in Unicode code-point order.
//  3. Numbers are serialised without trailing zeros (matching ES6 number serialisation).
//
// This is the inline implementation rather than a third-party package so there is
// no extra dependency and the behaviour is transparent and auditable.
package canonicalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Canonicalize takes a JSON-serialisable value (struct, map, slice, or primitive)
// and returns the RFC 8785 canonical JSON bytes.
func Canonicalize(v interface{}) ([]byte, error) {
	// Marshal to generic representation first so we normalise struct tags, etc.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: initial marshal: %w", err)
	}

	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("canonicalize: unmarshal to generic: %w", err)
	}

	var buf bytes.Buffer
	if err := writeValue(&buf, generic); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalizeRaw takes already-decoded bytes and returns the canonical form.
func CanonicalizeRaw(raw []byte) ([]byte, error) {
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("canonicalize: unmarshal: %w", err)
	}
	var buf bytes.Buffer
	if err := writeValue(&buf, generic); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeValue(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")

	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}

	case float64:
		if err := writeNumber(buf, val); err != nil {
			return err
		}

	case string:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Errorf("canonicalize: marshal string: %w", err)
		}
		buf.Write(b)

	case []interface{}:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')

	case map[string]interface{}:
		// Sort keys by Unicode code point order (same as Go string comparison).
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return fmt.Errorf("canonicalize: marshal key: %w", err)
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeValue(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')

	default:
		return fmt.Errorf("canonicalize: unsupported type %T", v)
	}
	return nil
}

// writeNumber serialises a float64 using ES6-compatible rules (RFC 8785 § 3.2.2.3).
// Integers are written without a decimal point. Non-integer values use the shortest
// round-trip representation. ±Inf and NaN are illegal in JSON.
func writeNumber(buf *bytes.Buffer, f float64) error {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("canonicalize: illegal number value %v", f)
	}
	// Use Go's default float formatting which produces shortest round-trip.
	// For integers, avoid ".0" suffix by checking truncation equality.
	if f == math.Trunc(f) && !math.IsInf(f, 0) && f >= -1e15 && f <= 1e15 {
		buf.WriteString(strconv.FormatInt(int64(f), 10))
	} else {
		buf.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
	}
	return nil
}
