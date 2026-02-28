package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

type playbackConfig struct {
	sampleRateHz   int
	channels       int
	bytesPerSample int
	tick           time.Duration
	markInterval   time.Duration

	noSpeaker      bool
	ffplayPath     string
	ffplayLogLevel string
	ffplayVolume   int
	debug          bool
	dumpAssistantPCM string

	playedMSMode string
	playedLagMS  int64
}

type playbackManager struct {
	cfg playbackConfig

	mu sync.Mutex

	activeAssistantID string
	buffer            bytes.Buffer
	playedBytes       int64
	sentBytes         int64
	reportedPlayedMS  int64
	endReceived       bool
	finishedSent      bool

	lastMarkAt time.Time

	sendMark func(protocol.ClientPlaybackMark)

	speaker *ffplaySpeaker
	dumpFile *os.File

	ctx    context.Context
	cancel context.CancelFunc

	errCh chan error
}

func newPlaybackManager(cfg playbackConfig) *playbackManager {
	if cfg.sampleRateHz <= 0 {
		cfg.sampleRateHz = audioOutSampleRateHz
	}
	if cfg.channels <= 0 {
		cfg.channels = 1
	}
	if cfg.bytesPerSample <= 0 {
		cfg.bytesPerSample = 2
	}
	if cfg.tick <= 0 {
		cfg.tick = 20 * time.Millisecond
	}
	if cfg.markInterval <= 0 {
		cfg.markInterval = 200 * time.Millisecond
	}
	if strings.TrimSpace(cfg.ffplayPath) == "" {
		cfg.ffplayPath = "ffplay"
	}
	if strings.TrimSpace(cfg.ffplayLogLevel) == "" {
		cfg.ffplayLogLevel = "error"
	}
	if cfg.ffplayVolume <= 0 {
		cfg.ffplayVolume = 80
	}
	if strings.TrimSpace(cfg.playedMSMode) == "" {
		cfg.playedMSMode = "bounded_lag"
	}
	cfg.playedMSMode = strings.ToLower(strings.TrimSpace(cfg.playedMSMode))
	if cfg.playedLagMS <= 0 {
		cfg.playedLagMS = 1200
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := &playbackManager{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
		errCh:  make(chan error, 1),
	}
	if !cfg.noSpeaker {
		m.speaker = newFFPlaySpeaker(cfg.ffplayPath, cfg.sampleRateHz, cfg.channels, cfg.ffplayLogLevel, cfg.ffplayVolume, cfg.debug)
		if err := m.speaker.Start(); err != nil {
			m.emitErr(err)
		}
	}
	go m.run()
	return m
}

func (m *playbackManager) ErrCh() <-chan error {
	if m == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return m.errCh
}

func (m *playbackManager) SetMarkSender(fn func(protocol.ClientPlaybackMark)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendMark = fn
}

func (m *playbackManager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	if m.speaker != nil {
		_ = m.speaker.Close()
	}
	if m.dumpFile != nil {
		_ = m.dumpFile.Close()
	}
	return nil
}

func (m *playbackManager) PlayTestTone(d time.Duration) error {
	if m == nil {
		return nil
	}
	if d <= 0 {
		return nil
	}
	if m.cfg.noSpeaker || m.speaker == nil {
		return fmt.Errorf("speaker disabled")
	}
	if err := m.speaker.EnsureRunning(); err != nil {
		return err
	}
	pcm := sineTonePCM16LE(440, m.cfg.sampleRateHz, d, 0.2)
	if len(pcm) == 0 {
		return nil
	}
	// Stream the tone in realtime-ish chunks to match how we feed assistant audio.
	bytesPerSecond := m.cfg.sampleRateHz * m.cfg.channels * m.cfg.bytesPerSample
	if bytesPerSecond <= 0 {
		bytesPerSecond = 24000 * 1 * 2
	}
	tick := m.cfg.tick
	if tick <= 0 {
		tick = 20 * time.Millisecond
	}
	bytesPerTick := int64(bytesPerSecond) * int64(tick) / int64(time.Second)
	if bytesPerTick <= 0 {
		bytesPerTick = 960
	}
	for off := int64(0); off < int64(len(pcm)); off += bytesPerTick {
		end := off + bytesPerTick
		if end > int64(len(pcm)) {
			end = int64(len(pcm))
		}
		if err := m.speaker.Write(pcm[off:end]); err != nil {
			return err
		}
		time.Sleep(tick)
	}
	return nil
}

func (m *playbackManager) AssistantAudioStart(assistantID string) {
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" || m == nil {
		return
	}

	m.mu.Lock()
	m.activeAssistantID = assistantID
	m.buffer.Reset()
	m.playedBytes = 0
	m.sentBytes = 0
	m.reportedPlayedMS = 0
	m.endReceived = false
	m.finishedSent = false
	m.lastMarkAt = time.Time{}
	m.mu.Unlock()

	if strings.TrimSpace(m.cfg.dumpAssistantPCM) != "" {
		m.mu.Lock()
		if m.dumpFile == nil {
			f, err := os.OpenFile(m.cfg.dumpAssistantPCM, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				m.mu.Unlock()
				m.emitErr(err)
			} else {
				m.dumpFile = f
				m.mu.Unlock()
				fmt.Fprintf(os.Stderr, "[debug] dumping assistant pcm to %s (ffplay -f s16le -ar %d -ch_layout mono -i %s)\n", m.cfg.dumpAssistantPCM, m.cfg.sampleRateHz, m.cfg.dumpAssistantPCM)
			}
		} else {
			m.mu.Unlock()
		}
	}

	if !m.cfg.noSpeaker && m.speaker != nil {
		if err := m.speaker.EnsureRunning(); err != nil {
			m.emitErr(err)
		}
	}
}

