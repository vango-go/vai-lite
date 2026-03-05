package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core/types"
	vai "github.com/vango-go/vai-lite/sdk"
)

func TestLiveWebsocketURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
		wantErr string
	}{
		{
			name:    "http converts to ws",
			baseURL: "http://127.0.0.1:8080",
			want:    "ws://127.0.0.1:8080/v1/live",
		},
		{
			name:    "https converts to wss",
			baseURL: "https://api.example.com/proxy",
			want:    "wss://api.example.com/proxy/v1/live",
		},
		{
			name:    "unsupported scheme",
			baseURL: "ftp://example.com",
			wantErr: "unsupported base-url scheme",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := liveWebsocketURL(tc.baseURL)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v, want contains %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("liveWebsocketURL error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("url=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildLiveWSHeaders_IncludesGatewayAndProviderKeys(t *testing.T) {
	cfg := chatConfig{
		GatewayAPIKey: "vai_sk_test",
		ProviderKeys: map[string]string{
			"openai":   "sk-openai",
			"cartesia": "sk-cartesia",
			"tavily":   "tvly",
		},
	}

	headers := buildLiveWSHeaders(cfg)
	if got := headers.Get("Authorization"); got != "Bearer vai_sk_test" {
		t.Fatalf("authorization=%q", got)
	}
	if got := headers.Get("X-Provider-Key-OpenAI"); got != "sk-openai" {
		t.Fatalf("openai header=%q", got)
	}
	if got := headers.Get("X-Provider-Key-Cartesia"); got != "sk-cartesia" {
		t.Fatalf("cartesia header=%q", got)
	}
	if got := headers.Get("X-Provider-Key-Tavily"); got != "tvly" {
		t.Fatalf("tavily header=%q", got)
	}
}

func TestPartitionLiveTools(t *testing.T) {
	talkTool := vai.MakeTool("talk_to_user", "talk", func(ctx context.Context, input struct {
		Content string `json:"content"`
	}) (string, error) {
		return "delivered", nil
	})
	localTool := vai.MakeTool("local_tool", "local", func(ctx context.Context, input struct {
		Value string `json:"value"`
	}) (string, error) {
		return input.Value, nil
	})

	requestTools, handlers, serverTools := partitionLiveTools([]vai.ToolWithHandler{
		vai.VAIWebSearch(vai.Tavily),
		vai.VAIWebFetch(vai.Tavily),
		talkTool,
		localTool,
	})

	if len(serverTools) != 2 {
		t.Fatalf("len(serverTools)=%d, want 2", len(serverTools))
	}
	if len(requestTools) != 1 {
		t.Fatalf("len(requestTools)=%d, want 1", len(requestTools))
	}
	if requestTools[0].Name != "local_tool" {
		t.Fatalf("requestTools[0].Name=%q, want local_tool", requestTools[0].Name)
	}
	if _, ok := handlers["local_tool"]; !ok {
		t.Fatalf("expected local_tool handler in map")
	}
	if _, ok := handlers["talk_to_user"]; ok {
		t.Fatalf("did not expect talk_to_user handler in map")
	}
}

func TestBuildLiveStartFrame_ConfiguresRunRequest(t *testing.T) {
	localTool := vai.MakeTool("local_tool", "local", func(ctx context.Context, input struct {
		Value string `json:"value"`
	}) (string, error) {
		return input.Value, nil
	})

	start, handlers := buildLiveStartFrame(chatConfig{
		MaxTokens:    321,
		SystemPrompt: "Be concise",
		VoiceID:      "voice-id",
	}, "oai-resp/gpt-5-mini", nil, 16000, []vai.ToolWithHandler{vai.VAIWebSearch(vai.Tavily), localTool})

	if start.Type != "start" {
		t.Fatalf("type=%q, want start", start.Type)
	}
	if start.RunRequest.Request.Model != "oai-resp/gpt-5-mini" {
		t.Fatalf("model=%q", start.RunRequest.Request.Model)
	}
	if start.RunRequest.Request.MaxTokens != 321 {
		t.Fatalf("max_tokens=%d, want 321", start.RunRequest.Request.MaxTokens)
	}
	if start.RunRequest.Request.Voice == nil || start.RunRequest.Request.Voice.Output == nil {
		t.Fatal("voice output not configured")
	}
	if start.RunRequest.Request.Voice.Output.SampleRate != 16000 {
		t.Fatalf("sample_rate=%d, want %d", start.RunRequest.Request.Voice.Output.SampleRate, 16000)
	}
	if len(start.RunRequest.ServerTools) != 1 || start.RunRequest.ServerTools[0] != "vai_web_search" {
		t.Fatalf("server_tools=%v, want [vai_web_search]", start.RunRequest.ServerTools)
	}
	if _, ok := start.RunRequest.ServerToolConfig["vai_web_search"]; !ok {
		t.Fatalf("missing server_tool_config for vai_web_search")
	}
	if len(start.RunRequest.Request.Tools) != 1 || start.RunRequest.Request.Tools[0].Name != "local_tool" {
		t.Fatalf("request.tools=%v", start.RunRequest.Request.Tools)
	}
	if _, ok := handlers["local_tool"]; !ok {
		t.Fatalf("missing local_tool handler")
	}
	if !strings.Contains(start.RunRequest.Request.System.(string), "talk_to_user") {
		t.Fatalf("system prompt missing talk_to_user instruction: %v", start.RunRequest.Request.System)
	}
}

func TestToolOutputToContentBlocks(t *testing.T) {
	blocks := toolOutputToContentBlocks("hello")
	if len(blocks) != 1 {
		t.Fatalf("len(blocks)=%d, want 1", len(blocks))
	}
	if tb, ok := blocks[0].(types.TextBlock); !ok || tb.Text != "hello" {
		t.Fatalf("unexpected block: %#v", blocks[0])
	}

	blocks = toolOutputToContentBlocks(map[string]any{"a": 1})
	if len(blocks) != 1 {
		t.Fatalf("len(blocks)=%d, want 1", len(blocks))
	}
	if tb, ok := blocks[0].(types.TextBlock); !ok || !strings.Contains(tb.Text, `"a":1`) {
		t.Fatalf("unexpected json block: %#v", blocks[0])
	}
}

func TestValidateModelForLive(t *testing.T) {
	cfg := chatConfig{ProviderKeys: map[string]string{"openai": "sk-openai"}}
	if err := validateModelForLive("oai-resp/gpt-5-mini", cfg); err != nil {
		t.Fatalf("expected oai-resp with openai key to pass, got %v", err)
	}

	cfg = chatConfig{ProviderKeys: map[string]string{}}
	err := validateModelForLive("oai-resp/gpt-5-mini", cfg)
	if err == nil || !strings.Contains(err.Error(), "missing provider key") {
		t.Fatalf("expected missing provider key error, got %v", err)
	}
}

func TestMaybeCloseFinishedLiveSession(t *testing.T) {
	session := &liveModeSession{
		done: make(chan struct{}),
		history: []vai.Message{{
			Role:    "assistant",
			Content: vai.Text("hello"),
		}},
		err: errors.New("boom"),
	}
	state := &chatRuntime{}
	var errOut bytes.Buffer
	close(session.done)

	active := session
	maybeCloseFinishedLiveSession(state, &active, &errOut)
	if active != nil {
		t.Fatalf("expected active session to be cleared")
	}
	if len(state.history) != 1 || state.history[0].TextContent() != "hello" {
		t.Fatalf("history not synced: %#v", state.history)
	}
	if !strings.Contains(errOut.String(), "live session ended: boom") {
		t.Fatalf("missing closed-session error output: %q", errOut.String())
	}
}

func TestLiveModeCommandHelpers(t *testing.T) {
	if !isLiveModeOnCommand(" /live ") {
		t.Fatal("expected /live to match")
	}
	if !isLiveModeOffCommand(" /LIVE OFF ") {
		t.Fatal("expected /live off to match")
	}
}

func TestRunChatbot_LiveModeToggleCommands(t *testing.T) {
	oldStartLiveMode := startLiveModeFunc
	t.Cleanup(func() { startLiveModeFunc = oldStartLiveMode })

	startCalls := 0
	startLiveModeFunc = func(ctx context.Context, cfg chatConfig, state *chatRuntime, tools []vai.ToolWithHandler, out io.Writer, errOut io.Writer) (*liveModeSession, error) {
		startCalls++
		return &liveModeSession{done: make(chan struct{})}, nil
	}

	cfg := chatConfig{
		BaseURL:      "http://127.0.0.1:8080",
		Model:        "oai-resp/gpt-5-mini",
		MaxTokens:    128,
		Timeout:      2 * time.Second,
		VoiceEnabled: true,
		VoiceID:      "voice-id",
		ProviderKeys: map[string]string{
			"openai":   "sk-openai",
			"tavily":   "tvly",
			"cartesia": "sk-cartesia",
		},
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	input := strings.NewReader("/live\n/live off\n/quit\n")

	if err := runChatbot(context.Background(), cfg, input, &out, &errOut); err != nil {
		t.Fatalf("runChatbot error: %v", err)
	}
	if startCalls != 1 {
		t.Fatalf("startCalls=%d, want 1", startCalls)
	}
	if !strings.Contains(out.String(), "Live mode active.") {
		t.Fatalf("missing live-mode activation output: %q", out.String())
	}
	if !strings.Contains(out.String(), "Live mode stopped.") {
		t.Fatalf("missing live-mode stop output: %q", out.String())
	}
	if !strings.Contains(out.String(), "bye") {
		t.Fatalf("missing exit output: %q", out.String())
	}
}

func TestLiveOutputRateCandidates(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		want   []int
	}{
		{
			name:   "auto",
			policy: "auto",
			want:   []int{16000, 8000},
		},
		{
			name:   "forced 16k",
			policy: "16000",
			want:   []int{16000},
		},
		{
			name:   "forced 8k",
			policy: "8000",
			want:   []int{8000},
		},
		{
			name:   "forced 24k",
			policy: "24000",
			want:   []int{24000},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := liveOutputRateCandidates(tc.policy)
			if len(got) != len(tc.want) {
				t.Fatalf("len(candidates)=%d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("candidates[%d]=%d, want %d", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLiveRequestedOutputSampleRate(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		want   int
	}{
		{
			name:   "auto picks 16k",
			policy: "auto",
			want:   16000,
		},
		{
			name:   "forced 8k",
			policy: "8000",
			want:   8000,
		},
		{
			name:   "forced 24k",
			policy: "24000",
			want:   24000,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := liveRequestedOutputSampleRate(tc.policy)
			if got != tc.want {
				t.Fatalf("requested rate=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestReadLiveSessionStarted_AcceptsNegotiatedOutputRate(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade websocket: %v", err)
		}
		defer conn.Close()
		if err := conn.WriteJSON(types.LiveSessionStartedEvent{
			Type:               "session_started",
			InputFormat:        "pcm_s16le",
			InputSampleRateHz:  16000,
			OutputFormat:       "pcm_s16le",
			OutputSampleRateHz: 16000,
			SilenceCommitMS:    600,
		}); err != nil {
			t.Fatalf("write session_started: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	started, err := readLiveSessionStarted(conn)
	if err != nil {
		t.Fatalf("readLiveSessionStarted error: %v", err)
	}
	if started.OutputSampleRateHz != 16000 {
		t.Fatalf("output sample_rate_hz=%d, want 16000", started.OutputSampleRateHz)
	}
}

func TestReadLiveSessionStarted_RejectsNonPositiveOutputRate(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade websocket: %v", err)
		}
		defer conn.Close()
		payload, _ := json.Marshal(types.LiveSessionStartedEvent{
			Type:               "session_started",
			InputFormat:        "pcm_s16le",
			InputSampleRateHz:  16000,
			OutputFormat:       "pcm_s16le",
			OutputSampleRateHz: 0,
			SilenceCommitMS:    600,
		})
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			t.Fatalf("write session_started: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	_, err = readLiveSessionStarted(conn)
	if err == nil || !strings.Contains(err.Error(), "unsupported live output sample rate") {
		t.Fatalf("expected output sample rate error, got %v", err)
	}
}

func TestHandleAudioChunk_NonFinalKeepsPlayerOpen(t *testing.T) {
	oldNew := newLivePCMPlayerFunc
	t.Cleanup(func() { newLivePCMPlayerFunc = oldNew })

	created := make(chan struct{}, 1)
	newLivePCMPlayerFunc = func(sampleRate int) (*pcmPlayer, error) {
		select {
		case created <- struct{}{}:
		default:
		}
		return &pcmPlayer{sampleRate: sampleRate}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	session := &liveModeSession{
		ctx:                        ctx,
		negotiatedOutputSampleRate: 24000,
		errOut:                     io.Discard,
		cancelledTurns:             make(map[string]struct{}),
		audioResetTurns:            make(map[string]struct{}),
	}
	session.audioQueueCond = sync.NewCond(&session.audioQueueMu)
	session.setActiveTurn("turn_1")

	session.wg.Add(1)
	go session.audioPlaybackLoop()

	session.handleAudioChunk(types.LiveAudioChunkEvent{
		Type:         "audio_chunk",
		TurnID:       "turn_1",
		Format:       "pcm_s16le",
		SampleRateHz: 24000,
		Audio:        base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}),
		IsFinal:      false,
	})

	select {
	case <-created:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for player creation")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.audioMu.Lock()
		playerOpen := session.player != nil
		session.audioMu.Unlock()
		if playerOpen && session.isTurnAudioOpen() {
			session.closeAudioQueue()
			session.wg.Wait()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected player to be opened by playback loop")
}

func TestHandleAudioChunk_FinalClosesTurnPlayer(t *testing.T) {
	oldClose := closePCMPlayerFunc
	oldNew := newLivePCMPlayerFunc
	t.Cleanup(func() {
		closePCMPlayerFunc = oldClose
		newLivePCMPlayerFunc = oldNew
	})

	closeCalls := 0
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	newLivePCMPlayerFunc = func(sampleRate int) (*pcmPlayer, error) {
		return &pcmPlayer{sampleRate: sampleRate}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	session := &liveModeSession{
		ctx:                        ctx,
		negotiatedOutputSampleRate: 24000,
		errOut:                     io.Discard,
		cancelledTurns:             make(map[string]struct{}),
		audioResetTurns:            make(map[string]struct{}),
	}
	session.audioQueueCond = sync.NewCond(&session.audioQueueMu)
	session.setActiveTurn("turn_1")

	session.wg.Add(1)
	go session.audioPlaybackLoop()

	session.handleAudioChunk(types.LiveAudioChunkEvent{
		Type:         "audio_chunk",
		TurnID:       "turn_1",
		Format:       "pcm_s16le",
		SampleRateHz: 24000,
		Audio:        base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
		IsFinal:      true,
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.audioMu.Lock()
		playerCleared := session.player == nil && session.playerRate == 0
		session.audioMu.Unlock()
		if playerCleared && !session.isTurnAudioOpen() && closeCalls == 1 {
			session.closeAudioQueue()
			session.wg.Wait()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected final chunk to close player (closeCalls=%d)", closeCalls)
}

func TestHandleServerEvent_TurnCompleteFinalizesOpenTurnAudio(t *testing.T) {
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() { closePCMPlayerFunc = oldClose })

	closeCalls := 0
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	session := &liveModeSession{
		out:    io.Discard,
		errOut: io.Discard,
	}
	session.audioMu.Lock()
	session.player = &pcmPlayer{}
	session.playerRate = 24000
	session.turnAudioOpen = true
	session.audioMu.Unlock()
	if err := session.handleServerEvent([]byte(`{"type":"turn_complete","stop_reason":"end_turn","history":[]}`)); err != nil {
		t.Fatalf("handleServerEvent error: %v", err)
	}

	if closeCalls != 1 {
		t.Fatalf("closeCalls=%d, want 1", closeCalls)
	}
	session.audioMu.Lock()
	player := session.player
	session.audioMu.Unlock()
	if player != nil {
		t.Fatal("expected player to be cleared")
	}
	if session.isTurnAudioOpen() {
		t.Fatal("expected turnAudioOpen=false")
	}
}

func TestHandleServerEvent_AudioUnavailableFinalizesOpenTurnAudio(t *testing.T) {
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() { closePCMPlayerFunc = oldClose })

	closeCalls := 0
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	session := &liveModeSession{
		out:    io.Discard,
		errOut: io.Discard,
	}
	session.audioMu.Lock()
	session.player = &pcmPlayer{}
	session.playerRate = 24000
	session.turnAudioOpen = true
	session.audioMu.Unlock()
	if err := session.handleServerEvent([]byte(`{"type":"audio_unavailable","reason":"tts_failed","message":"boom"}`)); err != nil {
		t.Fatalf("handleServerEvent error: %v", err)
	}

	if closeCalls != 1 {
		t.Fatalf("closeCalls=%d, want 1", closeCalls)
	}
	session.audioMu.Lock()
	player := session.player
	session.audioMu.Unlock()
	if player != nil {
		t.Fatal("expected player to be cleared")
	}
	if session.isTurnAudioOpen() {
		t.Fatal("expected turnAudioOpen=false")
	}
}

func TestHandleServerEvent_UserTurnCommittedFinalizesOpenTurnAudio(t *testing.T) {
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() { closePCMPlayerFunc = oldClose })

	closeCalls := 0
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	session := &liveModeSession{
		out:    io.Discard,
		errOut: io.Discard,
	}
	session.audioMu.Lock()
	session.player = &pcmPlayer{}
	session.playerRate = 24000
	session.turnAudioOpen = true
	session.audioMu.Unlock()
	if err := session.handleServerEvent([]byte(`{"type":"user_turn_committed","audio_bytes":1234}`)); err != nil {
		t.Fatalf("handleServerEvent error: %v", err)
	}

	if closeCalls != 1 {
		t.Fatalf("closeCalls=%d, want 1", closeCalls)
	}
	session.audioMu.Lock()
	player := session.player
	session.audioMu.Unlock()
	if player != nil {
		t.Fatal("expected player to be cleared")
	}
	if session.isTurnAudioOpen() {
		t.Fatal("expected turnAudioOpen=false")
	}
}

func TestHandleServerEvent_TurnCancelledFinalizesAndIgnoresLateDeltas(t *testing.T) {
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() { closePCMPlayerFunc = oldClose })

	closeCalls := 0
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	var out bytes.Buffer
	session := &liveModeSession{
		out:            &out,
		errOut:         io.Discard,
		cancelledTurns: make(map[string]struct{}),
	}
	session.audioMu.Lock()
	session.player = &pcmPlayer{}
	session.playerRate = 24000
	session.turnAudioOpen = true
	session.audioMu.Unlock()
	session.setActiveTurn("turn_1")

	if err := session.handleServerEvent([]byte(`{"type":"turn_cancelled","turn_id":"turn_1","reason":"grace_period"}`)); err != nil {
		t.Fatalf("handleServerEvent(turn_cancelled) error: %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("closeCalls=%d, want 1", closeCalls)
	}
	if session.isTurnAudioOpen() {
		t.Fatal("expected turnAudioOpen=false")
	}

	if err := session.handleServerEvent([]byte(`{"type":"assistant_text_delta","turn_id":"turn_1","text":"late text"}`)); err != nil {
		t.Fatalf("handleServerEvent(assistant_text_delta) error: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("expected cancelled turn delta to be ignored, out=%q", out.String())
	}
}

func TestHandleServerEvent_IgnoresOlderTurnDeltas(t *testing.T) {
	var out bytes.Buffer
	session := &liveModeSession{
		out:            &out,
		errOut:         io.Discard,
		cancelledTurns: make(map[string]struct{}),
	}
	session.setActiveTurn("turn_2")

	if err := session.handleServerEvent([]byte(`{"type":"assistant_text_delta","turn_id":"turn_1","text":"old"}`)); err != nil {
		t.Fatalf("handleServerEvent(old turn) error: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("expected stale turn delta to be ignored, out=%q", out.String())
	}

	if err := session.handleServerEvent([]byte(`{"type":"assistant_text_delta","turn_id":"turn_2","text":"new"}`)); err != nil {
		t.Fatalf("handleServerEvent(active turn) error: %v", err)
	}
	if !strings.Contains(out.String(), "assistant: new") {
		t.Fatalf("expected active turn delta to render, out=%q", out.String())
	}
}

func TestFinalizeTurnAudio_SendsPlaybackStopped(t *testing.T) {
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() { closePCMPlayerFunc = oldClose })
	closePCMPlayerFunc = func(p *pcmPlayer) error { return nil }

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 2)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade websocket: %w", err)
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				serverErr <- fmt.Errorf("read websocket frame: %w", err)
				return
			}
			received <- payload
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	session := &liveModeSession{
		ctx:             context.Background(),
		conn:            conn,
		player:          &pcmPlayer{},
		turnAudioOpen:   true,
		out:             io.Discard,
		errOut:          io.Discard,
		cancelledTurns:  make(map[string]struct{}),
		audioResetTurns: make(map[string]struct{}),
	}
	session.setAudioTurn("turn_42")
	session.playbackMarkTurnID = "turn_42"
	session.playbackMarkRateHz = 16000
	session.playbackMarkStart = time.Now().Add(-500 * time.Millisecond)
	session.playbackMarkBytesPCM = int64(16000 * 2) // 1s buffered
	session.playbackMarkLastSent = -1
	session.finalizeTurnAudio("test close", "stopped")

	var mark types.LivePlaybackMarkFrame
	select {
	case err := <-serverErr:
		t.Fatalf("server error: %v", err)
	case payload := <-received:
		if err := json.Unmarshal(payload, &mark); err != nil {
			t.Fatalf("decode playback_mark frame: %v", err)
		}
		if mark.Type != "playback_mark" {
			t.Fatalf("type=%q, want playback_mark", mark.Type)
		}
		if mark.TurnID != "turn_42" {
			t.Fatalf("turn_id=%q, want turn_42", mark.TurnID)
		}
		if mark.PlayedMS < 0 {
			t.Fatalf("played_ms=%d, want >= 0", mark.PlayedMS)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for playback_mark frame")
	}

	select {
	case err := <-serverErr:
		t.Fatalf("server error: %v", err)
	case payload := <-received:
		var frame types.LivePlaybackStateFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatalf("decode playback_state frame: %v", err)
		}
		if frame.Type != "playback_state" {
			t.Fatalf("type=%q, want playback_state", frame.Type)
		}
		if frame.TurnID != "turn_42" {
			t.Fatalf("turn_id=%q, want turn_42", frame.TurnID)
		}
		if frame.State != "stopped" {
			t.Fatalf("state=%q, want stopped", frame.State)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for playback_state frame")
	}
}

func TestHandleServerEvent_AudioResetKillsPlayerAndAllowsTurnComplete(t *testing.T) {
	oldKill := killPCMPlayerFunc
	oldClose := closePCMPlayerFunc
	t.Cleanup(func() {
		killPCMPlayerFunc = oldKill
		closePCMPlayerFunc = oldClose
	})

	killCalls := 0
	closeCalls := 0
	killPCMPlayerFunc = func(p *pcmPlayer) error {
		killCalls++
		return nil
	}
	closePCMPlayerFunc = func(p *pcmPlayer) error {
		closeCalls++
		return nil
	}

	session := &liveModeSession{
		ctx:             context.Background(),
		out:             io.Discard,
		errOut:          io.Discard,
		cancelledTurns:  make(map[string]struct{}),
		audioResetTurns: make(map[string]struct{}),
	}
	session.audioMu.Lock()
	session.player = &pcmPlayer{}
	session.playerRate = 24000
	session.turnAudioOpen = true
	session.audioMu.Unlock()
	session.setActiveTurn("turn_1")
	session.setAudioTurn("turn_1")

	if err := session.handleServerEvent([]byte(`{"type":"audio_reset","turn_id":"turn_1","reason":"barge_in"}`)); err != nil {
		t.Fatalf("handleServerEvent(audio_reset) error: %v", err)
	}
	if killCalls != 1 {
		t.Fatalf("killCalls=%d, want 1", killCalls)
	}
	if closeCalls != 0 {
		t.Fatalf("closeCalls=%d, want 0", closeCalls)
	}
	if session.isTurnAudioOpen() {
		t.Fatal("expected turnAudioOpen=false after audio_reset")
	}
	session.audioMu.Lock()
	player := session.player
	session.audioMu.Unlock()
	if player != nil {
		t.Fatal("expected player to be cleared after audio_reset")
	}

	// Late audio chunks for the reset turn must be ignored.
	if err := session.handleServerEvent([]byte(`{"type":"audio_chunk","turn_id":"turn_1","format":"pcm_s16le","sample_rate_hz":24000,"audio":"AQI=","is_final":false}`)); err != nil {
		t.Fatalf("handleServerEvent(audio_chunk) error: %v", err)
	}
	if killCalls != 1 {
		t.Fatalf("killCalls=%d, want 1 after late chunk", killCalls)
	}

	if err := session.handleServerEvent([]byte(`{"type":"turn_complete","turn_id":"turn_1","stop_reason":"cancelled","history":[{"role":"assistant","content":[{"type":"text","text":"ok"}]}]}`)); err != nil {
		t.Fatalf("handleServerEvent(turn_complete) error: %v", err)
	}
	history := session.HistorySnapshot()
	if len(history) != 1 || history[0].TextContent() != "ok" {
		t.Fatalf("history not updated after turn_complete: %#v", history)
	}
}

func TestPlaybackMarkLoop_SendsMonotonicMarks(t *testing.T) {
	oldInterval := livePlaybackMarkInterval
	oldMargin := livePlaybackMarkSafetyMargin
	t.Cleanup(func() {
		livePlaybackMarkInterval = oldInterval
		livePlaybackMarkSafetyMargin = oldMargin
	})
	livePlaybackMarkInterval = 10 * time.Millisecond
	livePlaybackMarkSafetyMargin = 0

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 8)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- fmt.Errorf("upgrade websocket: %w", err)
			return
		}
		defer conn.Close()
		for {
			if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				serverErr <- fmt.Errorf("set read deadline: %w", err)
				return
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				// Most likely the client closed the connection; treat as graceful.
				return
			}
			received <- payload
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := &liveModeSession{
		ctx:             ctx,
		conn:            conn,
		out:             io.Discard,
		errOut:          io.Discard,
		cancelledTurns:  make(map[string]struct{}),
		audioResetTurns: make(map[string]struct{}),
	}

	session.ensurePlaybackMarkLoop("turn_1", 16000)
	session.addPlaybackBytes("turn_1", 16000, int64(16000*2)) // 1s of PCM buffered

	var marks []types.LivePlaybackMarkFrame
	for len(marks) < 2 {
		select {
		case err := <-serverErr:
			t.Fatalf("server error: %v", err)
		case payload := <-received:
			var mark types.LivePlaybackMarkFrame
			if err := json.Unmarshal(payload, &mark); err != nil {
				t.Fatalf("decode playback_mark frame: %v", err)
			}
			if mark.Type != "playback_mark" {
				t.Fatalf("type=%q, want playback_mark", mark.Type)
			}
			if mark.TurnID != "turn_1" {
				t.Fatalf("turn_id=%q, want turn_1", mark.TurnID)
			}
			marks = append(marks, mark)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for playback_mark frames")
		}
	}
	if marks[1].PlayedMS <= marks[0].PlayedMS {
		t.Fatalf("played_ms not monotonic: %d then %d", marks[0].PlayedMS, marks[1].PlayedMS)
	}

	session.stopPlaybackMarkLoop("turn_1")
}
