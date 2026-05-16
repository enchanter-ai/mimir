package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSpanEmitsStructuredLog(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	tr := NewTracer(logger)

	_, span := tr.StartSpan(context.Background(), "test_span", slog.String("k", "v"))
	span.SetAttr("count", 42)
	span.End()

	out := buf.String()
	if !strings.Contains(out, `"span":"test_span"`) {
		t.Errorf("span name not in output: %q", out)
	}
	if !strings.Contains(out, `"duration"`) {
		t.Errorf("duration not in output: %q", out)
	}
	if !strings.Contains(out, `"k":"v"`) {
		t.Errorf("attr not in output: %q", out)
	}
	if !strings.Contains(out, `"count":42`) {
		t.Errorf("late attr not in output: %q", out)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Errorf("output is not valid JSON: %v\n%s", err, out)
	}
}

func TestSpanWithErrorEmitsAtErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	tr := NewTracer(slog.New(handler))

	_, span := tr.StartSpan(context.Background(), "failing_span")
	span.SetError(errors.New("kms unreachable"))
	span.End()

	out := buf.String()
	if !strings.Contains(out, `"level":"ERROR"`) {
		t.Errorf("expected ERROR level when span has error, got: %q", out)
	}
	if !strings.Contains(out, "kms unreachable") {
		t.Errorf("error message not surfaced: %q", out)
	}
}

func TestEndIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTracer(slog.New(slog.NewJSONHandler(&buf, nil)))
	_, span := tr.StartSpan(context.Background(), "x")
	span.End()
	first := buf.Len()
	span.End() // second End() must not double-emit
	if buf.Len() != first {
		t.Errorf("double End emitted extra output (was %d, now %d)", first, buf.Len())
	}
}

func TestNoOpTracer(t *testing.T) {
	tr := NoOpTracer{}
	_, span := tr.StartSpan(context.Background(), "anything")
	// All methods must be safe.
	span.SetAttr("k", 1)
	span.SetError(errors.New("ignored"))
	span.End()
	span.End() // idempotent here too
}

func TestSetupRespectsLogLevel(t *testing.T) {
	t.Setenv("ISSUER_LOG_LEVEL", "warn")
	logger := Setup()
	if logger == nil {
		t.Fatal("Setup returned nil")
	}
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info level should be filtered out under WARN setting")
	}
	if !logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Warn level must remain enabled")
	}
}
