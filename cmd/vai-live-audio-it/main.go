package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	coretypes "github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

const (
	liveProtocolVersion = "1"

	audioInSampleRateHz  = 16000
	audioOutSampleRateHz = 24000

	audioEncodingPCM16LE = "pcm_s16le"
	audioChannelsMono    = 1
)

type options struct {
	gateway          string
	gatewayAPIKey    string
	model            string
	lang             string
	transport        string
	micDevice        int
	micFrameMS       int
	markIntervalMS   int
	printRunEvents   bool
	elevenVoiceID    string
	listElevenVoices bool
	listMicDevices   bool
	noSpeaker        bool
	ffplayPath       string
	debug            bool

	clientEndpointing  bool
	endpointSilenceMS  int
	speechThresholdAbs int
	clientHasAEC       bool
	speakerVolume      int
	dumpAssistantPCM   string
	speakerTestToneMS  int
	playedMSMode       string
	playedLagMS        int

	micCmdOverride string
}

func main() {
	os.Exit(runMain())
}

func runMain() int {
	loadEnvFileBestEffort()
	normalizeEnvKeys()

	var opt options
	flag.StringVar(&opt.gateway, "gateway", "", "Gateway base URL (http(s)://host:port or ws(s)://...); required")
	flag.StringVar(&opt.gatewayAPIKey, "gateway-api-key", strings.TrimSpace(os.Getenv("VAI_GATEWAY_API_KEY")), "Gateway API key (optional; also reads VAI_GATEWAY_API_KEY)")
	flag.StringVar(&opt.model, "model", "", "Model provider/model (optional; defaults based on available API keys)")
	flag.StringVar(&opt.elevenVoiceID, "elevenlabs-voice-id", strings.TrimSpace(os.Getenv("ELEVENLABS_VOICE_ID")), "ElevenLabs voice ID (required unless ELEVENLABS_VOICE_ID is set)")
	flag.StringVar(&opt.lang, "lang", "en", "Voice language (default: en)")
	flag.StringVar(&opt.transport, "transport", "binary", "Audio transport: binary or base64_json (default: binary)")
	flag.IntVar(&opt.micDevice, "mic-device", 0, "macOS avfoundation mic device index (default: 0)")
	flag.IntVar(&opt.micFrameMS, "mic-frame-ms", 20, "Mic frame duration in ms (default: 20)")
	flag.IntVar(&opt.markIntervalMS, "mark-interval-ms", 200, "Playback mark interval in ms (default: 200)")
	flag.BoolVar(&opt.printRunEvents, "print-run-events", true, "Print live run_event frames (default: true)")
	flag.BoolVar(&opt.listElevenVoices, "list-elevenlabs-voices", false, "List available ElevenLabs voices for ELEVENLABS_API_KEY and exit")
	flag.BoolVar(&opt.listMicDevices, "list-mic-devices", false, "List microphone devices via ffmpeg (macOS) and exit")
	flag.BoolVar(&opt.noSpeaker, "no-speaker", false, "Do not spawn ffplay; still simulate playback and emit marks")
	flag.StringVar(&opt.ffplayPath, "ffplay-path", "ffplay", "Path to ffplay executable (default: ffplay)")
	flag.BoolVar(&opt.debug, "debug", false, "Enable debug logging (audio_in_ack, mic stats)")
	flag.BoolVar(&opt.clientEndpointing, "client-endpointing", true, "Send audio_stream_end when local silence is detected (default: true)")
	flag.IntVar(&opt.endpointSilenceMS, "endpoint-silence-ms", 700, "Silence duration (ms) before sending audio_stream_end when client-endpointing is enabled (default: 700)")
	flag.IntVar(&opt.speechThresholdAbs, "speech-threshold-abs", 500, "PCM16 abs amplitude threshold to consider a frame speech (default: 500)")
	flag.BoolVar(&opt.clientHasAEC, "client-has-aec", false, "Set hello.features.client_has_aec=true (can reduce minimum confirmed speech length; default: false)")
	flag.IntVar(&opt.speakerVolume, "speaker-volume", 80, "ffplay startup volume 0=min 100=max (default: 80)")
	flag.StringVar(&opt.dumpAssistantPCM, "dump-assistant-pcm", "", "If set, write assistant audio to this file (raw pcm_s16le @24kHz mono)")
	flag.IntVar(&opt.speakerTestToneMS, "speaker-test-tone-ms", 0, "If >0, play a 440Hz test tone for this many ms at startup (debugging)")
	flag.StringVar(&opt.playedMSMode, "played-ms-mode", "bounded_lag", "Playback mark semantics for played_ms: realtime or bounded_lag (default: bounded_lag)")
	flag.IntVar(&opt.playedLagMS, "played-lag-ms", 1200, "When played-ms-mode=bounded_lag, report played_ms within this lag of received audio (default: 1200)")
	flag.StringVar(&opt.micCmdOverride, "mic-cmd", "", "Override mic capture command (runs via /bin/sh -lc). If set, --mic-device is ignored.")
	flag.Parse()

	// If the user explicitly provided an empty flag value (e.g. `--elevenlabs-voice-id "$ELEVENLABS_VOICE_ID"` when
	// the shell var isn't exported), fall back to the env/.env-loaded value.
	if strings.TrimSpace(opt.elevenVoiceID) == "" {
		opt.elevenVoiceID = strings.TrimSpace(os.Getenv("ELEVENLABS_VOICE_ID"))
	}
	if strings.TrimSpace(opt.gatewayAPIKey) == "" {
		opt.gatewayAPIKey = strings.TrimSpace(os.Getenv("VAI_GATEWAY_API_KEY"))
	}

	if opt.debug {
		fmt.Fprintf(os.Stderr, "[debug] config gateway=%q model=%q voice_id=%q transport=%q mic_device=%d mic_frame_ms=%d no_speaker=%v ffplay_path=%q speaker_volume=%d mark_interval_ms=%d played_ms_mode=%q played_lag_ms=%d\n",
			strings.TrimSpace(opt.gateway),
			strings.TrimSpace(opt.model),
			strings.TrimSpace(opt.elevenVoiceID),
			strings.TrimSpace(opt.transport),
			opt.micDevice,
			opt.micFrameMS,
			opt.noSpeaker,
			strings.TrimSpace(opt.ffplayPath),
			opt.speakerVolume,
			opt.markIntervalMS,
			opt.playedMSMode,
			opt.playedLagMS,
		)
	}

	if opt.listElevenVoices {
		if err := listElevenLabsVoices(); err != nil {
			fmt.Fprintln(os.Stderr, "list elevenlabs voices:", err)
			return 2
		}
		return 0
	}

	if opt.listMicDevices {
		if err := listMicDevices(); err != nil {
			fmt.Fprintln(os.Stderr, "list mic devices:", err)
			return 2
		}
		return 0
	}

	if strings.TrimSpace(opt.gateway) == "" {
		fmt.Fprintln(os.Stderr, "--gateway is required")
		return 2
	}
	if strings.TrimSpace(opt.model) == "" {
		opt.model = defaultModelFromEnv()
		if strings.TrimSpace(opt.model) == "" {
			fmt.Fprintln(os.Stderr, "no --model specified and no LLM API key found in env (.env loaded if present)")
			return 2
		}
	}
	if strings.TrimSpace(opt.elevenVoiceID) == "" {
		fmt.Fprintln(os.Stderr, "--elevenlabs-voice-id is required (or set ELEVENLABS_VOICE_ID)")
		return 2
	}
	if opt.micFrameMS <= 0 {
		fmt.Fprintln(os.Stderr, "--mic-frame-ms must be > 0")
		return 2
	}
	if opt.markIntervalMS <= 0 {
		fmt.Fprintln(os.Stderr, "--mark-interval-ms must be > 0")
		return 2
	}
	if opt.endpointSilenceMS < 0 {
		fmt.Fprintln(os.Stderr, "--endpoint-silence-ms must be >= 0")
		return 2
	}
	if opt.speechThresholdAbs < 0 {
		fmt.Fprintln(os.Stderr, "--speech-threshold-abs must be >= 0")
		return 2
	}
	if opt.speakerVolume < 0 || opt.speakerVolume > 100 {
		fmt.Fprintln(os.Stderr, "--speaker-volume must be between 0 and 100")
		return 2
	}
	opt.playedMSMode = strings.ToLower(strings.TrimSpace(opt.playedMSMode))
	switch opt.playedMSMode {
	case "realtime", "bounded_lag":
	default:
		fmt.Fprintln(os.Stderr, "--played-ms-mode must be realtime or bounded_lag")
		return 2
	}
	if opt.playedLagMS < 0 {
		fmt.Fprintln(os.Stderr, "--played-lag-ms must be >= 0")
		return 2
	}
	if strings.TrimSpace(opt.transport) == "" {
		opt.transport = protocol.AudioTransportBinary
	}
	opt.transport = strings.ToLower(strings.TrimSpace(opt.transport))
	if opt.transport != protocol.AudioTransportBinary && opt.transport != protocol.AudioTransportBase64JSON {
		fmt.Fprintln(os.Stderr, "--transport must be binary or base64_json")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wsURL, err := liveWSURL(opt.gateway)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid --gateway:", err)
		return 2
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial websocket:", err)
		return 1
	}
	defer conn.Close()

	if err := doHandshake(ctx, conn, opt); err != nil {
		fmt.Fprintln(os.Stderr, "handshake failed:", err)
		return 1
	}

	if err := conn.WriteJSON(protocol.ClientAudioStreamStart{
		Type:         "audio_stream_start",
		StreamID:     "mic",
		Encoding:     audioEncodingPCM16LE,
		SampleRateHz: audioInSampleRateHz,
		Channels:     audioChannelsMono,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "failed to send audio_stream_start:", err)
		return 1
	}

	ffplayLogLevel := "error"
	if opt.debug {
		ffplayLogLevel = "info"
	}
	playback := newPlaybackManager(playbackConfig{
		sampleRateHz:     audioOutSampleRateHz,
		channels:         audioChannelsMono,
		bytesPerSample:   2,
		tick:             20 * time.Millisecond,
		markInterval:     time.Duration(opt.markIntervalMS) * time.Millisecond,
		noSpeaker:        opt.noSpeaker,
		ffplayPath:       strings.TrimSpace(opt.ffplayPath),
		ffplayLogLevel:   ffplayLogLevel,
		ffplayVolume:     opt.speakerVolume,
		debug:            opt.debug,
		dumpAssistantPCM: strings.TrimSpace(opt.dumpAssistantPCM),
		playedMSMode:     opt.playedMSMode,
		playedLagMS:      int64(opt.playedLagMS),
	})
	defer playback.Close()
	if opt.speakerTestToneMS > 0 {
		if err := playback.PlayTestTone(time.Duration(opt.speakerTestToneMS) * time.Millisecond); err != nil {
			fmt.Fprintln(os.Stderr, "speaker test tone failed:", err)
		} else {
			fmt.Fprintln(os.Stderr, "[debug] speaker test tone played")
		}
	}

	var writeMu sync.Mutex
	sendJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}
	playback.SetMarkSender(func(mark protocol.ClientPlaybackMark) {
		_ = sendJSON(mark)
	})

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- readServerLoop(ctx, conn, playback, opt.printRunEvents, opt.debug, opt.clientHasAEC)
	}()

	micErrCh := make(chan error, 1)
	go func() {
		micErrCh <- streamMicLoop(ctx, conn, &writeMu, opt)
	}()

	select {
	case <-ctx.Done():
		_ = sendJSON(protocol.ClientControl{Type: "control", Op: "end_session"})
		return 0
	case err := <-playback.ErrCh():
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "playback error:", err)
			return 1
		}
		return 0
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "server loop error:", err)
			return 1
		}
		return 0
	case err := <-micErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "mic loop error:", err)
			return 1
		}
		return 0
	}
}

