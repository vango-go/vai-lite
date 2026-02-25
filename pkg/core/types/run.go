package types

// RunRequest is the request body for /v1/runs and /v1/runs:stream.
type RunRequest struct {
	Request  MessageRequest `json:"request"`
	Run      RunConfig      `json:"run,omitempty"`
	Builtins []string       `json:"builtins,omitempty"`
}

// RunConfig controls server-side tool-loop limits.
type RunConfig struct {
	MaxTurns      int  `json:"max_turns,omitempty"`
	MaxToolCalls  int  `json:"max_tool_calls,omitempty"`
	MaxTokens     int  `json:"max_tokens,omitempty"`
	TimeoutMS     int  `json:"timeout_ms,omitempty"`
	ParallelTools bool `json:"parallel_tools"`
	ToolTimeoutMS int  `json:"tool_timeout_ms,omitempty"`
}

// RunStopReason indicates why a run ended.
type RunStopReason string

const (
	RunStopReasonEndTurn      RunStopReason = "end_turn"
	RunStopReasonMaxToolCalls RunStopReason = "max_tool_calls"
	RunStopReasonMaxTurns     RunStopReason = "max_turns"
	RunStopReasonMaxTokens    RunStopReason = "max_tokens"
	RunStopReasonTimeout      RunStopReason = "timeout"
	RunStopReasonCancelled    RunStopReason = "cancelled"
	RunStopReasonError        RunStopReason = "error"
	RunStopReasonCustom       RunStopReason = "custom"
)

// RunResultEnvelope is the blocking /v1/runs response envelope.
type RunResultEnvelope struct {
	Result *RunResult `json:"result"`
}

// RunResult is the terminal result of a server-side tool loop.
type RunResult struct {
	Response      *MessageResponse `json:"response,omitempty"`
	Steps         []RunStep        `json:"steps"`
	ToolCallCount int              `json:"tool_call_count"`
	TurnCount     int              `json:"turn_count"`
	Usage         Usage            `json:"usage"`
	StopReason    RunStopReason    `json:"stop_reason"`
	Messages      []Message        `json:"messages,omitempty"`
}

// RunStep captures one model-turn iteration.
type RunStep struct {
	Index       int              `json:"index"`
	Response    *MessageResponse `json:"response,omitempty"`
	ToolCalls   []RunToolCall    `json:"tool_calls,omitempty"`
	ToolResults []RunToolResult  `json:"tool_results,omitempty"`
	DurationMS  int64            `json:"duration_ms"`
}

// RunToolCall is a tool invocation emitted by the model.
type RunToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// RunToolResult is a gateway-side tool execution result.
type RunToolResult struct {
	ToolUseID string         `json:"tool_use_id"`
	Content   []ContentBlock `json:"content"`
	IsError   bool           `json:"is_error,omitempty"`
	Error     *Error         `json:"error,omitempty"`
}

// RunStreamEvent is the interface implemented by /v1/runs:stream SSE events.
type RunStreamEvent interface {
	EventType() string
}

// RunStartEvent is emitted once at stream start.
type RunStartEvent struct {
	Type            string `json:"type"`
	RequestID       string `json:"request_id,omitempty"`
	Model           string `json:"model"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
}

func (e RunStartEvent) EventType() string { return "run_start" }

// RunStepStartEvent is emitted at each turn start.
type RunStepStartEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

func (e RunStepStartEvent) EventType() string { return "step_start" }

// RunStreamEventWrapper wraps provider stream events.
type RunStreamEventWrapper struct {
	Type  string      `json:"type"`
	Event StreamEvent `json:"event"`
}

func (e RunStreamEventWrapper) EventType() string { return "stream_event" }

// RunToolCallStartEvent is emitted before gateway tool execution.
type RunToolCallStartEvent struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func (e RunToolCallStartEvent) EventType() string { return "tool_call_start" }

// RunToolResultEvent is emitted after gateway tool execution.
type RunToolResultEvent struct {
	Type    string         `json:"type"`
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"is_error,omitempty"`
	Error   *Error         `json:"error,omitempty"`
}

func (e RunToolResultEvent) EventType() string { return "tool_result" }

// RunStepCompleteEvent is emitted when a step finishes.
type RunStepCompleteEvent struct {
	Type     string           `json:"type"`
	Index    int              `json:"index"`
	Response *MessageResponse `json:"response,omitempty"`
}

func (e RunStepCompleteEvent) EventType() string { return "step_complete" }

// RunHistoryDeltaEvent appends deterministic history messages.
type RunHistoryDeltaEvent struct {
	Type        string    `json:"type"`
	ExpectedLen int       `json:"expected_len"`
	Append      []Message `json:"append"`
}

func (e RunHistoryDeltaEvent) EventType() string { return "history_delta" }

// RunCompleteEvent is the terminal success completion event.
type RunCompleteEvent struct {
	Type   string     `json:"type"`
	Result *RunResult `json:"result"`
}

func (e RunCompleteEvent) EventType() string { return "run_complete" }

// RunPingEvent is an optional keepalive event.
type RunPingEvent struct {
	Type string `json:"type"`
}

func (e RunPingEvent) EventType() string { return "ping" }

// RunErrorEvent is the terminal error event.
type RunErrorEvent struct {
	Type  string `json:"type"`
	Error Error  `json:"error"`
}

func (e RunErrorEvent) EventType() string { return "error" }
