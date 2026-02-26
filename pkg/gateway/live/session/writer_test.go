package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type recordedWrite struct {
	messageType int
	data        string
}

type fakeWSWriter struct {
	mu     sync.Mutex
	writes []recordedWrite
}

func (f *fakeWSWriter) SetWriteDeadline(time.Time) error { return nil }

func (f *fakeWSWriter) WriteMessage(messageType int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, recordedWrite{messageType: messageType, data: string(data)})
	return nil
}

func (f *fakeWSWriter) WriteControl(messageType int, data []byte, deadline time.Time) error {
	_ = deadline
	return f.WriteMessage(messageType, data)
}

func (f *fakeWSWriter) Close() error { return nil }

func (f *fakeWSWriter) snapshot() []recordedWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedWrite, len(f.writes))
	copy(out, f.writes)
	return out
}

func TestOutboundWriter_PriorityBeatsNormal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 1)

	normal <- outboundFrame{
		isAssistantAudio: true,
		assistantAudioID: "a_1",
		textPayload:      []byte(`{"type":"assistant_audio_chunk","assistant_audio_id":"a_1","seq":1,"audio_b64":"AAAA"}`),
	}
	priority <- outboundFrame{
		textPayload: []byte(`{"type":"audio_reset","reason":"barge_in","assistant_audio_id":"a_1"}`),
	}
	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
	}

	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	writes := ws.snapshot()
	if len(writes) == 0 {
		t.Fatalf("expected at least one write")
	}
	if !strings.Contains(writes[0].data, `"type":"audio_reset"`) {
		t.Fatalf("first write was not audio_reset: %q", writes[0].data)
	}
}

func TestOutboundWriter_CanceledAssistantAudioDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 8)

	normal <- outboundFrame{isAssistantAudio: true, assistantAudioID: "a_1", textPayload: []byte(`{"type":"assistant_audio_start","assistant_audio_id":"a_1"}`)}
	normal <- outboundFrame{isAssistantAudio: true, assistantAudioID: "a_1", textPayload: []byte(`{"type":"assistant_audio_chunk","assistant_audio_id":"a_1","seq":1}`)}
	normal <- outboundFrame{isAssistantAudio: true, assistantAudioID: "a_1", textPayload: []byte(`{"type":"assistant_audio_end","assistant_audio_id":"a_1"}`)}

	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
		isCanceled: func(id string) bool {
			return id == "a_1"
		},
	}

	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if writes := ws.snapshot(); len(writes) != 0 {
		t.Fatalf("expected zero writes, got %d: %+v", len(writes), writes)
	}
}

func TestOutboundWriter_NonAudioUnaffectedByCancelSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 8)

	normal <- outboundFrame{textPayload: []byte(`{"type":"warning","code":"x","message":"y"}`)}
	normal <- outboundFrame{textPayload: []byte(`{"type":"transcript_delta","utterance_id":"u_1","text":"hello"}`)}

	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
		isCanceled: func(string) bool {
			return true
		},
	}

	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	writes := ws.snapshot()
	if len(writes) != 2 {
		t.Fatalf("expected 2 writes, got %d: %+v", len(writes), writes)
	}
}

func TestOutboundWriter_BinaryPairDroppedWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 1)

	normal <- outboundFrame{
		isAssistantAudio: true,
		assistantAudioID: "a_1",
		binaryPair: &binaryPair{
			header: []byte(`{"type":"assistant_audio_chunk_header","assistant_audio_id":"a_1","seq":1,"bytes":2}`),
			data:   []byte{0x01, 0x02},
		},
	}

	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
		isCanceled: func(id string) bool {
			return id == "a_1"
		},
	}

	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if writes := ws.snapshot(); len(writes) != 0 {
		t.Fatalf("expected zero writes, got %d: %+v", len(writes), writes)
	}
}

func TestOutboundWriter_BinaryPairWrittenInOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 1)

	normal <- outboundFrame{
		isAssistantAudio: true,
		assistantAudioID: "a_1",
		binaryPair: &binaryPair{
			header: []byte(`{"type":"assistant_audio_chunk_header","assistant_audio_id":"a_1","seq":1,"bytes":2}`),
			data:   []byte{0x01, 0x02},
		},
	}

	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
	}

	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	writes := ws.snapshot()
	if len(writes) != 2 {
		t.Fatalf("expected 2 writes, got %d: %+v", len(writes), writes)
	}
	if writes[0].messageType != websocket.TextMessage {
		t.Fatalf("first write type=%d, want TextMessage", writes[0].messageType)
	}
	if writes[1].messageType != websocket.BinaryMessage {
		t.Fatalf("second write type=%d, want BinaryMessage", writes[1].messageType)
	}
}

func TestOutboundWriter_FlushesPriorityOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	priority := make(chan outboundFrame, 1)
	normal := make(chan outboundFrame, 1)

	priority <- outboundFrame{textPayload: []byte(`{"type":"audio_reset","reason":"backpressure","assistant_audio_id":"a_1"}`)}
	close(priority)
	close(normal)

	ws := &fakeWSWriter{}
	w := outboundWriter{
		ws:       ws,
		ctx:      ctx,
		cfg:      Config{PingInterval: time.Hour, WriteTimeout: time.Second},
		priority: priority,
		normal:   normal,
	}

	cancel()
	if err := w.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	writes := ws.snapshot()
	if len(writes) == 0 || !strings.Contains(writes[0].data, `"type":"audio_reset"`) {
		t.Fatalf("expected audio_reset to flush on shutdown, writes=%+v", writes)
	}
}