func (m *playbackManager) AssistantAudioChunk(assistantID string, pcm []byte) {
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" || m == nil || len(pcm) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeAssistantID != assistantID {
		// Drop late or unexpected audio.
		return
	}
	_, _ = m.buffer.Write(pcm)
	m.sentBytes += int64(len(pcm))
	if m.dumpFile != nil {
		_, err := m.dumpFile.Write(pcm)
		if err != nil {
			m.emitErr(err)
		}
	}
}

func (m *playbackManager) AssistantAudioEnd(assistantID string) {
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" || m == nil {
		return
	}

	var mark *protocol.ClientPlaybackMark

	m.mu.Lock()
	if m.activeAssistantID == assistantID {
		m.endReceived = true
		if m.buffer.Len() == 0 && !m.finishedSent {
			m.finishedSent = true
			mark = m.buildMarkLocked("finished")
			m.activeAssistantID = ""
		}
	}
	m.mu.Unlock()

	if mark != nil {
		m.emitMark(*mark)
	}
}

func (m *playbackManager) AudioReset(assistantID string) {
	if m == nil {
		return
	}
	assistantID = strings.TrimSpace(assistantID)

	var mark *protocol.ClientPlaybackMark

	m.mu.Lock()
	if assistantID == "" || m.activeAssistantID == assistantID {
		if m.activeAssistantID != "" {
			mark = m.buildMarkLocked("stopped")
		}
		m.activeAssistantID = ""
		m.buffer.Reset()
		m.playedBytes = 0
		m.endReceived = false
		m.finishedSent = false
	}
	m.mu.Unlock()

	if mark != nil {
		m.emitMark(*mark)
	}

	if !m.cfg.noSpeaker && m.speaker != nil {
		if err := m.speaker.Restart(); err != nil {
			m.emitErr(err)
		}
	}
}

func (m *playbackManager) run() {
	tick := m.cfg.tick
	if tick <= 0 {
		tick = 20 * time.Millisecond
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.onTick()
		}
	}
}

