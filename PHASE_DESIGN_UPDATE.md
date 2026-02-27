# Phase Design Update — Gateway Server Tools + BYOK Web Search/Fetch

Date: 2026-02-27

This document is an implementation-oriented design update that converts the earlier “gateway builtins” concept into a clearer, end-to-end **server tools** model and specifies the missing **keying/DX story** for VAI-native web search/fetch.

It is written to unblock a thorough implementation of the updated design in the gateway and SDKs.

---

## 0) Executive summary

We want the developer experience (DX) to be:

- “Want VAI native web search/fetch? Enable it via config.”
- “Bring your own keys (BYOK): put them in `.env` / secrets and pass them via SDK config.”
- “Nothing is a Gateway-paid default in v1 (no VAI-managed third-party keys).”

To achieve this safely while supporting server-side loops (`/v1/runs`) and Live WS (`/v1/live`), we formalize:

- **Gateway-executed tools** as **server tools** (renaming/clarifying the earlier “builtins” framing).
- A stable request shape: `server_tools` + `server_tool_config` (with legacy `builtins` accepted as a deprecated alias for a transition period).
- A complete, explicit **BYOK tool-provider key** story:
  - Tool provider keys are provided **out-of-band** (HTTP headers, or Live `hello.byok`), never in tool inputs.
  - The model calls tools with only non-secret parameters (`query`, `url`, etc.).

This doc also includes a detailed gateway implementation plan, tests, and acceptance criteria.

---

## 1) Principles / invariants (normative)

### 1.1 DX is determined by the client/developer

- The caller (SDK/app) decides what capabilities are enabled per run/session.
- The gateway does not silently enable networked tools “by default” just because a model might request them.

### 1.2 BYOK-first, no Gateway-paid defaults (v1)

- All upstream credentials are supplied by the developer:
  - LLM provider keys (Anthropic/OpenAI/Gemini/etc.)
  - Voice keys (Cartesia/ElevenLabs/etc.)
  - Web tool provider keys (Tavily/Exa/Firecrawl/etc.)
- The gateway must not ship with or rely on VAI-managed third-party web API keys in v1.
- Hosted deployments SHOULD keep server tools that incur third-party costs disabled unless/until billing exists (v2+), or BYOK is explicitly configured.

### 1.3 Secrets must never be model-visible

- API keys MUST NOT be passed as tool inputs.
- Tool inputs are non-secret operational parameters only (`query`, `url`, etc.).
- Keys are passed out-of-band and resolved by the gateway runtime.

### 1.4 Explicit server-tool allowlisting is required

Server tools are effectively “capabilities” the gateway will execute on behalf of a caller and may:
- incur costs
- perform outbound HTTP calls
- enlarge the attack surface (SSRF)

Therefore, they MUST be:
- explicitly enabled per run/session, and
- restricted to an allowlist controlled by gateway code/config.

---

## 2) Terminology (unified)

### 2.1 Tool categories

1) **Provider-native tools**
- Executed by the upstream LLM provider.
- Example: Anthropic native web search tool types, OpenAI Responses web search, etc.

2) **Client-executed function tools**
- Executed in the caller’s process (SDK local tool loop).
- Example: Go SDK `Messages.Run` with `vai.VAIWebSearch(tavily.NewSearch(...))`.

3) **Gateway-executed server tools** (formerly “builtins”)
- Executed in the gateway process.
- Enabled per run/session via `server_tools` (preferred) or `builtins` (deprecated alias).
- Configured via `server_tool_config`.
- Example: `vai_web_search`, `vai_web_fetch`.

### 2.2 “Server tools” vs “builtins”

The legacy term **builtins** is retained only for backward compatibility with earlier drafts.

**Design update:**
- New docs and new implementations should prefer the term **server tools**.
- Wire: `server_tools` is preferred; `builtins` is a deprecated alias.

**Implemented:** gateway request decoding accepts both and rejects conflicting values when both are present.

---

## 3) Updated API design (HTTP): `/v1/runs` and `/v1/runs:stream`

> Note: `/v1/runs` and `/v1/runs:stream` are implemented in the gateway.

### 3.1 Request shape (new preferred fields)

Add:
- `server_tools: string[]`
- `server_tool_config: object`

Example:

```jsonc
{
  "request": {
    "model": "anthropic/claude-sonnet-4",
    "max_tokens": 4096,
    "messages": [{ "role": "user", "content": "Find the latest Go release." }]
  },
  "run": {
    "max_turns": 8,
    "max_tool_calls": 20,
    "timeout_ms": 60000,
    "tool_timeout_ms": 30000
  },
  "server_tools": ["vai_web_search"],
  "server_tool_config": {
    "vai_web_search": {
      "provider": "tavily",
      "max_results": 8,
      "allowed_domains": ["go.dev", "github.com"],
      "blocked_domains": ["example-spam.tld"]
    }
  }
}
```

