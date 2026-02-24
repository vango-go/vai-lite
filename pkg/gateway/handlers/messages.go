package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
	"github.com/vango-go/vai-lite/pkg/gateway/apierror"
	"github.com/vango-go/vai-lite/pkg/gateway/auth"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/limits"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
	"github.com/vango-go/vai-lite/pkg/gateway/sse"
)

type ProviderFactory interface {
	New(providerName, apiKey string) (core.Provider, error)
}

type MessagesHandler struct {
	Config     config.Config
	Upstreams  ProviderFactory
	HTTPClient *http.Client
	Logger     *slog.Logger
	Limiter    *ratelimit.Limiter
}

func (h MessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqID, _ := mw.RequestIDFrom(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, h.Config.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeErrorJSON(w, reqID, core.NewInvalidRequestError("failed to read request body"), http.StatusBadRequest)
		return
	}

	req, err := types.UnmarshalMessageRequestStrict(body)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if err := limits.ValidateMessageRequest(req, h.Config); err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if len(h.Config.ModelAllowlist) > 0 {
		if _, ok := h.Config.ModelAllowlist[req.Model]; !ok {
			h.writeErrorJSON(w, reqID, &core.Error{
				Type:      core.ErrPermission,
				Message:   "model is not allowlisted",
				Param:     "model",
				RequestID: reqID,
			}, http.StatusForbidden)
			return
		}
	}

	providerName, modelName, err := core.ParseModelString(req.Model)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	upstreamKeyHeader, ok := providerKeyHeader(providerName)
	if !ok {
		h.writeErrorJSON(w, reqID, core.NewInvalidRequestErrorWithParam("unsupported provider", "model"), http.StatusBadRequest)
		return
	}
	upstreamKey := strings.TrimSpace(r.Header.Get(upstreamKeyHeader))
	if upstreamKey == "" {
		h.writeErrorJSON(w, reqID, &core.Error{
			Type:      core.ErrAuthentication,
			Message:   "missing upstream provider api key header",
			Param:     upstreamKeyHeader,
			RequestID: reqID,
		}, http.StatusUnauthorized)
		return
	}

	provider, err := h.Upstreams.New(providerName, upstreamKey)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	workingReq := *req
	workingReq.Model = modelName

	// Request-scoped timeout. Streaming requests use the SSE max duration.
	ctx := r.Context()
	if req.Stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Config.SSEMaxStreamDuration)
		defer cancel()
	} else if h.Config.HandlerTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Config.HandlerTimeout)
		defer cancel()
	}

	var voicePipeline *voice.Pipeline
	var userTranscript string
	if req.Voice != nil && (req.Voice.Input != nil || req.Voice.Output != nil) {
		cartesiaKey := strings.TrimSpace(r.Header.Get("X-Provider-Key-Cartesia"))
		if cartesiaKey == "" {
			h.writeErrorJSON(w, reqID, &core.Error{
				Type:      core.ErrAuthentication,
				Message:   "missing voice provider api key header",
				Param:     "X-Provider-Key-Cartesia",
				RequestID: reqID,
			}, http.StatusUnauthorized)
			return
		}

		voiceHTTPClient := h.HTTPClient
		if voiceHTTPClient == nil {
			voiceHTTPClient = &http.Client{}
		}
		voicePipeline = voice.NewPipelineWithProviders(
			stt.NewCartesiaWithClient(cartesiaKey, voiceHTTPClient),
			tts.NewCartesiaWithClient(cartesiaKey, voiceHTTPClient),
		)

		if req.Voice.Input != nil {
			processedReq, transcript, err := voice.PreprocessMessageRequestInputAudio(ctx, voicePipeline, &workingReq)
			if err != nil {
				h.writeErr(w, reqID, err, false)
				return
			}
			workingReq = *processedReq
			userTranscript = transcript
		}
	}

	if req.Stream {
		principalKey := "anonymous"
		if p, ok := auth.PrincipalFrom(r.Context()); ok {
			principalKey = ratelimit.PrincipalKeyFromAPIKey(p.APIKey)
		}

		if h.Limiter != nil && h.Config.LimitMaxConcurrentStreams > 0 {
			dec := h.Limiter.AcquireStream(principalKey, time.Now())
			if !dec.Allowed {
				if dec.RetryAfter > 0 {
					w.Header().Set("Retry-After", itoa(dec.RetryAfter))
				}
				h.writeErrorJSON(w, reqID, core.NewRateLimitError("too many concurrent streams", dec.RetryAfter), http.StatusTooManyRequests)
				return
			}
			if dec.Permit != nil {
				defer dec.Permit.Release()
			}
		}

		h.serveStream(w, r, ctx, reqID, provider, &workingReq, voicePipeline, userTranscript)
		return
	}

	resp, err := provider.CreateMessage(ctx, &workingReq)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if userTranscript != "" {
		if resp.Metadata == nil {
			resp.Metadata = make(map[string]any)
		}
		resp.Metadata["user_transcript"] = userTranscript
	}

	if voicePipeline != nil && req.Voice != nil && req.Voice.Output != nil {
		if err := voice.AppendVoiceOutputToMessageResponse(ctx, voicePipeline, req.Voice, resp); err != nil {
			h.writeErr(w, reqID, err, false)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Model", resp.Model)
	w.Header().Set("X-Input-Tokens", itoa(resp.Usage.InputTokens))
	w.Header().Set("X-Output-Tokens", itoa(resp.Usage.OutputTokens))
	w.Header().Set("X-Total-Tokens", itoa(resp.Usage.TotalTokens))
	_ = json.NewEncoder(w).Encode(resp)
}

func (h MessagesHandler) serveStream(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	reqID string,
	provider core.Provider,
	req *types.MessageRequest,
	voicePipeline *voice.Pipeline,
	userTranscript string,
) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sw, err := sse.New(w)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	// Keepalive pings when the upstream is quiet.
	// Track last *non-ping* activity so pings don't suppress themselves.
	var lastNonPingActivity atomic.Int64
	lastNonPingActivity.Store(time.Now().UnixNano())
	send := func(event string, data any) error {
		if err := sw.Send(event, data); err != nil {
			return err
		}
		lastNonPingActivity.Store(time.Now().UnixNano())
		return nil
	}

	pingInterval := h.Config.SSEPingInterval
	if pingInterval > 0 {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case t := <-ticker.C:
					last := time.Unix(0, lastNonPingActivity.Load())
					if t.Sub(last) < pingInterval {
						continue
					}
					_ = sw.Send("ping", types.PingEvent{Type: "ping"})
				}
			}
		}()
	}

	reqCopy := *req
	reqCopy.Stream = true
	stream, err := provider.StreamMessage(ctx, &reqCopy)
	if err != nil {
		h.writeErr(w, reqID, err, true)
		return
	}
	defer func() { _ = stream.Close() }()

	// Voice output side-channel: feed text deltas into streaming TTS and emit audio_chunk SSE events.
	var ttsCtx *tts.StreamingContext
	var ttsStream *voice.StreamingTTS
	audioDone := make(chan struct{})
	sampleRateHz := 24000
	if req.Voice != nil && req.Voice.Output != nil && req.Voice.Output.SampleRate > 0 {
		sampleRateHz = req.Voice.Output.SampleRate
	}

	if voicePipeline != nil && req.Voice != nil && req.Voice.Output != nil {
		var err error
		ttsCtx, err = voicePipeline.NewStreamingTTSContext(ctx, req.Voice)
		if err != nil {
			h.writeErr(w, reqID, err, true)
			return
		}
		ttsStream = voice.NewStreamingTTS(ttsCtx, voice.StreamingTTSOptions{BufferAudio: false})

		go func() {
			defer close(audioDone)
			for chunk := range ttsStream.Audio() {
				ev := types.AudioChunkEvent{
					Type:         "audio_chunk",
					Format:       "pcm",
					Audio:        base64.StdEncoding.EncodeToString(chunk),
					SampleRateHz: sampleRateHz,
				}
				_ = send(ev.EventType(), ev)
			}
		}()
	} else {
		close(audioDone)
	}

	type nextResult struct {
		ev  types.StreamEvent
		err error
	}
	nextCh := make(chan nextResult, 1)
	go func() {
		defer close(nextCh)
		for {
			ev, err := stream.Next()
			select {
			case nextCh <- nextResult{ev: ev, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = stream.Close()
			if ttsCtx != nil {
				_ = ttsCtx.Close()
			}
			// If this was a max-duration timeout (not client disconnect), emit a terminal SSE error event.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
				h.writeErr(w, reqID, ctx.Err(), true)
			}
			return

		case res, ok := <-nextCh:
			if !ok {
				goto done
			}
			if res.ev != nil {
				// Feed text deltas into TTS.
				if ttsStream != nil {
					if cbd, ok := res.ev.(types.ContentBlockDeltaEvent); ok {
						if td, ok := cbd.Delta.(types.TextDelta); ok {
							if sendErr := ttsStream.OnTextDelta(td.Text); sendErr != nil {
								_ = ttsStream.Close()
								h.writeErr(w, reqID, sendErr, true)
								return
							}
						}
					}
				}

				if sendErr := send(res.ev.EventType(), res.ev); sendErr != nil {
					_ = stream.Close()
					if ttsCtx != nil {
						_ = ttsCtx.Close()
					}
					return
				}
			}

			if res.err != nil {
				if errors.Is(res.err, io.EOF) {
					goto done
				}
				h.writeErr(w, reqID, res.err, true)
				if ttsCtx != nil {
					_ = ttsCtx.Close()
				}
				return
			}
		}
	}

done:

	if ttsStream != nil {
		_ = ttsStream.Flush()
		_ = ttsStream.Close()
		<-audioDone
		if err := ttsStream.Err(); err != nil {
			h.writeErr(w, reqID, err, true)
			return
		}
		// Emit a final marker.
		_ = send("audio_chunk", types.AudioChunkEvent{
			Type:         "audio_chunk",
			Format:       "pcm",
			Audio:        "",
			SampleRateHz: sampleRateHz,
			IsFinal:      true,
		})
	}
}