func doHandshake(ctx context.Context, conn *websocket.Conn, opt options) error {
	providerName, _, err := core.ParseModelString(strings.TrimSpace(opt.model))
	if err != nil {
		return fmt.Errorf("invalid model %q: %w", opt.model, err)
	}

	hello := protocol.ClientHello{
		Type:            "hello",
		ProtocolVersion: liveProtocolVersion,
		Model:           strings.TrimSpace(opt.model),
		AudioIn: protocol.AudioFormat{
			Encoding:     audioEncodingPCM16LE,
			SampleRateHz: audioInSampleRateHz,
			Channels:     audioChannelsMono,
		},
		AudioOut: protocol.AudioFormat{
			Encoding:     audioEncodingPCM16LE,
			SampleRateHz: audioOutSampleRateHz,
			Channels:     audioChannelsMono,
		},
		Voice: &protocol.HelloVoice{
			Provider: protocol.VoiceProviderElevenLabs,
			Language: strings.TrimSpace(opt.lang),
			VoiceID:  strings.TrimSpace(opt.elevenVoiceID),
		},
		Features: protocol.HelloFeatures{
			AudioTransport:         opt.transport,
			SendPlaybackMarks:      true,
			WantPartialTranscripts: true,
			WantAssistantText:      true,
			WantRunEvents:          opt.printRunEvents,
			ClientHasAEC:           opt.clientHasAEC,
		},
		BYOK: protocol.HelloBYOK{
			Keys: make(map[string]string),
		},
	}

	if strings.TrimSpace(opt.gatewayAPIKey) != "" {
		hello.Auth = &protocol.HelloAuth{
			Mode:          "api_key",
			GatewayAPIKey: strings.TrimSpace(opt.gatewayAPIKey),
		}
	}

	llmKey, err := llmKeyForProvider(providerName)
	if err != nil {
		return err
	}
	hello = setBYOK(hello, providerName, llmKey)

	cartesiaKey := strings.TrimSpace(os.Getenv("CARTESIA_API_KEY"))
	if cartesiaKey == "" {
		return fmt.Errorf("missing CARTESIA_API_KEY (required for live STT)")
	}
	hello.BYOK.Cartesia = cartesiaKey

	elevenKey := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	if elevenKey == "" {
		return fmt.Errorf("missing ELEVENLABS_API_KEY (required for elevenlabs TTS)")
	}
	hello.BYOK.ElevenLabs = elevenKey

	handshakeTimeout := 5 * time.Second
	_ = conn.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	if err := conn.WriteJSON(hello); err != nil {
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	typ, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if typ != websocket.TextMessage {
		return fmt.Errorf("expected hello_ack text frame, got messageType=%d", typ)
	}

	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("invalid hello_ack json: %w", err)
	}
	if strings.TrimSpace(env.Type) == "error" {
		var se protocol.ServerError
		_ = json.Unmarshal(data, &se)
		return fmt.Errorf("gateway error: %s (%s)", se.Message, se.Code)
	}
	if strings.TrimSpace(env.Type) != "hello_ack" {
		return fmt.Errorf("expected hello_ack, got %q", strings.TrimSpace(env.Type))
	}
	var ack protocol.ServerHelloAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return fmt.Errorf("invalid hello_ack: %w", err)
	}
	if strings.TrimSpace(ack.SessionID) == "" {
		return fmt.Errorf("hello_ack missing session_id")
	}
	if strings.TrimSpace(ack.Features.AudioTransport) != opt.transport {
		return fmt.Errorf("hello_ack transport mismatch: got=%q want=%q", ack.Features.AudioTransport, opt.transport)
	}
	fmt.Fprintf(os.Stderr, "live session connected: session_id=%s transport=%s\n", ack.SessionID, ack.Features.AudioTransport)
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	return nil
}