func (m *playbackManager) onTick() {
	if m == nil {
		return
	}

	bytesPerSecond := int64(m.cfg.sampleRateHz * m.cfg.channels * m.cfg.bytesPerSample)
	if bytesPerSecond <= 0 {
		return
	}
	bytesPerTick := bytesPerSecond * int64(m.cfg.tick) / int64(time.Second)
	if bytesPerTick <= 0 {
		bytesPerTick = 1
	}

	var (
		activeID   string
		toPlay     []byte
		markToSend *protocol.ClientPlaybackMark
	)
	var debugLine string

	m.mu.Lock()
	activeID = m.activeAssistantID
	if activeID != "" && m.buffer.Len() > 0 {
		n := int(bytesPerTick)
		if n > m.buffer.Len() {
			n = m.buffer.Len()
		}
		toPlay = make([]byte, n)
		_, _ = io.ReadFull(&m.buffer, toPlay)
		m.playedBytes += int64(n)
	}

	now := time.Now()
	shouldMark := activeID != "" && (m.lastMarkAt.IsZero() || now.Sub(m.lastMarkAt) >= m.cfg.markInterval)
	if shouldMark {
		m.lastMarkAt = now
		markToSend = m.buildMarkLocked("playing")
		if m.cfg.debug && markToSend != nil {
			debugLine = fmt.Sprintf("[debug] playback assistant_id=%s played_ms=%d buffered_ms=%d\n", markToSend.AssistantAudioID, markToSend.PlayedMS, markToSend.BufferedMS)
		}
	}

	if activeID != "" && m.endReceived && m.buffer.Len() == 0 && !m.finishedSent {
		m.finishedSent = true
		markToSend = m.buildMarkLocked("finished")
		m.activeAssistantID = ""
	}
	m.mu.Unlock()

	if debugLine != "" {
		fmt.Fprint(os.Stderr, debugLine)
	}
	if len(toPlay) > 0 {
		if m.cfg.noSpeaker {
			// Simulate playback: consume bytes and advance playedBytes without output.
		} else if m.speaker != nil {
			if err := m.speaker.Write(toPlay); err != nil {
				m.emitErr(err)
			}
		}
	}
	if markToSend != nil {
		m.emitMark(*markToSend)
	}
}

func (m *playbackManager) buildMarkLocked(state string) *protocol.ClientPlaybackMark {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(m.activeAssistantID) == "" {
		return nil
	}
	bytesPerSecond := int64(m.cfg.sampleRateHz * m.cfg.channels * m.cfg.bytesPerSample)
	if bytesPerSecond <= 0 {
		bytesPerSecond = int64(24000 * 1 * 2)
	}
	actualPlayedMS := (m.playedBytes * 1000) / bytesPerSecond
	sentMS := (m.sentBytes * 1000) / bytesPerSecond
	playedMS := actualPlayedMS
	if m.cfg.playedMSMode == "bounded_lag" {
		target := sentMS - m.cfg.playedLagMS
		if target < 0 {
			target = 0
		}
		if target > playedMS {
			playedMS = target
		}
		if playedMS < m.reportedPlayedMS {
			playedMS = m.reportedPlayedMS
		}
	}
	if playedMS > sentMS {
		playedMS = sentMS
	}
	if playedMS < 0 {
		playedMS = 0
	}
	if playedMS > m.reportedPlayedMS {
		m.reportedPlayedMS = playedMS
	}
	bufferedMS := bufferedMSFromBytes(m.buffer.Len(), m.cfg.sampleRateHz, m.cfg.channels, m.cfg.bytesPerSample)
	return &protocol.ClientPlaybackMark{
		Type:             "playback_mark",
		AssistantAudioID: m.activeAssistantID,
		PlayedMS:         playedMS,
		BufferedMS:       bufferedMS,
		State:            state,
	}
}

func (m *playbackManager) emitMark(mark protocol.ClientPlaybackMark) {
	m.mu.Lock()
	send := m.sendMark
	m.mu.Unlock()
	if send != nil {
		send(mark)
	}
}

func (m *playbackManager) emitErr(err error) {
	if err == nil || m == nil {
		return
	}
	select {
	case m.errCh <- err:
	default:
	}
}

func playedMSFromBytes(playedBytes int64, sampleRateHz, channels, bytesPerSample int) int64 {
	bytesPerSecond := int64(sampleRateHz * channels * bytesPerSample)
	if bytesPerSecond <= 0 || playedBytes <= 0 {
		return 0
	}
	return (playedBytes * 1000) / bytesPerSecond
}

func bufferedMSFromBytes(bufferedBytes int, sampleRateHz, channels, bytesPerSample int) int64 {
	bytesPerSecond := int64(sampleRateHz * channels * bytesPerSample)
	if bytesPerSecond <= 0 || bufferedBytes <= 0 {
		return 0
	}
	return (int64(bufferedBytes) * 1000) / bytesPerSecond
}

