package sseframe

import (
	"io"
	"strings"
	"testing"
)

func TestParserSingleLineFrame(t *testing.T) {
	p := New(strings.NewReader("event: test\ndata: {\"ok\":true}\n\n"))

	frame, err := p.Next()
	if err != nil {
		t.Fatalf("Next() err=%v, want nil", err)
	}
	if frame.Event != "test" {
		t.Fatalf("frame.Event=%q, want %q", frame.Event, "test")
	}
	if string(frame.Data) != `{"ok":true}` {
		t.Fatalf("frame.Data=%q, want %q", string(frame.Data), `{"ok":true}`)
	}

	frame, err = p.Next()
	if err != io.EOF {
		t.Fatalf("second Next() err=%v, want io.EOF", err)
	}
	if frame.Event != "" || frame.Data != nil {
		t.Fatalf("second frame=%+v, want zero Frame", frame)
	}
}

func TestParserMultiLineDataJoinedWithNewline(t *testing.T) {
	p := New(strings.NewReader(strings.Join([]string{
		"event: test",
		`data: {"type":"x",`,
		`data: "value":1}`,
		"",
	}, "\n")))

	frame, err := p.Next()
	if err != nil {
		t.Fatalf("Next() err=%v, want nil", err)
	}
	if frame.Event != "test" {
		t.Fatalf("frame.Event=%q, want %q", frame.Event, "test")
	}
	want := "{\"type\":\"x\",\n\"value\":1}"
	if string(frame.Data) != want {
		t.Fatalf("frame.Data=%q, want %q", string(frame.Data), want)
	}
}

func TestParserEmitsFinalFrameWithoutTrailingNewline(t *testing.T) {
	p := New(strings.NewReader("event: final\ndata: {\"n\":1}"))

	frame, err := p.Next()
	if err != nil {
		t.Fatalf("Next() err=%v, want nil", err)
	}
	if frame.Event != "final" {
		t.Fatalf("frame.Event=%q, want %q", frame.Event, "final")
	}
	if string(frame.Data) != `{"n":1}` {
		t.Fatalf("frame.Data=%q, want %q", string(frame.Data), `{"n":1}`)
	}

	_, err = p.Next()
	if err != io.EOF {
		t.Fatalf("second Next() err=%v, want io.EOF", err)
	}
}

func TestParserIgnoresCommentsAndOptionalLeadingSpace(t *testing.T) {
	p := New(strings.NewReader(strings.Join([]string{
		": ping",
		"event:test",
		"id: 123",
		"retry: 1000",
		"data:alpha",
		"",
	}, "\n")))

	frame, err := p.Next()
	if err != nil {
		t.Fatalf("Next() err=%v, want nil", err)
	}
	if frame.Event != "test" {
		t.Fatalf("frame.Event=%q, want %q", frame.Event, "test")
	}
	if string(frame.Data) != "alpha" {
		t.Fatalf("frame.Data=%q, want %q", string(frame.Data), "alpha")
	}
}

func TestParserSkipsHeartbeatEmptyFrames(t *testing.T) {
	p := New(strings.NewReader("\n\ndata: ok\n\n"))

	frame, err := p.Next()
	if err != nil {
		t.Fatalf("Next() err=%v, want nil", err)
	}
	if frame.Event != "" {
		t.Fatalf("frame.Event=%q, want empty", frame.Event)
	}
	if string(frame.Data) != "ok" {
		t.Fatalf("frame.Data=%q, want %q", string(frame.Data), "ok")
	}
}
