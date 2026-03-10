package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	assetsvc "github.com/vango-go/vai-lite/pkg/gateway/assets"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
	"github.com/vango-go/vai-lite/pkg/gateway/principal"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
)

const (
	chainWSSubprotocol  = "vai.chain.v1"
	chainWSQueueLimit   = 256
	chainWSQueueBytes   = 1 << 20
	chainWSPingInterval = 25 * time.Second
)

type ChainWSHandler struct {
	Config     config.Config
	Upstreams  ProviderFactory
	HTTPClient *http.Client
	Logger     *slog.Logger
	Limiter    *ratelimit.Limiter
	Lifecycle  *lifecycle.Lifecycle
	Chains     *chainrt.Manager
	Assets     *assetsvc.Service
}

func (h ChainWSHandler) getConfig() config.Config      { return h.Config }
func (h ChainWSHandler) getHTTPClient() *http.Client   { return h.HTTPClient }
func (h ChainWSHandler) getUpstreams() ProviderFactory { return h.Upstreams }
func (h ChainWSHandler) getAssets() *assetsvc.Service  { return h.Assets }

func (h ChainWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := requestID(r)
	if r.Method != http.MethodGet {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: reqID}, http.StatusMethodNotAllowed)
		return
	}
	if h.Chains == nil {
		writeCoreErrorJSON(w, reqID, core.NewInvalidRequestError("chain runtime is not configured"), http.StatusServiceUnavailable)
		return
	}
	if h.Lifecycle != nil && h.Lifecycle.IsDraining() {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrOverloaded, Message: "gateway is draining", Code: "draining", RequestID: reqID}, 529)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrInvalidRequest, Message: "websocket upgrade required", Code: "ws_upgrade_required", RequestID: reqID}, http.StatusBadRequest)
		return
	}
	if !liveOriginAllowed(h.Config, r) {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrPermission, Message: "origin is not allowed", Code: "origin_not_allowed", RequestID: reqID}, http.StatusForbidden)
		return
	}
	if !supportsChainSubprotocol(r) {
		writeCanonicalErrorJSON(w, types.NewCanonicalError(types.ErrorCodeProtocolUnsupportedCapability, "Sec-WebSocket-Protocol must include vai.chain.v1"))
		return
	}
	p := principal.Resolve(r, h.Config)
	if h.Limiter != nil && h.Config.WSMaxSessionsPerPrincipal > 0 {
		dec := h.Limiter.AcquireWSSession(p.Key, time.Now())
		if !dec.Allowed {
			if dec.RetryAfter > 0 {
				w.Header().Set("Retry-After", "1")
			}
			writeCanonicalErrorJSON(w, &types.CanonicalError{
				Code:            types.ErrorCodeQuotaLimitExceeded,
				Message:         "too many concurrent websocket sessions",
				RetryAfter:      intPtr(1),
				Retryable:       true,
				SuggestedAction: types.SuggestedActionRetryLater,
			})
			return
		}
		if dec.Permit != nil {
			defer dec.Permit.Release()
		}
	}
	upgrader := liveWSUpgrader
	upgrader.CheckOrigin = liveOriginCheck(h.Config)
	upgrader.Subprotocols = []string{chainWSSubprotocol}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	sessionCtx := r.Context()
	if h.Config.WSMaxSessionDuration > 0 {
		var cancel context.CancelFunc
		sessionCtx, cancel = context.WithTimeout(sessionCtx, h.Config.WSMaxSessionDuration)
		defer cancel()
	}
	session := newChainWSSession(conn, h.Logger)
	defer func() {
		chainID, attachmentID := session.attachmentState()
		if chainID != "" && attachmentID != "" {
			_ = h.Chains.Detach(context.Background(), chainID, attachmentID, "socket_closed")
		}
		session.close(websocket.CloseNormalClosure, "bye")
	}()
	go session.writerLoop(sessionCtx)

	_ = conn.SetReadDeadline(time.Now().Add(chainWSPingInterval * 2))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(chainWSPingInterval * 2))
	})

	pr := chainPrincipalFromRequest(r, h.Config)
	env := chainEnv(h, pr, r.Header, types.AttachmentModeTurnWS, chainWSSubprotocol, true, session.enqueueEvent)

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, context.Canceled) {
				return
			}
			session.sendChainError(chainErrorEvent(session.chainID(), "", err))
			return
		}
		if mt != websocket.TextMessage {
			session.closeWithCanonicalError(types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "binary websocket frames are not supported"), websocket.CloseUnsupportedData)
			return
		}
		frame, err := types.UnmarshalChainClientFrameStrict(data)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Error("chain websocket frame decode failed", "request_id", reqID, "error", err, "payload", string(data))
			}
			session.closeWithCanonicalError(types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, err.Error()), websocket.ClosePolicyViolation)
			return
		}
		if session.hasAttachment() && !h.Chains.OwnsAttachment(sessionCtx, session.chainID(), session.attachmentID()) {
			session.closeWithCanonicalError(types.NewCanonicalError(types.ErrorCodeChainAttachConflict, "attachment no longer owns the writer lease").WithChain(session.chainID()), websocket.ClosePolicyViolation)
			return
		}
		if err := h.dispatchChainFrame(sessionCtx, session, pr, env, frame); err != nil {
			if canonical, ok := err.(*types.CanonicalError); ok {
				if canonical.Code == types.ErrorCodeProtocolUnknownFrame {
					session.closeWithCanonicalError(canonical, websocket.ClosePolicyViolation)
					return
				}
				session.sendChainError(chainErrorEvent(session.chainID(), "", canonical))
				continue
			}
			session.sendChainError(chainErrorEvent(session.chainID(), "", err))
		}
	}
}