func readServerLoop(ctx context.Context, conn *websocket.Conn, playback *playbackManager, printRunEvents bool, debug bool, clientHasAEC bool) error {
	var pendingBinaryHeader *protocol.ServerAssistantAudioChunkHeader
	// Mirror gateway confirmed-speech thresholds so we can print actionable hints.
	minConfirmedChars := 8
	if clientHasAEC {
		minConfirmedChars = 4
	}
	var assistantBytes int64
	var assistantPeak int
	assistantCaptions := make(map[string]*strings.Builder)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		switch messageType {
		case websocket.TextMessage:
			var env struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &env); err != nil {
				fmt.Fprintln(os.Stderr, "invalid server JSON:", err)
				continue
			}
			switch strings.TrimSpace(env.Type) {
			case "transcript_delta":
				var td protocol.ServerTranscriptDelta
				if err := json.Unmarshal(data, &td); err != nil {
					fmt.Fprintln(os.Stderr, "bad transcript_delta:", err)
					continue
				}
				state := "partial"
				if td.IsFinal {
					state = "final"
				}
				fmt.Printf("[stt:%s] %s\n", state, td.Text)
				if td.IsFinal {
					trimmed := strings.TrimSpace(td.Text)
					if trimmed != "" && len([]rune(trimmed)) < minConfirmedChars {
						fmt.Fprintf(os.Stderr, "[hint] live turns only start after \"confirmed\" speech; try saying a longer phrase (>= %d chars), or run with `--client-has-aec`.\n", minConfirmedChars)
					}
				}
			case "utterance_final":
				var uf protocol.ServerUtteranceFinal
				if err := json.Unmarshal(data, &uf); err != nil {
					fmt.Fprintln(os.Stderr, "bad utterance_final:", err)
					continue
				}
				fmt.Printf("[utterance] %s\n", uf.Text)
			case "assistant_audio_start":
				var st protocol.ServerAssistantAudioStart
				if err := json.Unmarshal(data, &st); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_audio_start:", err)
					continue
				}
				if debug {
					assistantBytes = 0
					fmt.Fprintf(os.Stderr, "[debug] assistant_audio_start id=%s\n", st.AssistantAudioID)
				}
				playback.AssistantAudioStart(st.AssistantAudioID)
				assistantCaptions[st.AssistantAudioID] = &strings.Builder{}
			case "assistant_text_delta":
				var td protocol.ServerAssistantTextDelta
				if err := json.Unmarshal(data, &td); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_text_delta:", err)
					continue
				}
				b, ok := assistantCaptions[td.AssistantAudioID]
				if !ok || b == nil {
					b = &strings.Builder{}
					assistantCaptions[td.AssistantAudioID] = b
				}
				b.WriteString(td.Delta)
				if strings.TrimSpace(td.Delta) != "" {
					fmt.Printf("[assistant:text_delta] %s\n", td.Delta)
				}
			case "assistant_text_final":
				var tf protocol.ServerAssistantTextFinal
				if err := json.Unmarshal(data, &tf); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_text_final:", err)
					continue
				}
				if strings.TrimSpace(tf.Text) != "" {
					fmt.Printf("[assistant:text_final] %s\n", tf.Text)
				}
			case "assistant_audio_chunk_header":
				var hdr protocol.ServerAssistantAudioChunkHeader
				if err := json.Unmarshal(data, &hdr); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_audio_chunk_header:", err)
					continue
				}
				pendingBinaryHeader = &hdr
			case "assistant_audio_chunk":
				var chunk protocol.ServerAssistantAudioChunk
				if err := json.Unmarshal(data, &chunk); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_audio_chunk:", err)
					continue
				}
				audio, err := decodeBase64(chunk.AudioB64)
				if err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_audio_chunk audio:", err)
					continue
				}
				if debug && len(audio) > 0 {
					assistantBytes += int64(len(audio))
					if p, _ := pcm16LEStats(audio); p > assistantPeak {
						assistantPeak = p
					}
					if assistantBytes > 0 && assistantBytes%(24000*2) < int64(len(audio)) {
						fmt.Fprintf(os.Stderr, "[debug] assistant audio bytes=%d peak_abs=%d\n", assistantBytes, assistantPeak)
					}
				}
				playback.AssistantAudioChunk(chunk.AssistantAudioID, audio)
			case "assistant_audio_end":
				var end protocol.ServerAssistantAudioEnd
				if err := json.Unmarshal(data, &end); err != nil {
					fmt.Fprintln(os.Stderr, "bad assistant_audio_end:", err)
					continue
				}
				if debug {
					fmt.Fprintf(os.Stderr, "[debug] assistant_audio_end id=%s total_bytes=%d\n", end.AssistantAudioID, assistantBytes)
				}
				playback.AssistantAudioEnd(end.AssistantAudioID)
				delete(assistantCaptions, end.AssistantAudioID)
			case "audio_reset":
				var reset protocol.ServerAudioReset
				if err := json.Unmarshal(data, &reset); err != nil {
					fmt.Fprintln(os.Stderr, "bad audio_reset:", err)
					continue
				}
				playback.AudioReset(reset.AssistantAudioID)
				delete(assistantCaptions, reset.AssistantAudioID)
			case "audio_in_ack":
				if debug {
					var ack protocol.ServerAudioInAck
					if err := json.Unmarshal(data, &ack); err == nil {
						fmt.Fprintf(os.Stderr, "[debug] audio_in_ack last_seq=%d\n", ack.LastSeq)
					}
				}
			case "warning":
				var w protocol.ServerWarning
				if err := json.Unmarshal(data, &w); err != nil {
					fmt.Fprintln(os.Stderr, "bad warning:", err)
					continue
				}
				fmt.Fprintf(os.Stderr, "[warning] %s: %s\n", w.Code, w.Message)
			case "error":
				var se protocol.ServerError
				if err := json.Unmarshal(data, &se); err != nil {
					return fmt.Errorf("bad server error frame: %w", err)
				}
				return fmt.Errorf("server error: %s (%s)", se.Message, se.Code)
			case "run_event":
				if !printRunEvents {
					continue
				}
				var re protocol.ServerRunEvent
				if err := json.Unmarshal(data, &re); err != nil {
					fmt.Fprintln(os.Stderr, "bad run_event:", err)
					continue
				}
				ev, err := coretypes.UnmarshalRunStreamEvent(re.Event)
				if err != nil {
					fmt.Fprintln(os.Stderr, "bad run_event payload:", err)
					continue
				}
				printRunEvent(re.TurnID, ev)
			default:
				// ignore unknown server frames
			}
		case websocket.BinaryMessage:
			if pendingBinaryHeader == nil {
				fmt.Fprintln(os.Stderr, "unexpected binary frame (no pending assistant_audio_chunk_header); dropping")
				continue
			}
			hdr := *pendingBinaryHeader
			pendingBinaryHeader = nil
			chunk := data
			if hdr.Bytes > 0 && len(chunk) != hdr.Bytes {
				if len(chunk) > hdr.Bytes {
					chunk = chunk[:hdr.Bytes]
				} else {
					fmt.Fprintf(os.Stderr, "binary assistant chunk size mismatch: got=%d want=%d; dropping\n", len(chunk), hdr.Bytes)
					continue
				}
			}
			if debug && len(chunk) > 0 {
				assistantBytes += int64(len(chunk))
				if p, _ := pcm16LEStats(chunk); p > assistantPeak {
					assistantPeak = p
				}
				if assistantBytes > 0 && assistantBytes%(24000*2) < int64(len(chunk)) {
					fmt.Fprintf(os.Stderr, "[debug] assistant audio bytes=%d peak_abs=%d\n", assistantBytes, assistantPeak)
				}
			}
			playback.AssistantAudioChunk(hdr.AssistantAudioID, chunk)
		default:
			// ignore
		}
	}
}