### 3.2 Back-compat (`builtins`)

- `builtins: string[]` is accepted as a deprecated alias of `server_tools`.
- SDKs and docs should migrate to `server_tools`.

**Implemented behavior:**
- If both `server_tools` and `builtins` appear:
  - reject if they differ
  - accept if identical (or prefer `server_tools`)

### 3.3 Validation rules (server tools)

In addition to existing `/v1/runs` validation:

- `server_tools`/`builtins` must contain only allowlisted tool names.
- `server_tool_config` must:
  - have keys that are a subset of enabled `server_tools`
  - validate per-tool config schema (e.g. `provider` must be known)
  - reject unknown fields unless explicitly allowed (choose strictness per tool)

### 3.4 Tool schema injection (canonical tools)

Server tools are “referenced, not defined” by callers.

Normative behavior:
- Gateway injects canonical tool schema + description into the model request.
- Gateway ignores caller-supplied schema/description for server tools.
- Name collision rule:
  - if caller supplies a `function` tool whose `name` equals an enabled server tool name, reject.

### 3.5 Credential model (BYOK tool providers)

Server tools that call third-party APIs require BYOK provider keys.

**HTTP mechanism:** `X-Provider-Key-*` headers.

Examples:
- `X-Provider-Key-Tavily: tvly_...`
- `X-Provider-Key-Exa: exa_...`
- `X-Provider-Key-Firecrawl: fc_...`

Rules:
- Keys are held in memory for request lifetime only.
- Keys are never logged, never returned, never persisted.
- Keys must never be inserted into tool inputs.

### 3.6 Error model (missing tool provider config vs missing key)

These should be distinct and precise for good DX.

Recommended canonical errors:

1) Server tool enabled but `provider` missing:
- HTTP `400`
- `invalid_request_error`
- `code="tool_provider_missing"`
- `param="server_tool_config.vai_web_search.provider"`

2) Server tool enabled with provider `tavily`, but `X-Provider-Key-Tavily` missing:
- HTTP `401`
- `authentication_error`
- `code="provider_key_missing"`
- `param="X-Provider-Key-Tavily"`

3) Provider value unknown/unsupported:
- HTTP `400`
- `invalid_request_error`
- `code="unsupported_tool_provider"`
- `param="server_tool_config.vai_web_search.provider"`

4) Tool provider upstream error:
- HTTP `502` (or `500` depending on policy)
- `api_error`
- `code="tool_provider_error"`
- include `request_id`; never include secrets

**Implemented:** canonical error codes are returned with targeted tests for validation/auth scenarios.

---

## 4) Updated API design (WebSocket Live): BYOK tool-provider keys

This design update also applies to Live mode, because Live needs gateway-executed tools.

### 4.1 Live keying constraints

Browsers cannot reliably set arbitrary WS headers after upgrade, so Live uses in-band `hello.byok` to transmit BYOK credentials.

### 4.2 Proposed Live BYOK shape (tool providers)

Continue using a map-like BYOK shape:
- `hello.byok.keys["tavily"]`, `["firecrawl"]`, `["exa"]`, etc.

**Important:** no secrets are ever echoed back to the client, logged, or persisted.

### 4.3 Proposed Live tool enable/config shape

Add to `hello`:

```jsonc
{
  "tools": {
    "server_tools": ["vai_web_search", "vai_web_fetch"],
    "server_tool_config": {
      "vai_web_search": {"provider":"tavily"},
      "vai_web_fetch": {"provider":"firecrawl"}
    }
  }
}
```

**Implemented:** Live hello decoding/validation includes `tools.server_tools` and `tools.server_tool_config`.

---

## 5) Server tool definitions (initial set)

### 5.1 `vai_web_search`

Purpose:
- Execute a web search via a configured third-party provider and return normalized results to the model.

Provider adapters (initial):
- `tavily`
- `exa`

Config (`server_tool_config.vai_web_search`):
- `provider` (required): `"tavily" | "exa"`
- `max_results` (optional): default e.g. 5–10 (cap hard in gateway)
- `allowed_domains` / `blocked_domains` (optional)
- `country` / `language` (optional; provider-dependent; normalize where possible)

Credentials:
- `tavily` → `X-Provider-Key-Tavily` (HTTP) or `hello.byok.keys["tavily"]` (Live)
- `exa` → `X-Provider-Key-Exa` / `hello.byok.keys["exa"]`

Output normalization:
- Return a stable schema to the model (title/url/snippet/published_at/source).
- Include provider name for observability (non-secret).

