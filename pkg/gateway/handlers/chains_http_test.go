package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/auth"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
)

func TestChainsHandler_CreateRunAndReadHistory(t *testing.T) {
	h := ChainsHandler{
		Config:     baseRunsConfig(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/chains", bytes.NewReader([]byte(`{
		"external_session_id":"sess_ext_1",
		"defaults":{"model":"anthropic/test"},
		"history":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
	}`)))
	createReq.Header.Set(idempotencyKeyHeader, "chain_create_test")
	createReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	var started types.ChainStartedEvent
	if err := json.Unmarshal(createRR.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if started.ChainID == "" {
		t.Fatal("expected chain_id")
	}

	runReq := httptest.NewRequest(http.MethodPost, "/v1/chains/"+started.ChainID+"/runs", bytes.NewReader([]byte(`{
		"input":[{"role":"user","content":[{"type":"text","text":"say hi"}]}]
	}`)))
	runReq.Header.Set(idempotencyKeyHeader, "chain_run_test")
	runReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	runRR := httptest.NewRecorder()
	h.ServeHTTP(runRR, runReq)
	if runRR.Code != http.StatusOK {
		t.Fatalf("run status=%d body=%s", runRR.Code, runRR.Body.String())
	}
	var runEnvelope struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
		Result struct {
			Response struct {
				Content []json.RawMessage `json:"content"`
			} `json:"response"`
		} `json:"result"`
	}
	if err := json.Unmarshal(runRR.Body.Bytes(), &runEnvelope); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runEnvelope.Run.ID == "" {
		t.Fatalf("run envelope=%+v", runEnvelope)
	}
	if len(runEnvelope.Result.Response.Content) != 1 {
		t.Fatalf("unexpected run content: %+v", runEnvelope.Result.Response.Content)
	}
	var textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(runEnvelope.Result.Response.Content[0], &textBlock); err != nil {
		t.Fatalf("decode response content: %v", err)
	}
	if strings.TrimSpace(textBlock.Text) != "ok" {
		t.Fatalf("unexpected run result text: %+v", textBlock)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/chains/"+started.ChainID+"/runs", nil)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list runs status=%d body=%s", listRR.Code, listRR.Body.String())
	}
	var runList types.ChainRunList
	if err := json.Unmarshal(listRR.Body.Bytes(), &runList); err != nil {
		t.Fatalf("decode run list: %v", err)
	}
	if len(runList.Items) != 1 {
		t.Fatalf("len(run list)=%d, want 1", len(runList.Items))
	}

	readHandler := ChainRunsReadHandler{Chains: h.Chains}
	timelineReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+runEnvelope.Run.ID+"/timeline", nil)
	timelineRR := httptest.NewRecorder()
	readHandler.ServeHTTP(timelineRR, timelineReq)
	if timelineRR.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", timelineRR.Code, timelineRR.Body.String())
	}
	var timeline types.RunTimelineResponse
	if err := json.Unmarshal(timelineRR.Body.Bytes(), &timeline); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(timeline.Items) == 0 {
		t.Fatal("expected timeline items")
	}
}

func TestChainWSHandler_AllowsSequentialRunsOnOneSocket(t *testing.T) {
	h := ChainWSHandler{
		Config:     configForChainWS(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", h)
	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/chains/ws"
	headers := http.Header{
		"Sec-WebSocket-Protocol":   []string{chainWSSubprotocol},
		"X-Provider-Key-Anthropic": []string{"sk-test"},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if err := conn.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_ws",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn).(types.ChainStartedEvent)
	if started.ChainID == "" {
		t.Fatal("expected chain_id")
	}

	for i := 0; i < 2; i++ {
		if err := conn.WriteJSON(types.RunStartFrame{
			Type:           "run.start",
			IdempotencyKey: "run_start_ws_" + string(rune('a'+i)),
			RunStartPayload: types.RunStartPayload{
				Input: []types.Message{{Role: "user", Content: []types.ContentBlock{types.TextBlock{Type: "text", Text: "hello"}}}},
			},
		}); err != nil {
			t.Fatalf("write run.start %d: %v", i, err)
		}
		if !readUntilRunComplete(t, conn) {
			t.Fatalf("expected run_complete for run %d", i)
		}
	}
}

func TestChainWSHandler_AttachConflictReturnsCanonicalError(t *testing.T) {
	h := ChainWSHandler{
		Config:     configForChainWS(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", h)
	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/chains/ws"
	headers := http.Header{
		"Sec-WebSocket-Protocol":   []string{chainWSSubprotocol},
		"X-Provider-Key-Anthropic": []string{"sk-test"},
	}
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket 1: %v", err)
	}
	defer conn1.Close()
	_ = conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn1.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_conflict",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn1).(types.ChainStartedEvent)

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket 2: %v", err)
	}
	defer conn2.Close()
	_ = conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn2.WriteJSON(types.ChainAttachFrame{
		Type:           "chain.attach",
		IdempotencyKey: "chain_attach_conflict",
		ChainID:        started.ChainID,
		ResumeToken:    started.ResumeToken,
	}); err != nil {
		t.Fatalf("write chain.attach: %v", err)
	}
	event := mustReadChainEvent(t, conn2)
	errEvent, ok := event.(types.ChainErrorEvent)
	if !ok {
		t.Fatalf("event=%T, want ChainErrorEvent", event)
	}
	if errEvent.Code != types.ErrorCodeChainAttachConflict {
		t.Fatalf("code=%q", errEvent.Code)
	}
}

func TestChainWSHandler_AttachRejectsCrossOrgEvenWithResumeToken(t *testing.T) {
	const (
		ownerKey    = "vai_sk_owner_attach"
		attackerKey = "vai_sk_attacker_attach"
	)

	cfg := configForChainWS()
	cfg.AuthMode = config.AuthModeRequired
	cfg.APIKeys = map[string]struct{}{
		ownerKey:    {},
		attackerKey: {},
	}
	h := ChainWSHandler{
		Config:     cfg,
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", mw.Auth(cfg, h))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn1 := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, ""))
	defer conn1.Close()
	if err := conn1.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_cross_org",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn1).(types.ChainStartedEvent)

	conn2 := dialChainWSConn(t, server.URL, chainWSHeaders(attackerKey, ""))
	defer conn2.Close()
	if err := conn2.WriteJSON(types.ChainAttachFrame{
		Type:           "chain.attach",
		IdempotencyKey: "chain_attach_cross_org",
		ChainID:        started.ChainID,
		ResumeToken:    started.ResumeToken,
		Takeover:       true,
	}); err != nil {
		t.Fatalf("write chain.attach: %v", err)
	}
	errEvent := mustReadChainEvent(t, conn2).(types.ChainErrorEvent)
	if errEvent.Code != types.ErrorCodeAuthResumeTokenInvalid {
		t.Fatalf("code=%q, want %q", errEvent.Code, types.ErrorCodeAuthResumeTokenInvalid)
	}
}

func TestChainWSHandler_AttachRejectsMissingAndWrongActor(t *testing.T) {
	const ownerKey = "vai_sk_owner_actor"

	cfg := configForChainWS()
	cfg.AuthMode = config.AuthModeRequired
	cfg.APIKeys = map[string]struct{}{ownerKey: {}}
	h := ChainWSHandler{
		Config:     cfg,
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", mw.Auth(cfg, h))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn1 := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, "user_123"))
	defer conn1.Close()
	if err := conn1.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_actor_scope",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn1).(types.ChainStartedEvent)

	for _, tc := range []struct {
		name    string
		actorID string
	}{
		{name: "missing_actor", actorID: ""},
		{name: "wrong_actor", actorID: "user_456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, tc.actorID))
			defer conn.Close()
			if err := conn.WriteJSON(types.ChainAttachFrame{
				Type:           "chain.attach",
				IdempotencyKey: "chain_attach_" + tc.name,
				ChainID:        started.ChainID,
				ResumeToken:    started.ResumeToken,
				Takeover:       true,
			}); err != nil {
				t.Fatalf("write chain.attach: %v", err)
			}
			errEvent := mustReadChainEvent(t, conn).(types.ChainErrorEvent)
			if errEvent.Code != types.ErrorCodeAuthActorScopeDenied {
				t.Fatalf("code=%q, want %q", errEvent.Code, types.ErrorCodeAuthActorScopeDenied)
			}
		})
	}
}

func TestChainWSHandler_TakeoverWithSameActorRotatesResumeToken(t *testing.T) {
	const ownerKey = "vai_sk_owner_takeover"

	cfg := configForChainWS()
	cfg.AuthMode = config.AuthModeRequired
	cfg.APIKeys = map[string]struct{}{ownerKey: {}}
	h := ChainWSHandler{
		Config:     cfg,
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", mw.Auth(cfg, h))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn1 := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, "user_123"))
	defer conn1.Close()
	if err := conn1.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_takeover",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn1).(types.ChainStartedEvent)

	conn2 := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, "user_123"))
	defer conn2.Close()
	if err := conn2.WriteJSON(types.ChainAttachFrame{
		Type:           "chain.attach",
		IdempotencyKey: "chain_attach_takeover",
		ChainID:        started.ChainID,
		ResumeToken:    started.ResumeToken,
		Takeover:       true,
	}); err != nil {
		t.Fatalf("write chain.attach: %v", err)
	}
	attached := mustReadChainEvent(t, conn2).(types.ChainAttachedEvent)
	if attached.ActorID != "user_123" {
		t.Fatalf("actor_id=%q, want %q", attached.ActorID, "user_123")
	}
	if !strings.HasPrefix(attached.ResumeToken, "chain_rt_") {
		t.Fatalf("resume_token=%q, want chain_rt_*", attached.ResumeToken)
	}
	if attached.ResumeToken == started.ResumeToken {
		t.Fatalf("resume_token=%q, want rotation from %q", attached.ResumeToken, started.ResumeToken)
	}
}

func TestChainsHandler_PatchChainRejectsWrongOrgWithoutLeakingDefaults(t *testing.T) {
	const (
		ownerKey    = "vai_sk_owner_patch"
		attackerKey = "vai_sk_attacker_patch"
	)

	h := ChainsHandler{
		Config:     baseRunsConfig(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/chains", bytes.NewReader([]byte(`{
		"defaults":{"model":"anthropic/test","system":"owner secret"},
		"history":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
	}`)))
	createReq = requestWithAPIKey(createReq, ownerKey)
	createReq.Header.Set(idempotencyKeyHeader, "chain_create_patch_security")
	createReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	var started types.ChainStartedEvent
	if err := json.Unmarshal(createRR.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/v1/chains/"+started.ChainID, bytes.NewReader([]byte(`{
		"defaults":{"system":"attacker overwrite"}
	}`)))
	patchReq = requestWithAPIKey(patchReq, attackerKey)
	patchReq.Header.Set(idempotencyKeyHeader, "chain_patch_wrong_org")
	patchRR := httptest.NewRecorder()
	h.ServeHTTP(patchRR, patchReq)
	if patchRR.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", patchRR.Code, patchRR.Body.String())
	}
	var envelope types.CanonicalErrorEnvelope
	if err := json.Unmarshal(patchRR.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != types.ErrorCodeAuthChainAccessDenied {
		t.Fatalf("error=%+v, want %q", envelope.Error, types.ErrorCodeAuthChainAccessDenied)
	}
	if strings.Contains(patchRR.Body.String(), "owner secret") || strings.Contains(patchRR.Body.String(), "anthropic/test") {
		t.Fatalf("unauthorized response leaked chain defaults: %s", patchRR.Body.String())
	}

	record, err := h.Chains.GetChain(context.Background(), started.ChainID)
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if got, _ := record.Defaults.System.(string); got != "owner secret" {
		t.Fatalf("defaults.system=%q, want %q", got, "owner secret")
	}
}

func TestChainsHandler_PatchChainAllowsOwnerOnIdleChain(t *testing.T) {
	const ownerKey = "vai_sk_owner_patch_idle"

	h := ChainsHandler{
		Config:     baseRunsConfig(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/chains", bytes.NewReader([]byte(`{
		"defaults":{"model":"anthropic/test","system":"before"}
	}`)))
	createReq = requestWithAPIKey(createReq, ownerKey)
	createReq.Header.Set(idempotencyKeyHeader, "chain_create_patch_idle")
	createReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	var started types.ChainStartedEvent
	if err := json.Unmarshal(createRR.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/v1/chains/"+started.ChainID, bytes.NewReader([]byte(`{
		"defaults":{"system":"after"}
	}`)))
	patchReq = requestWithAPIKey(patchReq, ownerKey)
	patchReq.Header.Set(idempotencyKeyHeader, "chain_patch_owner_idle")
	patchRR := httptest.NewRecorder()
	h.ServeHTTP(patchRR, patchReq)
	if patchRR.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRR.Code, patchRR.Body.String())
	}
	var updated types.ChainUpdatedEvent
	if err := json.Unmarshal(patchRR.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if got, _ := updated.Defaults.System.(string); got != "after" {
		t.Fatalf("updated defaults.system=%q, want %q", got, "after")
	}
}

func TestChainsHandler_PatchChainReturnsAttachConflictWhenWriterActive(t *testing.T) {
	const ownerKey = "vai_sk_owner_patch_conflict"

	manager := chainrt.NewManager(nil, chainrt.DefaultManagerConfig())
	h := ChainsHandler{
		Config:     baseRunsConfig(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     manager,
	}

	cfg := configForChainWS()
	cfg.AuthMode = config.AuthModeRequired
	cfg.APIKeys = map[string]struct{}{ownerKey: {}}
	wsHandler := ChainWSHandler{
		Config:     cfg,
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     manager,
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/chains/ws", mw.Auth(cfg, wsHandler))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn := dialChainWSConn(t, server.URL, chainWSHeaders(ownerKey, ""))
	defer conn.Close()
	if err := conn.WriteJSON(types.ChainStartFrame{
		Type:           "chain.start",
		IdempotencyKey: "chain_start_patch_conflict",
		ChainStartPayload: types.ChainStartPayload{
			Defaults: types.ChainDefaults{Model: "anthropic/test"},
		},
	}); err != nil {
		t.Fatalf("write chain.start: %v", err)
	}
	started := mustReadChainEvent(t, conn).(types.ChainStartedEvent)

	patchReq := httptest.NewRequest(http.MethodPatch, "/v1/chains/"+started.ChainID, bytes.NewReader([]byte(`{
		"defaults":{"system":"after"}
	}`)))
	patchReq = requestWithAPIKey(patchReq, ownerKey)
	patchReq.Header.Set(idempotencyKeyHeader, "chain_patch_active_writer")
	patchRR := httptest.NewRecorder()
	h.ServeHTTP(patchRR, patchReq)
	if patchRR.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", patchRR.Code, patchRR.Body.String())
	}
	var envelope types.CanonicalErrorEnvelope
	if err := json.Unmarshal(patchRR.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != types.ErrorCodeChainAttachConflict {
		t.Fatalf("error=%+v, want %q", envelope.Error, types.ErrorCodeChainAttachConflict)
	}
}

func TestChainsHandler_RunStartRejectsWrongOrgWithChainAccessDenied(t *testing.T) {
	const (
		ownerKey    = "vai_sk_owner_run"
		attackerKey = "vai_sk_attacker_run"
	)

	h := ChainsHandler{
		Config:     baseRunsConfig(),
		Upstreams:  fakeFactory{p: &fakeRunProvider{streamEvents: chainTestStreamEvents("ok")}},
		HTTPClient: http.DefaultClient,
		Chains:     chainrt.NewManager(nil, chainrt.DefaultManagerConfig()),
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v1/chains", bytes.NewReader([]byte(`{
		"defaults":{"model":"anthropic/test"}
	}`)))
	createReq = requestWithAPIKey(createReq, ownerKey)
	createReq.Header.Set(idempotencyKeyHeader, "chain_create_run_authz")
	createReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createRR.Code, createRR.Body.String())
	}
	var started types.ChainStartedEvent
	if err := json.Unmarshal(createRR.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	runReq := httptest.NewRequest(http.MethodPost, "/v1/chains/"+started.ChainID+"/runs", bytes.NewReader([]byte(`{
		"input":[{"role":"user","content":[{"type":"text","text":"say hi"}]}]
	}`)))
	runReq = requestWithAPIKey(runReq, attackerKey)
	runReq.Header.Set(idempotencyKeyHeader, "chain_run_wrong_org")
	runReq.Header.Set("X-Provider-Key-Anthropic", "sk-test")
	runRR := httptest.NewRecorder()
	h.ServeHTTP(runRR, runReq)
	if runRR.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", runRR.Code, runRR.Body.String())
	}
	var envelope types.CanonicalErrorEnvelope
	if err := json.Unmarshal(runRR.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if envelope.Error == nil || envelope.Error.Code != types.ErrorCodeAuthChainAccessDenied {
		t.Fatalf("error=%+v, want %q", envelope.Error, types.ErrorCodeAuthChainAccessDenied)
	}
}

func configForChainWS() config.Config {
	cfg := baseRunsConfig()
	cfg.WSMaxSessionDuration = time.Minute
	cfg.WSMaxSessionsPerPrincipal = 2
	return cfg
}

func requestWithAPIKey(req *http.Request, apiKey string) *http.Request {
	return req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{APIKey: apiKey}))
}

func chainWSHeaders(apiKey, actorID string) http.Header {
	headers := http.Header{
		"Authorization":            []string{"Bearer " + apiKey},
		"Sec-WebSocket-Protocol":   []string{chainWSSubprotocol},
		"X-Provider-Key-Anthropic": []string{"sk-test"},
	}
	if strings.TrimSpace(actorID) != "" {
		headers.Set("X-VAI-Actor-ID", actorID)
	}
	return headers
}

func dialChainWSConn(t *testing.T, serverURL string, headers http.Header) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/v1/chains/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	return conn
}

func chainTestStreamEvents(text string) []types.StreamEvent {
	delta := types.MessageDeltaEvent{Type: "message_delta"}
	delta.Delta.StopReason = types.StopReasonEndTurn
	return []types.StreamEvent{
		types.MessageStartEvent{Type: "message_start", Message: types.MessageResponse{Type: "message", Role: "assistant", Model: "test"}},
		types.ContentBlockStartEvent{Type: "content_block_start", Index: 0, ContentBlock: types.TextBlock{Type: "text", Text: ""}},
		types.ContentBlockDeltaEvent{Type: "content_block_delta", Index: 0, Delta: types.TextDelta{Type: "text_delta", Text: text}},
		delta,
	}
}

func mustReadChainEvent(t *testing.T, conn *websocket.Conn) types.ChainServerEvent {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	event, err := types.UnmarshalChainServerEventStrict(data)
	if err != nil {
		t.Fatalf("decode chain event: %v", err)
	}
	return event
}

func readUntilRunComplete(t *testing.T, conn *websocket.Conn) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	for {
		event := mustReadChainEvent(t, conn)
		runEvent, ok := event.(types.RunEnvelopeEvent)
		if !ok {
			continue
		}
		if _, ok := runEvent.Event.(types.RunCompleteEvent); ok {
			return true
		}
	}
}