func streamMicLoop(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, opt options) error {
	frameBytes, err := micFrameBytes(opt.micFrameMS)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	var stdout io.ReadCloser
	var stderr io.ReadCloser
	if strings.TrimSpace(opt.micCmdOverride) != "" {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-lc", opt.micCmdOverride)
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, _ = cmd.StderrPipe()
	} else {
		args := []string{
			"-hide_banner",
			"-loglevel", "error",
			"-f", "avfoundation",
			// Use `none:<audioIndex>` to avoid opening a video device/camera.
			"-i", fmt.Sprintf("none:%d", opt.micDevice),
			"-ac", "1",
			"-ar", "16000",
			"-f", "s16le",
			"-",
		}
		cmd = exec.CommandContext(ctx, "ffmpeg", args...)
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, _ = cmd.StderrPipe()
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	go streamFilteredFFmpegStderr(stderr, opt.debug)

	reader := bufio.NewReaderSize(stdout, 64*1024)
	buf := make([]byte, 0, frameBytes*4)
	tmp := make([]byte, 16*1024)

	sendBinary := func(p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.BinaryMessage, p)
	}
	sendBase64Frame := func(seq int64, p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(protocol.ClientAudioFrame{
			Type:    "audio_frame",
			Seq:     seq,
			DataB64: base64.StdEncoding.EncodeToString(p),
		})
	}
	sendAudioStreamEnd := func() error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(protocol.ClientAudioStreamEnd{Type: "audio_stream_end", StreamID: "mic"})
	}

	seq := int64(0)
	var totalSent int64
	startedAt := time.Now()
	lastDebugAt := time.Time{}
	lastDataAt := time.Time{}
	warnedNoData := false
	var (
		lastSpeechAt     time.Time
		haveSpeech       bool
		sentFinalize     bool
		lastPeak         int
		lastRMS          float64
		lastEndpointSent time.Time
		warnedSilent     bool
	)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, err := reader.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			totalSent += int64(n)
			lastDataAt = time.Now()
		}
		if !warnedNoData && time.Since(startedAt) > 2*time.Second && lastDataAt.IsZero() {
			warnedNoData = true
			fmt.Fprintln(os.Stderr, "[warning] no mic audio received yet; check macOS microphone permissions and device index (try --list-mic-devices / --mic-device)")
		}
		if opt.debug && (lastDebugAt.IsZero() || time.Since(lastDebugAt) > 2*time.Second) {
			lastDebugAt = time.Now()
			if !lastDataAt.IsZero() {
				fmt.Fprintf(os.Stderr, "[debug] mic bytes read=%d peak_abs=%d rms=%.4f\n", totalSent, lastPeak, lastRMS)
			}
		}
		if !warnedSilent && !lastDataAt.IsZero() && time.Since(startedAt) > 3*time.Second && lastPeak == 0 {
			warnedSilent = true
			fmt.Fprintln(os.Stderr, "[warning] mic audio appears to be all zeros (peak_abs=0). This usually means the wrong input device is selected or macOS microphone permission is not granted to your terminal app.")
			fmt.Fprintln(os.Stderr, "          Try: `--mic-device 1` (your ULT WEAR), or run `--list-mic-devices`, and check System Settings → Privacy & Security → Microphone for your terminal.")
		}
		for len(buf) >= frameBytes {
			frame := buf[:frameBytes]
			seq++
			peak, rms := pcm16LEStats(frame)
			lastPeak, lastRMS = peak, rms

			now := time.Now()
			if peak >= opt.speechThresholdAbs {
				lastSpeechAt = now
				haveSpeech = true
				sentFinalize = false
			}
			if opt.clientEndpointing && haveSpeech && !sentFinalize && opt.endpointSilenceMS > 0 && !lastSpeechAt.IsZero() {
				if now.Sub(lastSpeechAt) >= time.Duration(opt.endpointSilenceMS)*time.Millisecond {
					// Avoid spamming finalize if the input is marginal and toggling.
					if lastEndpointSent.IsZero() || now.Sub(lastEndpointSent) >= 250*time.Millisecond {
						if opt.debug {
							fmt.Fprintf(os.Stderr, "[debug] client endpoint: sending audio_stream_end (peak=%d)\n", peak)
						}
						_ = sendAudioStreamEnd()
						lastEndpointSent = now
					}
					sentFinalize = true
					haveSpeech = false
				}
			}

			if opt.transport == protocol.AudioTransportBinary {
				if err := sendBinary(frame); err != nil {
					return err
				}
			} else {
				if err := sendBase64Frame(seq, frame); err != nil {
					return err
				}
			}
			buf = buf[frameBytes:]
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("mic capture ended (EOF)")
			}
			return err
		}
	}
}

