package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/types"
	vai "github.com/vango-go/vai-lite/sdk"
)

const (
	micSampleRateHz      = 16000
	playbackSampleRateHz = 24000
	pcmBytesPerSample    = 2
	playbackMarkInterval = 250 * time.Millisecond
)

func validateLiveModeConfig(cfg chatConfig, model string) error {
	if _, err := validateModelWithKeys(model, cfg.ProviderKeys); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.LiveVoiceProvider)) {
	case "cartesia", "elevenlabs":
	default:
		return errors.New("live voice provider must be cartesia or elevenlabs")
	}
	if strings.TrimSpace(cfg.LiveVoiceID) == "" {
		return errors.New("live voice id is required (set --live-voice-id or VAI_LIVE_VOICE_ID)")
	}
	if strings.TrimSpace(cfg.CartesiaAPIKey) == "" {
		return errors.New("CARTESIA_API_KEY is required for live STT")
	}
	if strings.EqualFold(strings.TrimSpace(cfg.LiveVoiceProvider), "elevenlabs") && strings.TrimSpace(cfg.ElevenLabsAPIKey) == "" {
		return errors.New("ELEVENLABS_API_KEY is required when live voice provider is elevenlabs")
	}
	return nil
}

func runLiveMode(
	ctx context.Context,
	scanner *bufio.Scanner,
	client *vai.Client,
	cfg chatConfig,
	state *chatRuntime,
	out io.Writer,
	errOut io.Writer,
) error {
	if scanner == nil {
		return errors.New("scanner must not be nil")
	}
	if client == nil || client.Live == nil {
		return errors.New("live service is not available")
	}
	if state == nil {
		return errors.New("chat state must not be nil")
	}
	if err := validateLiveModeConfig(cfg, state.currentModel); err != nil {
		return err
	}
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	connectReq := &vai.LiveConnectRequest{
		Model:    state.currentModel,
		System:   cfg.SystemPrompt,
		Messages: append([]vai.Message(nil), state.history...),
		Voice: vai.LiveVoice{
			Provider: cfg.LiveVoiceProvider,
			VoiceID:  cfg.LiveVoiceID,
			Language: cfg.LiveLanguage,
		},
		Features: vai.LiveFeatures{
			AudioTransport:         "base64_json",
			SendPlaybackMarks:      true,
			WantPartialTranscripts: true,
			WantAssistantText:      true,
			ClientHasAEC:           true,
			WantRunEvents:          true,
		},
		Tools: vai.LiveTools{
			ServerTools: []string{"vai_web_search", "vai_web_fetch"},
			ServerToolConfig: map[string]any{
				"vai_web_search": map[string]any{
					"provider": "tavily",
				},
				"vai_web_fetch": map[string]any{
					"provider": "tavily",
					"format":   "markdown",
				},
			},
		},
	}

	session, err := client.Live.Connect(ctx, connectReq)
	if err != nil {
		return err
	}
	defer session.Close()

	mic, err := newFFmpegMicCapture()
	if err != nil {
		return err
	}
	defer mic.Close()

	player, err := newFFplayPCMPlayer()
	if err != nil {
		return err
	}
	defer player.Close()

	liveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	tracker := &playbackTracker{}
	var seq atomic.Int64
	seq.Store(1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 1024)
		for {
			if liveCtx.Err() != nil {
				return
			}
			n, readErr := mic.Read(buf)
			if n > 0 {
				frame := make([]byte, n)
				copy(frame, buf[:n])
				currentSeq := seq.Add(1) - 1
				if sendErr := session.SendAudioFrame(frame, vai.LiveAudioMeta{Seq: currentSeq}); sendErr != nil {
					fmt.Fprintf(errOut, "live mic send error: %v\n", sendErr)
					return
				}
			}
			if readErr != nil {
				if liveCtx.Err() == nil && !errors.Is(readErr, io.EOF) {
					fmt.Fprintf(errOut, "live mic read error: %v\n", readErr)
				}
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(playbackMarkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-liveCtx.Done():
				return
			case <-ticker.C:
				assistantID, playedMS, bufferedMS, ok := tracker.playingSnapshot()
				if !ok {
					continue
				}
				_ = session.SendPlaybackMark(vai.LivePlaybackMark{
					AssistantAudioID: assistantID,
					PlayedMS:         playedMS,
					BufferedMS:       bufferedMS,
					State:            "playing",
				})
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range session.Events() {
			switch e := event.(type) {
			case vai.LiveTranscriptDeltaEvent:
				if e.IsFinal {
					fmt.Fprintf(out, "\n[user] %s\n", strings.TrimSpace(e.Text))
				}
			case vai.LiveAssistantTextDeltaEvent:
				fmt.Fprint(out, e.Delta)
			case vai.LiveAssistantTextFinalEvent:
				fmt.Fprintln(out)
			case vai.LiveAssistantAudioStartEvent:
				tracker.start(e.Start.AssistantAudioID)
			case vai.LiveAssistantAudioChunkEvent:
				if err := player.Write(e.Data); err != nil {
					fmt.Fprintf(errOut, "live playback error: %v\n", err)
					continue
				}
				tracker.addBytes(e.AssistantAudioID, int64(len(e.Data)))
			case vai.LiveAssistantAudioEndEvent:
				assistantID, playedMS, bufferedMS, ok := tracker.finish(e.End.AssistantAudioID)
				if ok {
					_ = session.SendPlaybackMark(vai.LivePlaybackMark{
						AssistantAudioID: assistantID,
						PlayedMS:         playedMS,
						BufferedMS:       bufferedMS,
						State:            "finished",
					})
				}
			case vai.LiveAudioResetEvent:
				_ = player.Reset()
				assistantID, playedMS, bufferedMS, ok := tracker.stop(e.AssistantAudioID)
				if ok {
					_ = session.SendPlaybackMark(vai.LivePlaybackMark{
						AssistantAudioID: assistantID,
						PlayedMS:         playedMS,
						BufferedMS:       bufferedMS,
						State:            "stopped",
					})
				}
			case vai.LiveRunEvent:
				switch inner := e.Event.(type) {
				case types.RunToolCallStartEvent:
					fmt.Fprintf(out, "\n[tool] %s\n", strings.TrimSpace(inner.Name))
				case types.RunHistoryDeltaEvent:
					applyLiveHistoryDelta(state, inner, errOut)
				}
			case vai.LiveErrorEvent:
				fmt.Fprintf(errOut, "live session error: %s\n", strings.TrimSpace(e.Error.Message))
			}
		}
	}()

	fmt.Fprintf(out, "Live mode connected using %s (%s voice)\n", state.currentModel, cfg.LiveVoiceProvider)
	fmt.Fprintln(out, "Live commands: /text to return, /interrupt to barge-in, /end to end live session, /exit to quit.")
	for {
		fmt.Fprint(out, "(live)> ")
		if !scanner.Scan() {
			cancel()
			wg.Wait()
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read live command: %w", err)
			}
			fmt.Fprintln(out)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch line {
		case "/text":
			cancel()
			wg.Wait()
			fmt.Fprintln(out, "returned to text mode")
			return nil
		case "/interrupt":
			if err := session.Interrupt(); err != nil {
				fmt.Fprintf(errOut, "interrupt error: %v\n", err)
			}
		case "/end":
			_ = session.EndSession()
			cancel()
			wg.Wait()
			fmt.Fprintln(out, "live session ended")
			return nil
		case "/exit", "/quit":
			_ = session.EndSession()
			cancel()
			wg.Wait()
			return errChatExitRequested
		default:
			if handled, cmdErr := handleSlashCommand(line, state, cfg, out, errOut); cmdErr != nil {
				cancel()
				wg.Wait()
				return cmdErr
			} else if handled {
				fmt.Fprintln(out, "model updates apply on the next /live session")
				continue
			}
			fmt.Fprintln(out, "live commands: /text, /interrupt, /end, /model, /model:{provider}/{model}, /exit")
		}
	}
}

func applyLiveHistoryDelta(state *chatRuntime, delta types.RunHistoryDeltaEvent, errOut io.Writer) {
	if state == nil {
		return
	}
	if delta.ExpectedLen >= 0 && len(state.history) != delta.ExpectedLen {
		fmt.Fprintf(errOut, "live history warning: expected len %d, local len %d\n", delta.ExpectedLen, len(state.history))
	}
	state.history = append(state.history, delta.Append...)
}

type playbackTracker struct {
	mu          sync.Mutex
	assistantID string
	sentBytes   int64
	startedAt   time.Time
}

func (p *playbackTracker) start(assistantID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assistantID = strings.TrimSpace(assistantID)
	p.sentBytes = 0
	p.startedAt = time.Now()
}

func (p *playbackTracker) addBytes(assistantID string, n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" {
		return
	}
	if p.assistantID == "" {
		p.assistantID = assistantID
		p.startedAt = time.Now()
	}
	if assistantID != p.assistantID {
		return
	}
	p.sentBytes += n
}

func (p *playbackTracker) playingSnapshot() (assistantID string, playedMS int64, bufferedMS int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.assistantID == "" {
		return "", 0, 0, false
	}
	sentMS := bytesToMS(p.sentBytes, playbackSampleRateHz)
	elapsedMS := time.Since(p.startedAt).Milliseconds()
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	if elapsedMS > sentMS {
		elapsedMS = sentMS
	}
	buffered := sentMS - elapsedMS
	if buffered < 0 {
		buffered = 0
	}
	return p.assistantID, elapsedMS, buffered, true
}

func (p *playbackTracker) finish(assistantID string) (string, int64, int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	assistantID = strings.TrimSpace(assistantID)
	if p.assistantID == "" {
		return "", 0, 0, false
	}
	if assistantID != "" && assistantID != p.assistantID {
		return "", 0, 0, false
	}
	currentID := p.assistantID
	sentMS := bytesToMS(p.sentBytes, playbackSampleRateHz)
	p.assistantID = ""
	p.sentBytes = 0
	p.startedAt = time.Time{}
	return currentID, sentMS, 0, true
}

func (p *playbackTracker) stop(assistantID string) (string, int64, int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.assistantID == "" {
		return "", 0, 0, false
	}
	assistantID = strings.TrimSpace(assistantID)
	if assistantID != "" && assistantID != p.assistantID {
		return "", 0, 0, false
	}
	sentMS := bytesToMS(p.sentBytes, playbackSampleRateHz)
	elapsedMS := time.Since(p.startedAt).Milliseconds()
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	if elapsedMS > sentMS {
		elapsedMS = sentMS
	}
	buffered := sentMS - elapsedMS
	if buffered < 0 {
		buffered = 0
	}
	currentID := p.assistantID
	p.assistantID = ""
	p.sentBytes = 0
	p.startedAt = time.Time{}
	return currentID, elapsedMS, buffered, true
}

func bytesToMS(bytes int64, sampleRate int64) int64 {
	if sampleRate <= 0 {
		return 0
	}
	return (bytes * 1000) / (sampleRate * pcmBytesPerSample)
}

type ffmpegMicCapture struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

func newFFmpegMicCapture() (*ffmpegMicCapture, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("ffmpeg is required for /live mic capture (install ffmpeg and ensure it is in PATH)")
	}
	args, err := liveMicFFmpegArgs(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open ffmpeg stdout: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg mic capture: %w", err)
	}
	return &ffmpegMicCapture{cmd: cmd, stdout: stdout}, nil
}

