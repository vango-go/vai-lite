package session

import "github.com/vango-go/vai-lite/pkg/core/types"

type historyManager struct {
	canonical []types.Message
	played    []types.Message
}

func newHistoryManager() *historyManager {
	return &historyManager{
		canonical: make([]types.Message, 0, 16),
		played:    make([]types.Message, 0, 16),
	}
}

func (h *historyManager) appendUser(text string) (canonicalIdx int, playedIdx int) {
	msg := types.Message{Role: "user", Content: text}
	h.canonical = append(h.canonical, msg)
	h.played = append(h.played, msg)
	return len(h.canonical) - 1, len(h.played) - 1
}

func (h *historyManager) appendAssistantCanonical(text string) int {
	h.canonical = append(h.canonical, types.Message{Role: "assistant", Content: text})
	return len(h.canonical) - 1
}

func (h *historyManager) appendAssistantPlayed(text string) int {
	h.played = append(h.played, types.Message{Role: "assistant", Content: text})
	return len(h.played) - 1
}

func (h *historyManager) replaceCanonicalUser(idx int, text string) {
	if idx >= 0 && idx < len(h.canonical) && h.canonical[idx].Role == "user" {
		h.canonical[idx] = types.Message{Role: "user", Content: text}
		return
	}
	for i := len(h.canonical) - 1; i >= 0; i-- {
		if h.canonical[i].Role == "user" {
			h.canonical[i] = types.Message{Role: "user", Content: text}
			return
		}
	}
}

func (h *historyManager) replacePlayedUser(idx int, text string) {
	if idx >= 0 && idx < len(h.played) && h.played[idx].Role == "user" {
		h.played[idx] = types.Message{Role: "user", Content: text}
		return
	}
	for i := len(h.played) - 1; i >= 0; i-- {
		if h.played[i].Role == "user" {
			h.played[i] = types.Message{Role: "user", Content: text}
			return
		}
	}
}

func (h *historyManager) playedSnapshot() []types.Message {
	out := make([]types.Message, len(h.played))
	copy(out, h.played)
	return out
}

func (h *historyManager) canonicalSnapshot() []types.Message {
	out := make([]types.Message, len(h.canonical))
	copy(out, h.canonical)
	return out
}
