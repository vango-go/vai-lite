package voice

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core"
)

func TestResolveSTTModel_DefaultAndExplicit(t *testing.T) {
	got, err := ResolveSTTModel("")
	if err != nil {
		t.Fatalf("ResolveSTTModel default err=%v", err)
	}
	if got.Raw != DefaultSTTModel || got.Provider != "cartesia" || got.Model != "ink-whisper" {
		t.Fatalf("unexpected default resolution: %+v", got)
	}

	got, err = ResolveSTTModel("cartesia/custom-stt")
	if err != nil {
		t.Fatalf("ResolveSTTModel explicit err=%v", err)
	}
	if got.Provider != "cartesia" || got.Model != "custom-stt" {
		t.Fatalf("unexpected explicit resolution: %+v", got)
	}
}

func TestResolveTTSModel_RejectsUnsupportedProvider(t *testing.T) {
	_, err := ResolveTTSModel("elevenlabs/flash-v2.5")
	if err == nil {
		t.Fatalf("expected error")
	}
	coreErr, ok := err.(*core.Error)
	if !ok {
		t.Fatalf("error type=%T, want *core.Error", err)
	}
	if coreErr.Param != "tts_model" {
		t.Fatalf("param=%q, want tts_model", coreErr.Param)
	}
}