func pcm16LEStats(p []byte) (peakAbs int, rms float64) {
	if len(p) < 2 {
		return 0, 0
	}
	var sumSquares float64
	samples := 0
	for i := 0; i+1 < len(p); i += 2 {
		v := int16(binary.LittleEndian.Uint16(p[i : i+2]))
		abs := int(v)
		if abs < 0 {
			abs = -abs
		}
		if abs > peakAbs {
			peakAbs = abs
		}
		f := float64(v) / 32768.0
		sumSquares += f * f
		samples++
	}
	if samples == 0 {
		return peakAbs, 0
	}
	return peakAbs, math.Sqrt(sumSquares / float64(samples))
}

func streamFilteredFFmpegStderr(r io.ReadCloser, debug bool) {
	if r == nil {
		return
	}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// Filter noisy macOS AVFoundation warnings that are not actionable for our CLI.
		if strings.Contains(line, "NSCameraUseContinuityCameraDeviceType") ||
			strings.Contains(line, "AVCaptureDeviceTypeExternal is deprecated for Continuity Cameras") {
			if debug {
				fmt.Fprintln(os.Stderr, "[debug] suppressed macOS avfoundation warning:", line)
			}
			continue
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

func printRunEvent(turnID int, ev coretypes.RunStreamEvent) {
	switch e := ev.(type) {
	case coretypes.RunStartEvent:
		fmt.Printf("[run:%d] run_start model=%s\n", turnID, e.Model)
	case coretypes.RunStepStartEvent:
		fmt.Printf("[run:%d] step_start index=%d\n", turnID, e.Index)
	case coretypes.RunToolCallStartEvent:
		if e.Name == "talk_to_user" {
			fmt.Printf("[run:%d] tool_call_start talk_to_user input=%s\n", turnID, compactJSON(e.Input))
			return
		}
		fmt.Printf("[run:%d] tool_call_start name=%s\n", turnID, e.Name)
	case coretypes.RunToolResultEvent:
		if e.Name == "talk_to_user" {
			fmt.Printf("[run:%d] tool_result talk_to_user is_error=%v\n", turnID, e.IsError)
			return
		}
		fmt.Printf("[run:%d] tool_result name=%s is_error=%v\n", turnID, e.Name, e.IsError)
	case coretypes.RunStepCompleteEvent:
		fmt.Printf("[run:%d] step_complete index=%d\n", turnID, e.Index)
	case coretypes.RunHistoryDeltaEvent:
		fmt.Printf("[run:%d] history_delta append=%d expected_len=%d\n", turnID, len(e.Append), e.ExpectedLen)
	case coretypes.RunCompleteEvent:
		fmt.Printf("[run:%d] run_complete tool_calls=%d\n", turnID, func() int {
			if e.Result == nil {
				return 0
			}
			return e.Result.ToolCallCount
		}())
	case coretypes.RunErrorEvent:
		fmt.Printf("[run:%d] error type=%s message=%s\n", turnID, e.Error.Type, e.Error.Message)
	default:
		fmt.Printf("[run:%d] %s\n", turnID, ev.EventType())
	}
}

func compactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, data); err != nil {
		return string(data)
	}
	return out.String()
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// Use StdEncoding; server uses standard base64.
	return base64.StdEncoding.DecodeString(s)
}