### 5.2 `vai_web_fetch`

Purpose:
- Fetch/extract page content for a URL via configured provider.

Provider adapters (initial):
- `firecrawl`

Config (`server_tool_config.vai_web_fetch`):
- `provider` (required): `"firecrawl"`
- `format` (optional): `"text" | "markdown" | "html"` (gateway normalizes)
- size limits + allowed/blocked domains (optional; but always enforce SSRF rules)

Credentials:
- `firecrawl` → `X-Provider-Key-Firecrawl` / `hello.byok.keys["firecrawl"]`

---

## 6) Security and abuse controls (must-have)

### 6.1 SSRF controls (normative)

All gateway-executed outbound HTTP tools MUST:
- block private/link-local ranges
- block cloud metadata endpoints
- disallow non-http(s) schemes
- prevent DNS rebinding (validate at dial layer, not only at parse layer)
- enforce max response size + redirects + timeouts

This is already specified in `GATEWAY_SPEC.md` section 11.7; implementation MUST follow it.

### 6.2 Secret redaction (normative)

Do not log:
- `Authorization` bearer
- any `X-Provider-Key-*` values
- Live `hello.byok` contents

Never echo secrets back to clients in error responses.

### 6.3 Tool budgeting (normative)

Server tools must be bounded by:
- `run.max_tool_calls`
- `run.tool_timeout_ms`
- per-tool result size caps (bytes and item counts)

**Implemented baseline:** hard/default caps are enforced for web search and result payload sizes are bounded.

---

## 7) Gateway implementation plan (detailed)

> This plan assumes the gateway codebase matches the structure referenced in `GATEWAY_SPEC.md` (e.g., `pkg/gateway/*`).

### 7.1 Data model + decoding

1) Add new request fields to the `/v1/runs` request struct:
   - `ServerTools []string` (json: `server_tools`)
   - `ServerToolConfig map[string]json.RawMessage` (json: `server_tool_config`)
   - `Builtins []string` (legacy alias; json: `builtins`) — if you keep it for compatibility

2) Strict decoding rules:
   - accept unknown top-level fields for forward-compat only where intended
   - reject conflicting server tool selections between `server_tools` and `builtins`

**Implemented:** strict decoding and tests are in place.

### 7.2 Validation

Implement:
- allowlist check for tool names
- config decode per enabled tool (required/optional fields)
- provider selection validation
- name collision rule with caller-supplied function tools

### 7.3 Canonical tool registry (server tools)

Create a registry that provides:
- canonical schema (JSON Schema-ish parameters) and description
- execution handler
- config decoder/validator
- required BYOK key header name per provider selection (dynamic)

Suggested structure:

```go
type ServerTool interface {
    Name() string
    CanonicalToolDefinition(cfg any) types.Tool // or tool schema/desc pieces
    DecodeConfig(raw json.RawMessage) (any, error)
    Execute(ctx context.Context, execCtx ToolExecContext, input json.RawMessage) ([]types.ContentBlock, error)
}
```

**Implemented:** gateway server tools are implemented in `pkg/gateway/tools/servertools/`.

### 7.4 Credential resolution (tool providers)

For HTTP:
- parse the incoming `X-Provider-Key-*` headers into a request-scoped credential map
- do not store beyond request lifetime

For Live:
- decode `hello.byok.keys` (or whatever final shape)
- store only in session memory
- redact in logs

### 7.5 Tool execution path (run loop integration)

In `/v1/runs`, when the model emits a tool call:
1) match tool name
2) if server tool:
   - execute in gateway
   - write a `tool_result` in the canonical format
3) if provider-native tool:
   - pass to provider (already part of model request)
4) if unknown tool:
   - error

**Implemented:** run loop controller and streaming event emission are active in `/v1/runs` and `/v1/runs:stream`.

### 7.6 Provider adapters (Tavily, Exa, Firecrawl)

Implement minimal adapters that:
- accept key + input parameters
- call provider API
- normalize result shape
- enforce per-tool limits and timeouts
- never log secrets

**Implemented:** provider adapters (including Exa) and hermetic tests are in place.

### 7.7 Observability

Add metrics:
- tool call count by tool/provider
- tool latency histograms
- tool error counts by error code

Tracing:
- span per tool invocation (tool name + provider name, no secrets)

Logging:
- tool name + provider + duration + request_id, no query string if you consider it sensitive (make it configurable)

### 7.8 CORS allowlist updates

If CORS is enabled, gateway must enumerate concrete headers; add:
- `X-Provider-Key-Tavily`
- `X-Provider-Key-Exa`
- `X-Provider-Key-Firecrawl`

Note: BYOK-from-browser is discouraged; keys would be exposed in devtools.

### 7.9 Tests (must be hermetic)

