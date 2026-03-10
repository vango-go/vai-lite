package types

import "time"

// ChainClientFrame is a typed client-to-server frame on the chain websocket.
type ChainClientFrame interface {
	ChainClientFrameType() string
}

// ChainServerEvent is a typed server-to-client event on the chain websocket.
type ChainServerEvent interface {
	ChainServerEventType() string
}

type ChainStartPayload struct {
	ExternalSessionID string         `json:"external_session_id,omitempty"`
	Defaults          ChainDefaults  `json:"defaults,omitempty"`
	History           []Message      `json:"history,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type ChainUpdatePayload struct {
	Defaults ChainDefaults `json:"defaults,omitempty"`
}

type RunStartPayload struct {
	Input     []Message      `json:"input"`
	Overrides *RunOverrides  `json:"overrides,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ChainStartFrame struct {
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
	ChainStartPayload
}

func (f ChainStartFrame) ChainClientFrameType() string { return "chain.start" }

type ChainAttachFrame struct {
	Type               string `json:"type"`
	IdempotencyKey     string `json:"idempotency_key"`
	ChainID            string `json:"chain_id"`
	ResumeToken        string `json:"resume_token"`
	AfterEventID       int64  `json:"after_event_id,omitempty"`
	RequireExactReplay bool   `json:"require_exact_replay,omitempty"`
	Takeover           bool   `json:"takeover,omitempty"`
}

func (f ChainAttachFrame) ChainClientFrameType() string { return "chain.attach" }

type ChainUpdateFrame struct {
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
	ChainUpdatePayload
}

func (f ChainUpdateFrame) ChainClientFrameType() string { return "chain.update" }

type RunStartFrame struct {
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
	RunStartPayload
}

func (f RunStartFrame) ChainClientFrameType() string { return "run.start" }

type ClientToolResultFrame struct {
	Type           string         `json:"type"`
	IdempotencyKey string         `json:"idempotency_key"`
	RunID          string         `json:"run_id"`
	ExecutionID    string         `json:"execution_id"`
	Content        []ContentBlock `json:"content,omitempty"`
	IsError        bool           `json:"is_error,omitempty"`
}

func (f ClientToolResultFrame) ChainClientFrameType() string { return "client_tool.result" }

type RunCancelFrame struct {
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
	RunID          string `json:"run_id"`
}

func (f RunCancelFrame) ChainClientFrameType() string { return "run.cancel" }

type ChainCloseFrame struct {
	Type           string `json:"type"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (f ChainCloseFrame) ChainClientFrameType() string { return "chain.close" }

type ChainStartedEvent struct {
	Type                   string        `json:"type"`
	EventID                int64         `json:"event_id"`
	ChainVersion           int64         `json:"chain_version"`
	ChainID                string        `json:"chain_id"`
	SessionID              string        `json:"session_id,omitempty"`
	ExternalSessionID      string        `json:"external_session_id,omitempty"`
	ResumeToken            string        `json:"resume_token"`
	ActorID                string        `json:"actor_id,omitempty"`
	AuthorizedProviders    []string      `json:"authorized_providers,omitempty"`
	AuthorizedGatewayTools []string      `json:"authorized_gateway_tools,omitempty"`
	Defaults               ChainDefaults `json:"defaults,omitempty"`
}

func (e ChainStartedEvent) ChainServerEventType() string { return "chain.started" }

type ChainAttachedEvent struct {
	Type                   string         `json:"type"`
	EventID                int64          `json:"event_id"`
	ChainVersion           int64          `json:"chain_version"`
	ChainID                string         `json:"chain_id"`
	SessionID              string         `json:"session_id,omitempty"`
	ResumeToken            string         `json:"resume_token"`
	ActorID                string         `json:"actor_id,omitempty"`
	AuthorizedProviders    []string       `json:"authorized_providers,omitempty"`
	AuthorizedGatewayTools []string       `json:"authorized_gateway_tools,omitempty"`
	ReplayStatus           ReplayStatus   `json:"replay_status"`
	ActiveRun              *ActiveRunInfo `json:"active_run,omitempty"`
}

func (e ChainAttachedEvent) ChainServerEventType() string { return "chain.attached" }

type ChainUpdatedEvent struct {
	Type         string        `json:"type"`
	EventID      int64         `json:"event_id"`
	ChainVersion int64         `json:"chain_version"`
	ChainID      string        `json:"chain_id"`
	Defaults     ChainDefaults `json:"defaults,omitempty"`
}

func (e ChainUpdatedEvent) ChainServerEventType() string { return "chain.updated" }

type RunEnvelopeEvent struct {
	Type         string         `json:"type"`
	EventID      int64          `json:"event_id"`
	ChainVersion int64          `json:"chain_version"`
	RunID        string         `json:"run_id"`
	ChainID      string         `json:"chain_id"`
	Event        RunStreamEvent `json:"event"`
}

func (e RunEnvelopeEvent) ChainServerEventType() string { return "run.event" }

type ClientToolCallEvent struct {
	Type         string          `json:"type"`
	EventID      int64           `json:"event_id"`
	ChainVersion int64           `json:"chain_version"`
	RunID        string          `json:"run_id"`
	ChainID      string          `json:"chain_id"`
	ExecutionID  string          `json:"execution_id"`
	Name         string          `json:"name"`
	DeadlineAt   time.Time       `json:"deadline_at"`
	Input        map[string]any  `json:"input"`
	EffectClass  ToolEffectClass `json:"effect_class,omitempty"`
}

func (e ClientToolCallEvent) ChainServerEventType() string { return "client_tool.call" }

type ChainErrorEvent struct {
	Type         string `json:"type"`
	EventID      int64  `json:"event_id"`
	ChainVersion int64  `json:"chain_version"`
	CanonicalError
}

func (e ChainErrorEvent) ChainServerEventType() string { return "chain.error" }