type ffplaySpeaker struct {
	path        string
	sampleRate  int
	channels    int
	logLevel    string
	volume      int
	debug       bool
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	runningMu   sync.Mutex
}

func newFFPlaySpeaker(path string, sampleRate, channels int, logLevel string, volume int, debug bool) *ffplaySpeaker {
	return &ffplaySpeaker{path: path, sampleRate: sampleRate, channels: channels, logLevel: logLevel, volume: volume, debug: debug}
}

func (s *ffplaySpeaker) EnsureRunning() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		return nil
	}
	return s.startLocked()
}

func (s *ffplaySpeaker) Start() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.startLocked()
}

func (s *ffplaySpeaker) startLocked() error {
	if s.cmd != nil && s.cmd.Process != nil {
		return nil
	}
	// ffplay does not accept ffmpeg-style `-ac` (channels); use `-ch_layout mono`.
	chLayout := "mono"
	if s.channels == 2 {
		chLayout = "stereo"
	}
	args := []string{
		"-hide_banner",
		"-loglevel", s.logLevel,
		"-nostats",
		"-volume", fmt.Sprintf("%d", s.volume),
		"-nodisp",
		"-f", "s16le",
		"-ch_layout", chLayout,
		"-ar", fmt.Sprintf("%d", s.sampleRate),
		// Read from stdin. (`pipe:0` should work too, but `-` is the most portable.)
		"-i", "-",
	}
	cmd := exec.Command(s.path, args...)
	if runtime.GOOS == "darwin" {
		// ffplay uses SDL for audio output on macOS. In some environments SDL can select a dummy
		// audio backend with no sound; prefer CoreAudio unless the user explicitly overrides it.
		if os.Getenv("SDL_AUDIODRIVER") == "" {
			cmd.Env = append(os.Environ(), "SDL_AUDIODRIVER=coreaudio")
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	if s.debug && cmd.Process != nil {
		fmt.Fprintf(os.Stderr, "[debug] ffplay started pid=%d (%s %s)\n", cmd.Process.Pid, s.path, strings.Join(args, " "))
		if runtime.GOOS == "darwin" && os.Getenv("SDL_AUDIODRIVER") == "" {
			fmt.Fprintln(os.Stderr, "[debug] ffplay env: SDL_AUDIODRIVER=coreaudio")
		}
	}
	s.cmd = cmd
	s.stdin = stdin
	go func(c *exec.Cmd) {
		_ = c.Wait()
		s.runningMu.Lock()
		if s.cmd == c {
			s.cmd = nil
			s.stdin = nil
		}
		s.runningMu.Unlock()
	}(cmd)
	return nil
}

func (s *ffplaySpeaker) Write(p []byte) error {
	if s == nil || len(p) == 0 {
		return nil
	}
	s.runningMu.Lock()
	stdin := s.stdin
	s.runningMu.Unlock()
	if stdin == nil {
		return fmt.Errorf("ffplay is not running")
	}
	_, err := stdin.Write(p)
	return err
}

func (s *ffplaySpeaker) Restart() error {
	if s == nil {
		return nil
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	_ = s.closeLocked()
	return s.startLocked()
}

func (s *ffplaySpeaker) Close() error {
	if s == nil {
		return nil
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	return s.closeLocked()
}

func (s *ffplaySpeaker) closeLocked() error {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.cmd = nil
	s.stdin = nil
	return nil
}

func sineTonePCM16LE(freqHz int, sampleRateHz int, d time.Duration, amp float64) []byte {
	if sampleRateHz <= 0 || d <= 0 || freqHz <= 0 {
		return nil
	}
	if amp <= 0 {
		amp = 0.2
	}
	if amp > 1.0 {
		amp = 1.0
	}
	samples := int(float64(sampleRateHz) * d.Seconds())
	if samples <= 0 {
		samples = 1
	}
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		t := float64(i) / float64(sampleRateHz)
		v := amp * math.Sin(2*math.Pi*float64(freqHz)*t)
		s := int16(v * 32767.0)
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	return out
}