1) Strict decode tests for `/v1/runs` request:
   - `server_tools` and `server_tool_config` decoding
   - legacy `builtins` alias decoding
   - conflict rejection when both present and differ

2) Validation tests:
   - unsupported tool name
   - missing provider
   - unsupported provider
   - config supplied for disabled tool
   - name collision with caller tool

3) Credential resolution tests:
   - missing key -> `401 provider_key_missing`
   - wrong header name -> missing key

4) Tool adapter tests:
   - fake HTTP provider servers for Tavily/Exa/Firecrawl
   - result normalization
   - timeout behavior
   - size caps

5) Security tests (unit/integration):
   - SSRF blocked IP ranges
   - DNS rebinding prevention behavior (as much as feasible with controlled dialer tests)

---

## 8) SDK plan (DX improvements)

### 8.1 Go SDK (client-executed tools)

Current DX is already good:
- `vai.VAIWebSearch(tavily.NewSearch(os.Getenv("TAVILY_API_KEY")))`

Recommended additive sugar:
- add `tavily.FromEnv()`, `exa.FromEnv()`, `firecrawl.FromEnv()` helpers
  - internally read env vars and return provider implementations

**Implemented:** helper functions (`FromEnv`) and docs updates are available.

### 8.2 Go SDK (proxy/server tools)

When `/v1/runs` is implemented, the SDK should provide a simple opt-in path so developers can say:
- “I want server-side runs and gateway-executed web tools.”

Recommended:
- keep `client.Messages.Run` as client-side tool loop (today’s behavior)
- add/keep `client.Runs.Create/Stream` for server-run
- allow specifying `server_tools` + `server_tool_config` and tool-provider keys via `WithProviderKey` (or a dedicated helper)

**Implemented (Go SDK):** server-tools request fields and provider-key header propagation are supported.

### 8.3 TypeScript SDK (thin)

The TS SDK can expose:
- `serverTools: [...]`
- `serverToolConfig: {...}`
- `providerKeys: {...}` including tool providers (tavily/exa/firecrawl)

The SDK maps provider keys to headers automatically.

**TO BE IMPLEMENTED:** TS SDK design and implementation.

---

## 9) Acceptance criteria (for the new design)

### 9.1 `/v1/runs` correctness

- Requests can enable server tools via `server_tools`.
- The gateway injects canonical tool schemas and executes server tools.
- Server tools can select providers via `server_tool_config`.
- Missing/invalid provider config produces the correct `400` error codes.
- Missing tool-provider BYOK keys produces the correct `401` errors.
- Keys are never logged, never persisted, never echoed.

### 9.2 SSRF and bounds

- Outbound fetch/search obey SSRF blocklists, redirect limits, and size limits.
- Tool timeouts and tool call budgets are enforced.

### 9.3 Compatibility

- Legacy `builtins` works as an alias for `server_tools` (and conflicting dual usage is rejected).

### 9.4 Developer clarity

- Docs make it explicit that:
  - SDK local tool loop uses Go adapters
  - Gateway server-run uses declarative server tools + BYOK headers
  - keys never go in tool inputs
  - there are no VAI-paid defaults in v1

---

## 10) Work breakdown (recommended sequencing)

1) Implement `/v1/runs` skeleton run loop + SSE stream (Phase 06)
2) Implement server tool registry + allowlist + schema injection
3) Implement `vai_web_search` adapter(s):
   - start with Tavily OR Exa (pick one) behind `provider` config
4) Implement `vai_web_fetch` adapter (Firecrawl)
5) Add strict decode + validation + error codes
6) Add SSRF-safe outbound HTTP client utilities and tests
7) Add CORS header allowlist updates
8) Add SDK support for `server_tools` contract (and adapter `FromEnv()` sugar)

---

# Implementation Plan — Server Tools (`server_tools`) + BYOK Web Search/Fetch (Runs + Live)

## Summary

Implement the new **gateway server-tools** design end-to-end across:

- HTTP server-side run loop: `POST /v1/runs` and `POST /v1/runs:stream`
- Live WebSocket mode: `GET /v1/live` (`hello` handshake + per-turn tool execution)

Key outcomes:

- Replace the confusing “builtins” UX with a clear **server tools** model (`server_tools` + `server_tool_config`) while keeping **legacy `builtins` as a deprecated alias**.
- Make VAI-native web search/fetch truly **BYOK**:
  - Tool-provider keys are provided **out-of-band** (HTTP headers / Live `hello.byok`)
  - Keys are never present in tool inputs and never logged/persisted
- Implement provider selection for `vai_web_search`:
  - Supports **Tavily + Exa**
  - Supports **provider inference** from headers/hello BYOK when config is missing and unambiguous
