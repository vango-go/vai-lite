package voice

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
)

type StreamingTTSOptions struct {
	// BufferAudio controls whether the streamer retains a copy of all audio bytes it forwards.
	// This is useful for SDK callers that want to append a final base64 audio block to the response.
	BufferAudio bool
}

// StreamingTTS turns streamed text deltas into a streaming TTS audio stream using SentenceBuffer chunking.
// It is intended for /v1/messages streaming voice output fan-out (SDK Stream and gateway SSE).
type StreamingTTS struct {
	ttsCtx *tts.StreamingContext
	buf    *SentenceBuffer
	opts   StreamingTTSOptions

	audioCh chan []byte
	doneCh  chan struct{}

	audioMu  sync.Mutex
	audioBuf bytes.Buffer

	errMu sync.Mutex
	err   error
}

func NewStreamingTTS(ttsCtx *tts.StreamingContext, opts StreamingTTSOptions) *StreamingTTS {
	s := &StreamingTTS{
		ttsCtx:   ttsCtx,
		buf:      NewSentenceBuffer(),
		opts:     opts,
		audioCh:  make(chan []byte, 100),
		doneCh:   make(chan struct{}),
		audioBuf: bytes.Buffer{},
	}
	go s.forwardAudio()
	return s
}

func (s *StreamingTTS) forwardAudio() {
	defer close(s.audioCh)
	defer close(s.doneCh)

	if s.ttsCtx == nil {
		s.setErr(fmt.Errorf("tts context is nil"))
		return
	}

	audio := s.ttsCtx.Audio()
	done := s.ttsCtx.Done()

	for {
		var chunk []byte
		var ok bool

		select {
		case chunk, ok = <-audio:
			if !ok {
				goto finished
			}
		case <-done:
			// Context closed; drain any buffered audio best-effort before stopping.
			for {
				select {
				case chunk, ok = <-audio:
					if !ok {
						goto finished
					}
					if len(chunk) == 0 {
						continue
					}
					if s.opts.BufferAudio {
						s.audioMu.Lock()
						s.audioBuf.Write(chunk)
						s.audioMu.Unlock()
					}
					s.audioCh <- chunk
				default:
					goto finished
				}
			}
		}

		if len(chunk) == 0 {
			continue
		}
		if s.opts.BufferAudio {
			s.audioMu.Lock()
			s.audioBuf.Write(chunk)
			s.audioMu.Unlock()
		}
		s.audioCh <- chunk
	}

finished:
	if err := s.ttsCtx.Err(); err != nil {
		s.setErr(err)
	}
}

// OnTextDelta consumes incremental text output and sends completed sentences to the TTS context.
func (s *StreamingTTS) OnTextDelta(text string) error {
	if s == nil || s.ttsCtx == nil {
		return fmt.Errorf("streaming tts is not initialized")
	}
	if err := s.Err(); err != nil {
		return err
	}
	for _, sentence := range s.buf.Add(text) {
		if err := s.ttsCtx.SendText(sentence, false); err != nil {
			s.setErr(err)
			_ = s.ttsCtx.Close()
			return err
		}
	}
	return nil
}

// Flush sends any remaining buffered text and signals completion to the provider.
func (s *StreamingTTS) Flush() error {
	if s == nil || s.ttsCtx == nil {
		return fmt.Errorf("streaming tts is not initialized")
	}
	if err := s.Err(); err != nil {
		return err
	}

	remaining := strings.TrimSpace(s.buf.Flush())
	if remaining != "" {
		if err := s.ttsCtx.SendText(remaining, true); err != nil {
			s.setErr(err)
			return err
		}
		return nil
	}

	if err := s.ttsCtx.Flush(); err != nil {
		s.setErr(err)
		return err
	}
	return nil
}

// Close closes the underlying TTS context and waits for audio forwarding to complete.
func (s *StreamingTTS) Close() error {
	if s == nil || s.ttsCtx == nil {
		return nil
	}
	_ = s.ttsCtx.Close()
	<-s.doneCh
	return s.Err()
}

func (s *StreamingTTS) Audio() <-chan []byte {
	if s == nil {
		ch := make(chan []byte)
		close(ch)
		return ch
	}
	return s.audioCh
}

func (s *StreamingTTS) AudioBytes() []byte {
	if s == nil || !s.opts.BufferAudio {
		return nil
	}
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	out := make([]byte, s.audioBuf.Len())
	copy(out, s.audioBuf.Bytes())
	return out
}

func (s *StreamingTTS) Err() error {
	if s == nil {
		return nil
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *StreamingTTS) setErr(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
}
