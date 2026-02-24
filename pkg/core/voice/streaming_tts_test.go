package voice

import (
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
)

func TestStreamingTTS_SentenceBufferingAndFlush(t *testing.T) {
	sc := tts.NewStreamingContext()

	var sent []struct {
		text    string
		isFinal bool
	}

	sc.SendFunc = func(text string, isFinal bool) error {
		sent = append(sent, struct {
			text    string
			isFinal bool
		}{text: text, isFinal: isFinal})

		if text != "" {
			_ = sc.PushAudio([]byte(text))
		}
		if isFinal {
			sc.FinishAudio()
		}
		return nil
	}
	sc.CloseFunc = func() error { return nil }

	stream := NewStreamingTTS(sc, StreamingTTSOptions{BufferAudio: true})

	if err := stream.OnTextDelta("Hello "); err != nil {
		t.Fatalf("OnTextDelta err=%v", err)
	}
	if err := stream.OnTextDelta("world."); err != nil {
		t.Fatalf("OnTextDelta err=%v", err)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush err=%v", err)
	}
	_ = stream.Close()

	// Ensure we sent at least one non-final chunk (sentence).
	if len(sent) == 0 {
		t.Fatalf("expected SendText calls")
	}
	if sent[0].text != "Hello world." || sent[0].isFinal {
		t.Fatalf("unexpected first send: %#v", sent[0])
	}

	// Audio bytes should include the pushed sentence.
	if got := string(stream.AudioBytes()); got != "Hello world." {
		t.Fatalf("audioBytes=%q", got)
	}
}

func TestStreamingTTS_CloseWaitsForDone(t *testing.T) {
	sc := tts.NewStreamingContext()
	sc.SendFunc = func(text string, isFinal bool) error {
		if isFinal {
			sc.FinishAudio()
		}
		return nil
	}
	sc.CloseFunc = func() error { return nil }

	stream := NewStreamingTTS(sc, StreamingTTSOptions{})
	_ = stream.Flush()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = stream.Close()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("close did not return")
	}
}