- Keep the gateway safe: strict allowlists, strict validation, SSRF protections, bounded timeouts/budgets.

---

## A) Public API / Types Changes

### A1. `/v1/runs` request shape (core types)

**Modify** `pkg/core/types/run.go`:

- Add:
  - `ServerTools []string  'json:"server_tools,omitempty"'`
  - `ServerToolConfig map[string]any 'json:"server_tool_config,omitempty"'`  
    (where values must be JSON objects; gateway will decode per-tool)
- Keep existing:
  - `Builtins []string 'json:"builtins,omitempty"'` as **deprecated alias**
- Compatibility rule:
  - If both `server_tools` and `builtins` are present:
    - Accept if equivalent sets (same names ignoring order/whitespace)
    - Reject if they differ (strict decode error)

**Rationale:** keeps SDK usage reasonable (no `json.RawMessage` ergonomics) while retaining strict validation in gateway.

### A2. Strict decode updates

**Update** `pkg/core/types/strict_decode_runs.go`:

- Allow top-level fields: `request`, `run`, `builtins`, `server_tools`, `server_tool_config`
- Decode + validate:
  - `server_tools` must be `[]string`, trimmed, non-empty, no duplicates
  - `server_tool_config` must be a JSON object if present
    - each value must be a JSON object (reject arrays/strings/etc.)
  - `builtins` remains supported
  - If both `builtins` and `server_tools` appear:
    - normalize + compare; reject if mismatch

**Update tests** `pkg/core/types/strict_decode_runs_test.go`:
- New cases:
  - accepts `server_tools`
  - rejects duplicates in `server_tools`
  - rejects non-object `server_tool_config`
  - rejects config entry value that is not an object
  - conflict between `builtins` and `server_tools`

---

## B) Gateway `/v1/runs` Implementation Updates (Server Tools + BYOK Keys)

### B1. Rename concept internally (optional but recommended)

Introduce a new package and migrate imports:

- New: `pkg/gateway/tools/servertools/` (replaces `pkg/gateway/tools/builtins/`)
- Keep `pkg/gateway/tools/builtins/` temporarily as deprecated shim (optional), or update all imports and delete later.

**Decision complete:** implement `servertools` package and update all call sites; do not rely on “builtins” naming for new behavior.

### B2. Server tool registry design (gateway)

Create `pkg/gateway/tools/servertools/registry.go` with:

- Tool allowlist: `vai_web_search`, `vai_web_fetch`
- Canonical tool definitions (schemas) for injection:
  - `vai_web_search` tool input: `{ query: string, max_results?: integer }`
  - `vai_web_fetch` tool input: `{ url: string }`
- Execution interface (similar to current builtins):
  - `Execute(ctx, toolName, input map[string]any) ([]types.ContentBlock, *types.Error)`
- Runtime binding:
  - registry should enforce an **enabled set** (only tools listed in `server_tools`)
  - if a model tries calling a tool not enabled, return `unsupported_function_tool` (or a new `server_tool_not_enabled` invalid_request)

### B3. Tool provider selection + inference (runs)

Implement in gateway runs handler (`pkg/gateway/handlers/runs.go`):

1. Compute `effectiveServerTools`:
   - Prefer `runReq.ServerTools` if non-empty
   - Else use legacy `runReq.Builtins`

2. Parse `server_tool_config` (if provided) into per-tool typed structs with `json.Decoder.DisallowUnknownFields()`:
   - `vai_web_search` config:
     - `provider` optional (`"tavily" | "exa"`)
     - `max_results` optional (default 5, hard cap 10)
     - `allowed_domains` optional
     - `blocked_domains` optional
   - `vai_web_fetch` config:
     - `provider` optional (default `"firecrawl"`)
     - `format` optional (`"text" | "markdown" | "html"`) (initially used only to control output normalization; if not supported by provider, ignore with explicit warning in logs—not user output)
     - `allowed_domains` / `blocked_domains` optional

3. Provider inference rules (as requested):
   - For `vai_web_search` if config provider missing:
     - If exactly one of these headers is present, choose it:
       - `X-Provider-Key-Tavily` ⇒ provider = `tavily`
       - `X-Provider-Key-Exa` ⇒ provider = `exa`
     - If none present → error `tool_provider_missing`
     - If both present → error `tool_provider_missing` with message “ambiguous; set provider”
   - For `vai_web_fetch` if config provider missing:
     - Default provider = `firecrawl`

4. BYOK key requirements (normative):
   - `vai_web_search` provider `tavily` ⇒ require header `X-Provider-Key-Tavily`
   - `vai_web_search` provider `exa` ⇒ require header `X-Provider-Key-Exa`
   - `vai_web_fetch` provider `firecrawl` ⇒ require header `X-Provider-Key-Firecrawl`

