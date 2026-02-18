package vai

import "testing"

func TestAudioChunkFrom(t *testing.T) {
	event := AudioChunkEvent{Data: []byte("chunk"), Format: "wav"}
	chunk, ok := AudioChunkFrom(event)
	if !ok {
		t.Fatalf("expected audio chunk event")
	}
	if string(chunk.Data) != "chunk" || chunk.Format != "wav" {
		t.Fatalf("unexpected chunk payload: %#v", chunk)
	}
}

func TestRunStreamProcess_OnAudioChunkCallback(t *testing.T) {
	events := make(chan RunStreamEvent, 2)
	events <- AudioChunkEvent{Data: []byte("a1"), Format: "wav"}
	events <- AudioChunkEvent{Data: []byte("a2"), Format: "wav"}
	close(events)

	done := make(chan struct{})
	close(done)

	rs := &RunStream{
		events: events,
		done:   done,
	}

	var got int
	_, err := rs.Process(StreamCallbacks{
		OnAudioChunk: func(data []byte, format string) {
			got++
			if format != "wav" {
				t.Fatalf("format = %q, want wav", format)
			}
		},
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("OnAudioChunk calls = %d, want 2", got)
	}
}
