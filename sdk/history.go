package vai

import (
	"fmt"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// DefaultHistoryHandler returns a permissive handler that appends HistoryDeltaEvent messages
// to the provided message slice.
//
// This handler does not enforce ExpectedLen; use DefaultHistoryHandlerStrict if you want
// mismatch detection.
func DefaultHistoryHandler(messages *[]types.Message) func(RunStreamEvent) {
	return func(event RunStreamEvent) {
		if messages == nil {
			return
		}
		switch e := event.(type) {
		case HistoryDeltaEvent:
			if len(e.Append) == 0 {
				return
			}
			*messages = append(*messages, e.Append...)
		}
	}
}

// DefaultHistoryHandlerStrict returns a handler that appends HistoryDeltaEvent messages
// and validates ExpectedLen matches the current history length.
func DefaultHistoryHandlerStrict(messages *[]types.Message) func(RunStreamEvent) {
	return func(event RunStreamEvent) {
		if messages == nil {
			return
		}
		switch e := event.(type) {
		case HistoryDeltaEvent:
			if len(e.Append) == 0 {
				return
			}
			if len(*messages) != e.ExpectedLen {
				panic(fmt.Sprintf("history length mismatch: got %d, expected %d", len(*messages), e.ExpectedLen))
			}
			*messages = append(*messages, e.Append...)
		}
	}
}