5. Error mapping:
   - Missing provider config (and cannot infer):
     - `400 invalid_request_error`
     - `code="tool_provider_missing"`
     - `param="server_tool_config.<tool>.provider"`
   - Missing required tool provider key header:
     - `401 authentication_error`
     - `code="provider_key_missing"`
     - `param="<header name>"`
   - Unsupported provider value:
     - `400 invalid_request_error`
     - `code="unsupported_tool_provider"`
     - `param="server_tool_config.<tool>.provider"`

### B4. Registry construction per request (runs)

Replace current `newBuiltinsRegistry()` behavior that reads env keys.

New approach in `RunsHandler`:

- Keep base URLs in gateway config:
  - `TavilyBaseURL`
  - `FirecrawlBaseURL`
  - **Add** `ExaBaseURL` with default `https://api.exa.ai`
- Build a restricted HTTP client once per request:
  - `toolHTTPClient := safety.NewRestrictedHTTPClient(h.HTTPClient)`
- Construct server tool executors with **keys from request headers**:
  - For Tavily: `tavily.NewClient(tavilyKey, cfg.TavilyBaseURL, toolHTTPClient)`
  - For Firecrawl: `firecrawl.NewClient(firecrawlKey, cfg.FirecrawlBaseURL, toolHTTPClient)`
  - For Exa: new adapter client (see section C)

**Important:** do not keep any tool-provider keys in gateway config.

### B5. Tool injection + execution path (runs)

- Replace `registry.ValidateAndInject(workingReq.Request.Tools, workingReq.Builtins)` with:
  - `registry.ValidateAndInject(workingReq.Request.Tools, effectiveServerTools)`
  - plus a new config validation step before injection:
    - ensure tool provider keys are present for enabled tools
- Ensure collision rule remains:
  - if caller supplies any `function` tool in `request.tools` -> reject
  - if caller tool name collides with server tool name -> reject

### B6. Update existing runs tests

Update `pkg/gateway/handlers/runs_test.go`:

- Replace `TestRunsHandler_BuiltinMissingConfigFailFast` with:
  - `TestRunsHandler_ServerToolMissingProvider_Fails` (no config, no headers → 400 tool_provider_missing)
  - `TestRunsHandler_ServerToolInferProviderFromHeader_Succeeds` (builtins/server_tools includes `vai_web_search`, `X-Provider-Key-Tavily` present → does not fail at validation)
  - `TestRunsHandler_ServerToolMissingKey_Fails401` (provider resolved to tavily but missing tavily key)
- Add tests for:
  - ambiguous provider inference (both headers present) → 400 tool_provider_missing
  - explicit provider `exa` but missing `X-Provider-Key-Exa` → 401 provider_key_missing
- Keep existing stream timeout / ping tests (should remain unchanged)

---

## C) Implement Gateway Exa Adapter (for `vai_web_search`)

Create `pkg/gateway/tools/adapters/exa/search.go`:

- Port logic from `sdk/adapters/exa/exa.go` (Search portion), but:
  - Use `safety.DecodeJSONBodyLimited` for responses
  - Enforce content-type checks and max bytes
  - Use restricted http client from `safety.NewRestrictedHTTPClient`
  - Header: `x-api-key: <key>`
- Provide:
  - `type Client struct { apiKey, baseURL string; httpClient *http.Client }`
  - `func NewClient(apiKey, baseURL string, httpClient *http.Client) *Client`
  - `func (c *Client) Configured() bool`
  - `func (c *Client) Search(ctx, query string, maxResults int) ([]Hit, error)` where `Hit` matches a local normalized shape (title/url/snippet/content)

Tests:
- `pkg/gateway/tools/adapters/exa/search_test.go` with `httptest.Server` to validate:
  - request body fields (`query`, `numResults`, etc.)
  - correct header usage
  - response decode + normalization
  - non-200 handling with limited body read

---

## D) Server Tool Behavior Details (Web Search/Fetch)

### D1. Domain allow/block enforcement (gateway-side, provider-agnostic)

Even if providers support include/exclude domains, enforce at gateway too:

- After receiving hits/content:
  - Parse URL host
  - If `allowed_domains` non-empty: only keep matches
  - If `blocked_domains` non-empty: drop matches
- Ensure:
  - case-insensitive host compare
  - treat subdomains as matches if desired (decision complete: **allow subdomain match**; `docs.example.com` matches `example.com` allowlist? Choose one)
    - Recommended: match exact host OR subdomain of allowlisted domain.
    - For blocked: match exact host OR subdomain.
- Add unit tests around URL host matching.

### D2. Max results enforcement

Compute effective max results:

- `hardCap = 10`
- `default = 5`
- `requested = tool_input.max_results` if present and > 0 else default
- `capFromConfig = server_tool_config.max_results` if present and > 0 else hardCap
- `effective = min(requested, capFromConfig, hardCap)`

---

## E) CORS Allowlist Updates (tool-provider headers)

Update `pkg/gateway/mw/cors.go`:

- Add:
  - `X-Provider-Key-Tavily`
  - `X-Provider-Key-Exa`
  - `X-Provider-Key-Firecrawl`

Update `pkg/gateway/mw/cors_test.go`:
- Ensure preflight reflection includes these when requested and allowlisted.

---

## F) Gateway Config Changes

Update `pkg/gateway/config/config.go`:

- Remove (or deprecate and stop using) tool-provider key env vars:
  - `VAI_PROXY_TAVILY_API_KEY`
  - `VAI_PROXY_FIRECRAWL_API_KEY`
- Keep base URL overrides:
  - `VAI_PROXY_TAVILY_BASE_URL`
  - `VAI_PROXY_FIRECRAWL_BASE_URL`
- Add Exa base URL override:
  - `VAI_PROXY_EXA_BASE_URL` default `https://api.exa.ai`

Update `pkg/gateway/config/config_test.go` accordingly.

**Invariant:** gateway should never require env keys for these tools; keys must come from caller BYOK headers / Live `hello.byok`.

---

## G) Live WebSocket: Add Server Tools + BYOK Tool Keys

### G1. Live protocol changes

Update `pkg/gateway/live/protocol/protocol.go`:

- Add to `ClientHello`:
  - `Tools *HelloTools 'json:"tools,omitempty"'`
- Add `HelloTools`:
  - `ServerTools []string 'json:"server_tools,omitempty"'`
  - `ServerToolConfig map[string]any 'json:"server_tool_config,omitempty"'`
  - (Optionally accept legacy `builtins` in live hello too; decision complete: **do not**—Live is newer; keep only `server_tools`)

Update:
- `DecodeClientMessage` + `ValidateHello`:
  - Validate tools fields if present (arrays, duplicates, config object/value objects)
- `RedactedForLog`:
  - include booleans like `has_server_tools`, tool names (not config), and do not include keys/config content.

Update `pkg/gateway/live/protocol/protocol_test.go`:
- decode + validate hello with tools fields
- verify redaction doesn’t include any BYOK values or tool config internals.

### G2. Live handler handshake changes

Update `pkg/gateway/handlers/live.go`:

- After resolving LLM key + Cartesia key:
  - If `hello.Tools` requests server tools:
    - Resolve providers using the same inference rules as `/v1/runs`, except keys come from:
      - `byokForProvider(hello.BYOK, "tavily")`
      - `byokForProvider(hello.BYOK, "exa")`
      - `byokForProvider(hello.BYOK, "firecrawl")`
    - Fail fast with `unauthorized` errors if required BYOK keys missing.
  - Construct a servertools registry for the session and pass into `session.Dependencies`.

Add tests in `pkg/gateway/handlers/live_test.go`:
- hello requests `vai_web_search` without provider and with tavily key only → accepted
- hello requests `vai_web_search` with provider exa but no exa key → unauthorized
- ambiguous (both tavily and exa keys present, no provider) → bad_request tool_provider_missing

---

## H) Live Session: Implement Tool Loop With Server Tools + Terminal `talk_to_user`

### H1. New Live turn runner architecture

Refactor `pkg/gateway/live/session/session.go`:

Replace current `runTurn` (single-shot, tool-choice forced to talk_to_user) with a bounded **tool loop**:

- Tools available to the model each turn:
  - `talk_to_user` (terminal)
  - enabled server tools from hello (`vai_web_search`, `vai_web_fetch`) injected with canonical schema
- Tool execution:
  - server tools executed via the bound servertools registry (same executors as runs)
  - `talk_to_user` handled as terminal action:
    - when detected, extract `text` and return it immediately (no further model calls)

### H2. Live tool loop algorithm (decision complete)

For each committed utterance:

1. Initialize `historyTurn := append(history, userMessage)`
2. Loop with budgets:
   - `maxToolCallsPerTurn`:
     - default 5
     - configurable via gateway config later (not required now)
   - `toolTimeoutMS`:
     - reuse `h.Config.LiveTurnTimeout` for overall
     - set per-tool timeout = min(10s, remaining time)
3. Call provider (streaming or non-streaming):
   - Decision complete: **use streaming** (existing infra) but rely on accumulator; keep current early-exit parsing for talk_to_user.