func providerKeyHeader(provider string) (string, bool) {
	switch provider {
	case "anthropic":
		return "X-Provider-Key-Anthropic", true
	case "openai":
		return "X-Provider-Key-OpenAI", true
	case "oai-resp":
		return "X-Provider-Key-OpenAI", true
	case "gemini":
		return "X-Provider-Key-Gemini", true
	case "groq":
		return "X-Provider-Key-Groq", true
	case "cerebras":
		return "X-Provider-Key-Cerebras", true
	case "openrouter":
		return "X-Provider-Key-OpenRouter", true
	default:
		return "", false
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func (h MessagesHandler) writeErr(w http.ResponseWriter, reqID string, err error, isStream bool) {
	coreErr, status := apierror.FromError(err, reqID)

	if isStream {
		// If headers already started, best-effort send SSE error event.
		sw, sseErr := sse.New(w)
		if sseErr == nil {
			_ = sw.Send("error", types.ErrorEvent{
				Type: "error",
				Error: types.Error{
					Type:          string(coreErr.Type),
					Message:       coreErr.Message,
					Param:         coreErr.Param,
					Code:          coreErr.Code,
					RequestID:     coreErr.RequestID,
					ProviderError: coreErr.ProviderError,
					RetryAfter:    coreErr.RetryAfter,
				},
			})
		}
		_ = status
		return
	}

	h.writeErrorJSON(w, reqID, coreErr, status)
}

func (h MessagesHandler) writeErrorJSON(w http.ResponseWriter, reqID string, coreErr *core.Error, status int) {
	if coreErr != nil && coreErr.RequestID == "" {
		coreErr.RequestID = reqID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apierror.Envelope{Error: coreErr})
}
