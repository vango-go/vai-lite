package vai

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestDefaultHistoryHandler_AppendsOnDelta(t *testing.T) {
	var history []types.Message
	apply := DefaultHistoryHandler(&history)

	apply(HistoryDeltaEvent{
		ExpectedLen: 0,
		Append:      []types.Message{{Role: "user", Content: []types.ContentBlock{Text("hi")}}},
	})

	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("history[0].Role = %q, want %q", history[0].Role, "user")
	}
}

func TestDefaultHistoryHandler_IgnoresOtherEvents(t *testing.T) {
	var history []types.Message
	apply := DefaultHistoryHandler(&history)

	apply(StepStartEvent{Index: 0})
	apply(RunCompleteEvent{Result: nil})

	if len(history) != 0 {
		t.Fatalf("len(history) = %d, want 0", len(history))
	}
}

func TestDefaultHistoryHandlerStrict_PanicsOnMismatch(t *testing.T) {
	var history []types.Message
	apply := DefaultHistoryHandlerStrict(&history)

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic, got nil")
		}
	}()

	history = append(history, types.Message{Role: "user", Content: []types.ContentBlock{Text("seed")}})
	apply(HistoryDeltaEvent{
		ExpectedLen: 0,
		Append:      []types.Message{{Role: "assistant", Content: []types.ContentBlock{Text("nope")}}},
	})
}

