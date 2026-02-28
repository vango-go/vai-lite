package session

import "testing"

func TestTalkTextExtractor_SimpleSplit(t *testing.T) {
	var e TalkTextExtractor
	out, err := e.Feed(`{"text":"hel`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "hel" {
		t.Fatalf("out=%q, want %q", out, "hel")
	}
	out, err = e.Feed(`lo"}`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "lo" {
		t.Fatalf("out=%q, want %q", out, "lo")
	}
	if e.FullText() != "hello" {
		t.Fatalf("FullText=%q, want %q", e.FullText(), "hello")
	}
}

func TestTalkTextExtractor_SplitAcrossEscape(t *testing.T) {
	var e TalkTextExtractor
	out, err := e.Feed(`{"text":"a\`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "a" {
		t.Fatalf("out=%q, want %q", out, "a")
	}
	out, err = e.Feed(`n"}`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "\n" {
		t.Fatalf("out=%q, want newline", out)
	}
	if e.FullText() != "a\n" {
		t.Fatalf("FullText=%q, want %q", e.FullText(), "a\n")
	}
}

func TestTalkTextExtractor_SplitAcrossUnicodeSurrogate(t *testing.T) {
	var e TalkTextExtractor
	out, err := e.Feed(`{"text":"\uD83D`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "" {
		t.Fatalf("out=%q, want empty (incomplete unicode)", out)
	}
	out, err = e.Feed(`\uDE00"}`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "😀" {
		t.Fatalf("out=%q, want %q", out, "😀")
	}
	if e.FullText() != "😀" {
		t.Fatalf("FullText=%q, want %q", e.FullText(), "😀")
	}
}

func TestTalkTextExtractor_ExtraFieldsBeforeAfter(t *testing.T) {
	var e TalkTextExtractor
	out, err := e.Feed(`{"other":123,"text":"hi","more":"x"}`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "hi" {
		t.Fatalf("out=%q, want %q", out, "hi")
	}
	if e.FullText() != "hi" {
		t.Fatalf("FullText=%q, want %q", e.FullText(), "hi")
	}
}

func TestTalkTextExtractor_NoTextField(t *testing.T) {
	var e TalkTextExtractor
	out, err := e.Feed(`{"foo":"bar"}`)
	if err != nil {
		t.Fatalf("Feed err=%v", err)
	}
	if out != "" {
		t.Fatalf("out=%q, want empty", out)
	}
	if e.FullText() != "" {
		t.Fatalf("FullText=%q, want empty", e.FullText())
	}
}