func (h ChainWSHandler) dispatchChainFrame(ctx context.Context, session *chainWSSession, principal chainrt.Principal, env chainrt.RuntimeEnvironment, frame types.ChainClientFrame) error {
	switch typed := frame.(type) {
	case types.ChainStartFrame:
		if session.chainID() != "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "socket is already attached to a chain")
		}
		event, attachmentID, err := h.Chains.StartChain(ctx, principal, env, typed.ChainStartPayload, typed.IdempotencyKey)
		if err != nil {
			return err
		}
		session.setAttachment(event.ChainID, attachmentID)
		if attachmentID == "" {
			return session.enqueueEvent(event)
		}
		return nil
	case types.ChainAttachFrame:
		if session.chainID() != "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "socket is already attached to a chain")
		}
		event, replay, attachmentID, err := h.Chains.AttachChain(ctx, principal, env, typed)
		if err != nil {
			return err
		}
		session.setAttachment(event.ChainID, attachmentID)
		for i := range replay {
			if enqueueErr := session.enqueueEvent(replay[i]); enqueueErr != nil {
				return enqueueErr
			}
		}
		return nil
	case types.ChainUpdateFrame:
		if session.chainID() == "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "chain.start or chain.attach is required before mutation")
		}
		_, err := h.Chains.UpdateChain(ctx, session.chainID(), env, typed.ChainUpdatePayload, typed.IdempotencyKey)
		return err
	case types.RunStartFrame:
		if session.chainID() == "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "chain.start or chain.attach is required before mutation")
		}
		_, err := h.Chains.StartRunAsync(ctx, session.chainID(), env, typed.RunStartPayload, typed.IdempotencyKey)
		return err
	case types.ClientToolResultFrame:
		if session.chainID() == "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "chain.start or chain.attach is required before mutation")
		}
		return h.Chains.SubmitClientToolResult(ctx, session.chainID(), typed)
	case types.RunCancelFrame:
		if session.chainID() == "" {
			return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "chain.start or chain.attach is required before mutation")
		}
		return h.Chains.CancelRun(ctx, session.chainID(), typed.RunID)
	case types.ChainCloseFrame:
		chainID, attachmentID := session.attachmentState()
		if chainID != "" && attachmentID != "" {
			_ = h.Chains.Detach(ctx, chainID, attachmentID, "client_closed")
		}
		session.clearAttachment()
		session.close(websocket.CloseNormalClosure, "chain closed")
		return nil
	default:
		return types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "unsupported chain frame")
	}
}

type chainWSSession struct {
	conn   *websocket.Conn
	logger *slog.Logger

	writeMu sync.Mutex
	sendCh  chan []byte

	mu          sync.Mutex
	queuedBytes int
	closed      bool
	chain       string
	attachment  string
}