func micFrameBytes(frameMS int) (int, error) {
	if frameMS <= 0 {
		return 0, fmt.Errorf("mic frame ms must be > 0")
	}
	samples := (audioInSampleRateHz * frameMS) / 1000
	if samples <= 0 {
		samples = 1
	}
	return samples * 2, nil
}

func liveWSURL(gateway string) (string, error) {
	raw := strings.TrimSpace(gateway)
	if raw == "" {
		return "", fmt.Errorf("empty gateway")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	// Preserve any base path, but always route to /v1/live.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/live"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func listMicDevices() error {
	cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// ffmpeg commonly exits non-zero after printing device lists; treat that as success
		// as long as the binary executed.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	}
	return nil
}

func listElevenLabsVoices() error {
	apiKey := strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("missing ELEVENLABS_API_KEY")
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.elevenlabs.io/v1/voices", nil)
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Voices []struct {
			VoiceID  string `json:"voice_id"`
			Name     string `json:"name"`
			Category string `json:"category,omitempty"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	if len(payload.Voices) == 0 {
		fmt.Println("(no voices returned)")
		return nil
	}
	for _, v := range payload.Voices {
		line := fmt.Sprintf("%s\t%s", strings.TrimSpace(v.VoiceID), strings.TrimSpace(v.Name))
		if strings.TrimSpace(v.Category) != "" {
			line += "\t(" + strings.TrimSpace(v.Category) + ")"
		}
		fmt.Println(line)
	}
	return nil
}

func normalizeEnvKeys() {
	if os.Getenv("GEMINI_API_KEY") == "" {
		if googleKey := os.Getenv("GOOGLE_API_KEY"); googleKey != "" {
			_ = os.Setenv("GEMINI_API_KEY", googleKey)
		}
	}
}

func defaultModelFromEnv() string {
	candidates := []struct {
		envKey string
		model  string
	}{
		{envKey: "ANTHROPIC_API_KEY", model: "anthropic/claude-haiku-4-5-20251001"},
		{envKey: "OPENAI_API_KEY", model: "oai-resp/gpt-5-mini"},
		{envKey: "GEMINI_API_KEY", model: "gemini/gemini-3-flash-preview"},
		{envKey: "GOOGLE_API_KEY", model: "gemini/gemini-3-flash-preview"},
		{envKey: "GROQ_API_KEY", model: "groq/moonshotai/kimi-k2-instruct-0905"},
		{envKey: "OPENROUTER_API_KEY", model: "openrouter/z-ai/glm-4.7"},
		{envKey: "GEMINI_OAUTH_PROJECT_ID", model: "gemini-oauth/gemini-3-pro-preview"},
	}
	for _, c := range candidates {
		if strings.TrimSpace(os.Getenv(c.envKey)) != "" {
			return c.model
		}
	}
	if strings.TrimSpace(readGeminiOAuthProjectID()) != "" {
		return "gemini-oauth/gemini-3-pro-preview"
	}
	return ""
}

func llmKeyForProvider(providerName string) (string, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	switch providerName {
	case "anthropic":
		return requireEnv("ANTHROPIC_API_KEY")
	case "oai-resp", "openai":
		return requireEnv("OPENAI_API_KEY")
	case "gemini":
		if key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); key != "" {
			return key, nil
		}
		if key := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); key != "" {
			return key, nil
		}
		return "", fmt.Errorf("missing GEMINI_API_KEY (or GOOGLE_API_KEY)")
	case "groq":
		return requireEnv("GROQ_API_KEY")
	case "cerebras":
		return requireEnv("CEREBRAS_API_KEY")
	case "openrouter":
		return requireEnv("OPENROUTER_API_KEY")
	case "gemini-oauth":
		if projectID := strings.TrimSpace(os.Getenv("GEMINI_OAUTH_PROJECT_ID")); projectID != "" {
			return projectID, nil
		}
		if projectID := strings.TrimSpace(readGeminiOAuthProjectID()); projectID != "" {
			return projectID, nil
		}
		return "", fmt.Errorf("missing GEMINI_OAUTH_PROJECT_ID (and no credentials file found)")
	default:
		return "", fmt.Errorf("unsupported model provider %q for key inference", providerName)
	}
}

func setBYOK(hello protocol.ClientHello, providerName, key string) protocol.ClientHello {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	switch providerName {
	case "anthropic":
		hello.BYOK.Anthropic = key
	case "oai-resp", "openai":
		hello.BYOK.OpenAI = key
	case "gemini", "gemini-oauth":
		hello.BYOK.Gemini = key
	case "groq":
		hello.BYOK.Groq = key
	case "cerebras":
		hello.BYOK.Cerebras = key
	case "openrouter":
		hello.BYOK.OpenRouter = key
	default:
		if hello.BYOK.Keys == nil {
			hello.BYOK.Keys = make(map[string]string)
		}
		hello.BYOK.Keys[providerName] = key
	}
	return hello
}

func requireEnv(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("missing %s", name)
	}
	return v, nil
}

func readGeminiOAuthProjectID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	credsPath := filepath.Join(home, ".config", "vango", "gemini-oauth-credentials.json")
	data, err := os.ReadFile(credsPath)
	if err != nil {
		return ""
	}
	var creds struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return strings.TrimSpace(creds.ProjectID)
}

func loadEnvFileBestEffort() {
	// Prefer: walk up from cwd to find .env (works with `go run` from subdirs).
	cwd, err := os.Getwd()
	if err == nil {
		if loadEnvFileFromAncestors(cwd, 8) {
			return
		}
	}

	// Fallback: locate repo root relative to this source file (works in tests/binaries).
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	// cmd/vai-live-audio-it/main.go -> repo root is ../../
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	_ = loadEnvFile(filepath.Join(root, ".env"))
}

func loadEnvFileFromAncestors(start string, maxLevels int) bool {
	dir := start
	for i := 0; i <= maxLevels; i++ {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			_ = loadEnvFile(p)
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			if strings.HasPrefix(key, "export ") {
				key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
			}
			if key == "" {
				continue
			}
			value = strings.TrimSpace(value)
			if len(value) >= 2 {
				if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
					value = value[1 : len(value)-1]
				}
			}
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, value)
			}
		}
	}
	return scanner.Err()
}