func liveMicFFmpegArgs(goos string) ([]string, error) {
	switch goos {
	case "darwin":
		return []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "avfoundation", "-i", ":0",
			"-ac", "1", "-ar", fmt.Sprintf("%d", micSampleRateHz),
			"-f", "s16le", "-",
		}, nil
	case "linux":
		return []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "pulse", "-i", "default",
			"-ac", "1", "-ar", fmt.Sprintf("%d", micSampleRateHz),
			"-f", "s16le", "-",
		}, nil
	default:
		return nil, fmt.Errorf("live mic capture is not implemented for %s; supported platforms: darwin, linux", goos)
	}
}

func (m *ffmpegMicCapture) Read(p []byte) (int, error) {
	if m == nil || m.stdout == nil {
		return 0, io.EOF
	}
	return m.stdout.Read(p)
}

func (m *ffmpegMicCapture) Close() error {
	if m == nil {
		return nil
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	return nil
}

type ffplayPCMPlayer struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func newFFplayPCMPlayer() (*ffplayPCMPlayer, error) {
	if _, err := exec.LookPath("ffplay"); err != nil {
		return nil, errors.New("ffplay is required for /live playback (install ffmpeg/ffplay and ensure it is in PATH)")
	}
	player := &ffplayPCMPlayer{}
	if err := player.startLocked(); err != nil {
		return nil, err
	}
	return player, nil
}

func (p *ffplayPCMPlayer) startLocked() error {
	p.cmd = exec.Command("ffplay",
		"-nodisp",
		"-autoexit",
		"-loglevel", "error",
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", playbackSampleRateHz),
		"-ac", "1",
		"-i", "pipe:0",
	)
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open ffplay stdin: %w", err)
	}
	p.cmd.Stdout = io.Discard
	p.cmd.Stderr = io.Discard
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start ffplay: %w", err)
	}
	p.stdin = stdin
	return nil
}

func (p *ffplayPCMPlayer) Write(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return errors.New("ffplay stdin is not initialized")
	}
	_, err := p.stdin.Write(data)
	return err
}

func (p *ffplayPCMPlayer) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	p.stdin = nil
	return p.startLocked()
}

func (p *ffplayPCMPlayer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	p.stdin = nil
	return nil
}
