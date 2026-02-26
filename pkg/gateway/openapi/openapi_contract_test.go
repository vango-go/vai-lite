package openapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPI_SSEUnknownEventFallbacks_AreForwardCompatible(t *testing.T) {
	specPath := mustFindOpenAPIPath(t)
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	spec := string(raw)

	messageStreamEvent := mustExtractSection(t, spec, "\n    MessageStreamEvent:\n", "\n\n    StreamEvent:\n")
	if !strings.Contains(messageStreamEvent, `- $ref: "#/components/schemas/UnknownMessageStreamEvent"`) {
		t.Fatalf("MessageStreamEvent missing UnknownMessageStreamEvent fallback")
	}

	runStreamEvent := mustExtractSection(t, spec, "\n    RunStreamEvent:\n", "\n\n    RunStartEvent:\n")
	if !strings.Contains(runStreamEvent, `- $ref: "#/components/schemas/UnknownRunStreamEvent"`) {
		t.Fatalf("RunStreamEvent missing UnknownRunStreamEvent fallback")
	}

	unknownMessage := mustExtractSection(t, spec, "\n    UnknownMessageStreamEvent:\n", "\n\n    ContentBlockStopEvent:\n")
	for _, typ := range []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
		"ping",
		"audio_chunk",
		"audio_unavailable",
		"error",
	} {
		if !strings.Contains(unknownMessage, "\n              - "+typ+"\n") {
			t.Fatalf("UnknownMessageStreamEvent.not exclusion list missing %q", typ)
		}
	}

	unknownRun := mustExtractSection(t, spec, "\n    UnknownRunStreamEvent:\n", "\n\n    # --- Models ---\n")
	for _, typ := range []string{
		"run_start",
		"step_start",
		"stream_event",
		"tool_call_start",
		"tool_result",
		"step_complete",
		"history_delta",
		"run_complete",
		"ping",
		"error",
	} {
		if !strings.Contains(unknownRun, "\n              - "+typ+"\n") {
			t.Fatalf("UnknownRunStreamEvent.not exclusion list missing %q", typ)
		}
	}
}

func mustFindOpenAPIPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for i := 0; i < 20; i++ {
		candidate := filepath.Join(dir, "api", "openapi.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate api/openapi.yaml from cwd")
	return ""
}

func mustExtractSection(t *testing.T, spec, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(spec, startMarker)
	if start < 0 {
		t.Fatalf("missing start marker %q", startMarker)
	}
	start += len(startMarker) - 1

	rest := spec[start:]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		t.Fatalf("missing end marker %q (start=%q)", endMarker, startMarker)
	}
	return rest[:end]
}