func newChainWSSession(conn *websocket.Conn, logger *slog.Logger) *chainWSSession {
	return &chainWSSession{
		conn:   conn,
		logger: logger,
		sendCh: make(chan []byte, chainWSQueueLimit),
	}
}

func (s *chainWSSession) enqueueEvent(event types.ChainServerEvent) (err error) {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("websocket session is closed")
	}
	if len(s.sendCh) >= chainWSQueueLimit || s.queuedBytes+len(data) > chainWSQueueBytes {
		s.mu.Unlock()
		s.closeWithCanonicalError(types.NewCanonicalError(types.ErrorCodeTransportBackpressureExceeded, "outbound websocket backpressure exceeded"), websocket.ClosePolicyViolation)
		return types.NewCanonicalError(types.ErrorCodeTransportBackpressureExceeded, "outbound websocket backpressure exceeded")
	}
	s.queuedBytes += len(data)
	s.mu.Unlock()
	defer func() {
		if recover() != nil {
			s.mu.Lock()
			s.queuedBytes -= len(data)
			s.mu.Unlock()
			err = errors.New("websocket session is closed")
		}
	}()
	select {
	case s.sendCh <- data:
		return nil
	default:
		s.mu.Lock()
		s.queuedBytes -= len(data)
		s.mu.Unlock()
		s.closeWithCanonicalError(types.NewCanonicalError(types.ErrorCodeTransportBackpressureExceeded, "outbound websocket backpressure exceeded"), websocket.ClosePolicyViolation)
		return types.NewCanonicalError(types.ErrorCodeTransportBackpressureExceeded, "outbound websocket backpressure exceeded")
	}
}

func (s *chainWSSession) writerLoop(ctx context.Context) {
	ticker := time.NewTicker(chainWSPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.writeMu.Lock()
			err := s.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			s.writeMu.Unlock()
			if err != nil {
				return
			}
		case data, ok := <-s.sendCh:
			if !ok {
				return
			}
			s.writeMu.Lock()
			err := s.conn.WriteMessage(websocket.TextMessage, data)
			s.writeMu.Unlock()
			s.mu.Lock()
			s.queuedBytes -= len(data)
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (s *chainWSSession) closeWithCanonicalError(err *types.CanonicalError, closeCode int) {
	if err == nil {
		err = types.NewCanonicalError(types.ErrorCodeProtocolUnknownFrame, "connection closed")
	}
	event := chainErrorEvent(err.ChainID, err.RunID, err)
	data, _ := json.Marshal(event)
	s.closeInternal(closeCode, err.Message, data)
}

func (s *chainWSSession) sendChainError(event types.ChainErrorEvent) {
	_ = s.enqueueEvent(event)
}

func (s *chainWSSession) close(closeCode int, reason string) {
	s.closeInternal(closeCode, reason, nil)
}

func (s *chainWSSession) closeInternal(closeCode int, reason string, prelude []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.sendCh)
	s.mu.Unlock()

	s.writeMu.Lock()
	if len(prelude) > 0 {
		_ = s.conn.WriteMessage(websocket.TextMessage, prelude)
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, reason), time.Now().Add(5*time.Second))
	s.writeMu.Unlock()
	_ = s.conn.Close()
}

func (s *chainWSSession) setAttachment(chainID, attachmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain = strings.TrimSpace(chainID)
	s.attachment = strings.TrimSpace(attachmentID)
}

func (s *chainWSSession) clearAttachment() {
	s.setAttachment("", "")
}

func (s *chainWSSession) attachmentState() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chain, s.attachment
}

func (s *chainWSSession) chainID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chain
}

func (s *chainWSSession) attachmentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachment
}

func (s *chainWSSession) hasAttachment() bool {
	chainID, attachmentID := s.attachmentState()
	return chainID != "" && attachmentID != ""
}

func supportsChainSubprotocol(r *http.Request) bool {
	for _, proto := range websocket.Subprotocols(r) {
		if strings.TrimSpace(proto) == chainWSSubprotocol {
			return true
		}
	}
	return false
}

func intPtr(value int) *int {
	return &value
}