4. Handle outcomes:
   - If talk_to_user text parsed (early-exit path): return text
   - Else if stop_reason == tool_use:
     - Extract tool uses from response
     - Reject if it contains both talk_to_user and other tool calls (invalid_request; log + pick talk_to_user only, or fail)
       - Decision complete: **fail the turn** with a session warning and then fallback to a short apology via TTS (so user hears something)
     - For each tool use:
       - If server tool: execute, create `tool_result` blocks, append to historyTurn, continue loop
       - If unknown tool: fail the turn similarly
   - Else (end_turn / plain text):
     - lenient fallback: use assistant text content as speak text

5. Return `speakText`, and the session continues to `speakTurn` as today.

### H3. History semantics for live (v1)

Decision complete (v1 baseline):
- Maintain `history` as plain user/assistant text messages between turns (what the model “should see” next time).
- Inside the tool loop, maintain an internal `historyTurn` that includes:
  - assistant tool_use blocks (from model response)
  - user tool_result blocks (from execution)
- At the end of the turn, commit only the final assistant spoken text to the outer `history` (as today).

(Played-history correctness and canonical-history auditing remain Phase 9B+ work; not required for “new design” web tools.)

### H4. Testing live tool loop

Add/extend tests in `pkg/gateway/live/session/turn_test.go`:

- Model emits `vai_web_search` tool_use then `talk_to_user`:
  - ensure tool executed and final speak text returned
- Tool timeout:
  - ensure tool execution respects timeout and turn completes with a safe apology
- Budget exceeded:
  - ensure max tool calls enforced with safe fallback

Use fake provider streams similar to existing tests (no real network).

---

## I) SDK Updates (Go)

### I1. Types usage

Update SDK callers to prefer `server_tools` / `server_tool_config`:

- `sdk/runs_service.go`:
  - Update validation error message to “server tools” terminology.
  - No longer mention “builtins” as primary.

### I2. Header propagation for tool providers

Update `sdk/proxy_transport.go`:

- Extend `providerByokHeaders` with:
  - `tavily` → `X-Provider-Key-Tavily`
  - `exa` → `X-Provider-Key-Exa`
  - `firecrawl` → `X-Provider-Key-Firecrawl`

Update `RunsService.Create/Stream` to attach tool-provider headers when needed:

- Determine enabled server tools:
  - prefer `req.ServerTools` else `req.Builtins`
- If `vai_web_search` enabled:
  - If config provider is set and parseable → set only that provider key header
  - Else set whichever keys are configured in the client (`WithProviderKey("tavily", ...)`, `WithProviderKey("exa", ...)`)
- If `vai_web_fetch` enabled:
  - set firecrawl key header if available

Update tests:
- `sdk/runs_service_test.go`:
  - verify correct headers are attached based on `server_tools`/config
  - verify both keys attached when provider omitted (to support inference)

---

## J) Docs Cleanup (remove “TO BE IMPLEMENTED” where completed)

After code is implemented and tests pass:

- Update:
  - `GATEWAY_SPEC.md`
  - `DEVELOPER_GUIDE.md`
  - `PHASE_DESIGN_UPDATE.md`
- Remove / adjust any **TO BE IMPLEMENTED** annotations that are now shipped.
- Ensure docs correctly reflect that `/v1/runs` is implemented (it already exists in code) and describe the new server-tools shape.

---

## K) Acceptance Criteria (exit conditions)

### Runs
- `/v1/runs` and `/v1/runs:stream` accept `server_tools` + `server_tool_config`.
- Legacy `builtins` continues to work as alias.
- `vai_web_search` supports Tavily + Exa with BYOK headers; provider can be inferred from headers when unambiguous.
- `vai_web_fetch` supports Firecrawl with BYOK header.
- Missing provider/config/key errors return correct status/type/code/param.
- No tool-provider keys are ever logged, echoed, or persisted.

### Live
- Live `hello` can enable server tools and configure/infer providers via `hello.byok`.
- Live turns can execute server tools before producing `talk_to_user` speech.
- Interrupt/cancel semantics still work (tool loop is context-cancellable).
- All tests are hermetic (fake provider streams + httptest servers).

### Security
- SSRF validation remains enforced for fetch target URLs.
- Restricted outbound HTTP client is used for all tool-provider API calls.
- CORS allowlist enumerates tool-provider BYOK headers.

---

## Assumptions / Defaults (locked)

- Header names: `X-Provider-Key-Tavily`, `X-Provider-Key-Exa`, `X-Provider-Key-Firecrawl`
- `vai_web_search` providers: `tavily`, `exa`
- `vai_web_fetch` provider: `firecrawl`
- `vai_web_search` hard cap: `10` results; default `5`
- Provider inference: allowed only when exactly one matching key is present
- Live tool loop budgets: `maxToolCallsPerTurn=5` (initial constant; configurable later)
