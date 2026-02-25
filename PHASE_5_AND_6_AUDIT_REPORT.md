# Phase 5 and 6 Audit Report (VAI Gateway v1)

Date: 2026-02-25

This report audits the current Phase 5 and Phase 6 implementations against the **VAI Gateway Specification (v1)** provided in the project docs (quoted below where relevant).

## Scope

Phase 5 (Provider compatibility validation + `/v1/models`)
- Compatibility catalog + validator: `pkg/gateway/compat/catalog.go`, `pkg/gateway/compat/validate.go`
- Models endpoint: `pkg/gateway/handlers/models.go`
- Compatibility enforcement at ingress:
  - `/v1/messages`: `pkg/gateway/handlers/messages.go`
  - `/v1/runs`: `pkg/gateway/handlers/runs.go`

Phase 6 (`/v1/runs` + `/v1/runs:stream` + builtins)
- Strict decode for run requests: `pkg/core/types/strict_decode_runs.go`
- Runs handlers: `pkg/gateway/handlers/runs.go`
- Run controller / tool-loop: `pkg/gateway/runloop/runloop.go`, `pkg/gateway/runloop/accumulator.go`
- Builtins registry + builtins: `pkg/gateway/tools/builtins/*`
- SSRF safety primitives for builtins: `pkg/gateway/tools/safety/*`

Validation
- Test suite: `go test ./...` (passes)

## Summary Of Findings

Priority is relative to hosted-gateway risk.

- **P0 Security (Resolved)**: Restricted tool HTTP transport now explicitly disables proxy usage and proxy CONNECT headers.
- **P1 Correctness (Resolved)**: Tool failures now preserve non-empty error text payloads in `tool_result.content`.
- **P1 Spec/Behavior (Resolved)**: Blocking `/v1/runs` now returns `200` with `result.stop_reason:"timeout"` for run timeout policy.
- **P1 Spec/Interoperability (Resolved)**: `gemini-oauth/*` is now accepted by gateway BYOK header mapping and upstream provider factory; `/v1/models` now includes auth metadata for allowlisted `gemini-oauth/*`.
- **P2 Spec/UX (Resolved)**: `/v1/runs:stream` now emits gateway-generated voice events (`audio_chunk`, `audio_unavailable`) as top-level run events rather than wrapped `stream_event`.
- **P2 Reliability (Resolved)**: Restricted transport dial validation no longer reparses synthetic URLs and now handles IPv6 literals correctly.
- **P2 Config Consistency (Resolved)**: `/v1/runs:stream` now enforces `min(run.timeout_ms, VAI_PROXY_SSE_MAX_DURATION)`.

## Findings (Detailed)

### 1) P0 Security: SSRF Protections Can Be Bypassed Via HTTP Proxy Settings

**Spec quotes**

> “Gateway-managed tools that make outbound HTTP requests (e.g., `vai_web_search`, `vai_web_fetch`) MUST enforce egress controls to prevent SSRF attacks.” (Section 11.7)

> “Instead, enforce validation at the socket layer by overriding `http.Transport.DialContext`: resolve the IP, validate it against the blocklist, and dial the validated IP directly.” (Section 11.7)

**What the implementation does**

- The gateway’s shared upstream `http.Client` is created with `Proxy: http.ProxyFromEnvironment`. (`pkg/gateway/server/server.go:33`)
- Builtins use a “restricted HTTP client” derived from that base client: `toolHTTPClient := safety.NewRestrictedHTTPClient(h.HTTPClient)`. (`pkg/gateway/handlers/runs.go:256`)
- The restricted client overrides `DialContext` to validate DNS/IPs, but it **does not disable transport proxying**. (`pkg/gateway/tools/safety/transport.go:20`, `pkg/gateway/tools/safety/transport.go:30`)

**Why this is a real bypass**

If the transport is configured to use an HTTP proxy (via environment variables or explicit `Transport.Proxy`):
- The TCP connection for outbound requests is made to the proxy (which is likely public and allowed).
- The proxy then connects to the target host/IP on behalf of the gateway (e.g., `169.254.169.254`, RFC1918 ranges).
- The gateway’s `DialContext` validation checks the proxy connection target, not the ultimate destination.

This defeats the intent of “default deny” SSRF controls because the “socket layer” connection is no longer to the final destination.

**Impact**

- Gateway-managed tools become a viable SSRF primitive if a proxy is configured (intentionally or accidentally).
- In hosted environments where `HTTP_PROXY` might be set for other reasons, this is high risk.

**Recommendations**

1. In `safety.NewRestrictedHTTPClient`, explicitly disable proxy use for tool traffic:
   - Set `clone.Proxy = nil` (or a function returning `nil`) on the cloned `*http.Transport`.
   - Consider also stripping `ProxyConnectHeader` and similar proxy-related settings if present.
2. Add a regression test:
   - Verify the returned restricted client’s `Transport.(*http.Transport).Proxy` is nil even when the base client has `ProxyFromEnvironment`.
   - Optionally add an integration-style test with a local CONNECT proxy to prove blocked destinations cannot be reached through proxying.

**Code references**

- Base client uses env proxy: `pkg/gateway/server/server.go:33`
- Restricted client clones the base transport but does not disable proxy: `pkg/gateway/tools/safety/transport.go:20`, `pkg/gateway/tools/safety/transport.go:30`

---

### 2) P1 Correctness: Tool Error Results Can Lose Their Error Text Payload

**Spec quotes**

> “If a tool fails: `is_error=true` and `error` contains the canonical inner error object.” (Section 8.2 `tool_result`)

> “`tool_result` … `content`: … `[]ContentBlock` … `error`: canonical inner error object.” (Section 8.2 `tool_result`)

**What the implementation does**

In `executeTools`:
- It calls the builtin executor.
- If the builtin returns empty content, it **immediately** substitutes `content = [{"type":"text","text":""}]`.
- It then constructs the tool result.
- If the builtin also returned an error, it sets `res.IsError=true` and `res.Error=toolErr`, but the code path that would fill `res.Content` with `toolErr.Message` is now unreachable because `len(content)` is already non-zero.

Relevant lines:
- Default empty content applied: `pkg/gateway/runloop/runloop.go:278` and `pkg/gateway/runloop/runloop.go:279`
- Error branch checks `if len(content) == 0`: `pkg/gateway/runloop/runloop.go:283`, `pkg/gateway/runloop/runloop.go:286`

**Impact**

- Clients may see tool failures with `is_error=true` and a non-nil `error`, but `content` can be an empty text block, which is poor for:
  - debugging
  - log readability
  - model follow-up turns (model receives a tool_result with no useful text unless it inspects the structured error field, which many prompt patterns won’t)

**Recommendations**

1. Adjust ordering:
   - Only inject empty default content for success cases (no `toolErr`).
   - For error cases:
     - If the executor returns empty content, default to a text block containing `toolErr.Message`.
     - If it returns non-empty content, keep it as-is (some tools might produce partial results plus an error).
2. Add/extend tests:
   - A builtin executor that returns `(nil, error)` should produce a tool_result with non-empty content and `is_error=true`.

---

### 3) P1 Spec/Behavior: Blocking Run Timeout Returns HTTP Error Instead Of Run Result

**Spec quotes**

> “`/v1/runs` (blocking): Server-side tool loop. Returns a final result after the run completes.” (Section 4.3)

> “`run.timeout_ms`: must be `1000–300000` (1 second to 5 minutes).” (Section 4.3)

> “If the client disconnects or the request context is cancelled while a blocking run is in progress: … The gateway returns `408 Request Timeout` or `error.type: "api_error"` with `code: "cancelled"` …” (Section 4.3 Cancellation semantics)

**What the implementation does**

- `RunBlocking` returns `ctx.Err()` when the context is done, even though it also sets `result.StopReason` and captures history. (`pkg/gateway/runloop/runloop.go:93`)
- `apierror.FromError` maps `context.DeadlineExceeded` to a `504` error and `context.Canceled` to `408`. (`pkg/gateway/apierror/apierror.go:25`)
- Therefore, a `run.timeout_ms` timeout produces an HTTP error response (likely 504), not a `200` JSON result envelope with `stop_reason:"timeout"`.

**Impact**

- Client-side semantics become inconsistent with the run result model (which includes `stop_reason:"timeout"`).
- If clients/SDKs treat `/v1/runs` as “always returns a result envelope on success and stop reasons inside JSON,” they will be forced into transport-error handling for timeouts rather than normal stop reason handling.

**Recommendation (requires a product decision)**

Decide which contract you want, then make it explicit and consistent across blocking + streaming:

Option A (result-oriented):
- For `run.timeout_ms`, return HTTP 200 with `{ "result": { ..., "stop_reason": "timeout" } }`.
- Reserve 408/504 for client disconnect / upstream gateway request timeouts, not for run timeout policy.

Option B (error-oriented, current-ish):
- Keep returning a non-200 on timeout.
- Update the spec to explicitly say that `run.timeout_ms` is surfaced as an HTTP timeout error rather than a successful result with stop reason.

Given the spec already models `"timeout"` as a run stop reason, Option A is usually the more ergonomic API.

---

### 4) P1 Spec/Interoperability: `gemini-oauth/*` Is In Spec Header Mapping But Not Supported By Gateway

**Spec quotes**

> “`X-Provider-Key-Gemini` | `gemini/*`, `gemini-oauth/*`” (Section 3.3 Header mapping)

**What the implementation does**

- Provider header mapping does not include `gemini-oauth`. (`pkg/gateway/compat/catalog.go:215`)
- Upstream provider factory also does not support `gemini-oauth`. (`pkg/gateway/upstream/factory.go:21`)
- As a result:
  - `/v1/messages` and `/v1/runs` reject `gemini-oauth/<model>` as “unsupported provider” (because `compat.ProviderKeyHeader` returns false).
  - `/v1/models` for an allowlisted `gemini-oauth/<model>` will synthesize a model entry but omit `auth.requires_byok_header` (because `ProviderKeyHeader("gemini-oauth")` returns false). (`pkg/gateway/handlers/models.go:103`)

**Impact**

- Spec and implementation disagree, which will produce confusing client behavior if `gemini-oauth/*` is used in docs/examples.
- `/v1/models` becomes misleading for allowlisted `gemini-oauth/*` models (auth metadata omitted even though the spec states it should be discoverable).

**Recommendations**

1. Make a product decision:
   - If `gemini-oauth` is intended only for local direct-mode SDK and not for gateway proxy mode:
     - Remove `gemini-oauth/*` from the gateway spec header mapping, or explicitly mark it “direct mode only”.
   - If it should be supported by the gateway:
     - Add `gemini-oauth` to `compat.ProviderKeyHeader` (if BYOK is the desired auth scheme), and add an upstream adapter in `pkg/gateway/upstream/factory.go`.
2. Add a test case for `/v1/models` allowlist containing `gemini-oauth/foo` to ensure auth metadata matches the chosen policy.

---

### 5) P2 Spec/UX: Voice Streaming Events Are Wrapped Inside `stream_event` For Runs

**Spec quotes**

> “The `event` field within a `stream_event` wrapper … is exactly a `types.StreamEvent` JSON object.” (Section 7.9)

> “Wraps `pkg/core/types.StreamEvent` objects from the underlying provider stream.” (Section 8.2 `stream_event`)

> “When voice output is enabled, the gateway also emits `audio_chunk` events interleaved with text events.” (Section 4.2)

**What the implementation does**

- For `/v1/runs:stream`, the implementation emits `audio_chunk` and `audio_unavailable` as `types.RunStreamEventWrapper{Type:"stream_event", Event: <audio event>}`. (`pkg/gateway/runloop/runloop.go:344`, `pkg/gateway/runloop/runloop.go:414`)

This still satisfies the “exactly a StreamEvent JSON object” requirement, but it stretches the “provider stream” phrasing.

**Impact**

- Client parsing is still consistent (unwrap -> parse StreamEvent), but:
  - clients might incorrectly assume `stream_event.event` is provider-originated only
  - tooling that wants to “replay provider stream” will now include gateway-generated voice events

**Recommendations**

Pick one and document it clearly:

Option A (clarify spec):
- Update spec wording to say `stream_event` may wrap **provider-normalized and gateway-generated StreamEvents** (especially voice).

Option B (separate run-level voice events):
- Emit run-level `audio_chunk` and `audio_unavailable` as top-level run events (parallel to `tool_result`, `step_complete`, etc.) instead of embedding within `stream_event`.

---

### 6) P2 Reliability: Restricted Tool Transport Likely Breaks For IPv6 Literals

**Spec quotes**

> “Blocked destinations … Private/link-local IPv6 …” and “IPv4-mapped IPv6 … normalize to IPv4 before checking, or block the entire range.” (Section 11.7)

**What the implementation does**

- In `DialContext`, the code constructs `u := "https://" + host` and passes it to `ValidateTargetURL`. (`pkg/gateway/tools/safety/transport.go:41`)
- For IPv6 literals, the valid URL form is bracketed, e.g. `https://[2001:db8::1]`.
- `host` from `net.SplitHostPort` is not bracketed; concatenating yields an invalid URL, causing `ValidateTargetURL` to fail even for public IPv6 destinations.

**Impact**

- Tool outbound HTTP may fail unexpectedly in IPv6-heavy environments (or when upstream resolves to IPv6 and the transport chooses IPv6 literals).

**Recommendations**

1. Avoid constructing URL strings inside `DialContext`. Instead:
   - Validate by IP directly when `host` parses as IP (including v6), using the existing `validateIP` logic.
   - For hostnames, perform resolution and IP validation (which you already do later in the function) without the intermediate `ValidateTargetURL` call.
2. Add unit coverage for IPv6:
   - A test that exercises the validation path for `host="2001:db8::1"` should succeed (since it’s public) and fail for link-local/private IPv6.

---

### 7) P2 Config Consistency: `/v1/runs:stream` Does Not Enforce `VAI_PROXY_SSE_MAX_DURATION`

**Spec quotes**

> “Default maximum SSE stream duration: 5 minutes (configurable).” (Section 7.4)

> “When either limit is reached: The gateway closes the upstream provider stream. Emits a terminal `error` event …” (Section 7.4)

**What the implementation does**

- `/v1/messages` streaming enforces `SSEMaxStreamDuration` by wrapping request context with `context.WithTimeout(ctx, h.Config.SSEMaxStreamDuration)`. (`pkg/gateway/handlers/messages.go:133`)
- `/v1/runs:stream` sets a timeout only based on `run.timeout_ms`. (`pkg/gateway/handlers/runs.go:186`)
- Because `UnmarshalRunRequestStrict` defaults `run.timeout_ms` to 60000ms, runs streams currently default to ~60s, not the SSE default 5m. (`pkg/core/types/strict_decode_runs.go:63`)

**Impact**

- The gateway has two “maximum stream duration” knobs:
  - `VAI_PROXY_SSE_MAX_DURATION` for `/v1/messages`
  - `run.timeout_ms` for `/v1/runs:stream`
- This may be intended, but it is inconsistent with the spec’s single SSE duration concept unless explicitly documented.

**Recommendations**

1. Make intent explicit:
   - If run timeout is the authoritative cap for `/v1/runs:stream`, document that `run.timeout_ms` governs stream duration for runs.
2. Or enforce both:
   - Use `min(run.timeout_ms, VAI_PROXY_SSE_MAX_DURATION)` for the effective stream duration cap.

## Positive Notes (Spec Alignment)

- `/v1/models` cache semantics match spec guidance (`private` when tenant-filtered via allowlist; `public` otherwise). (`pkg/gateway/handlers/models.go:58`)
- Compatibility validation follows “strict only when known unsupported; pass-through when unknown model/provider.” (`pkg/gateway/compat/validate.go:145`)
- Run strict decoding enforces bounds and rejects unknown fields, including `request.stream=true`. (`pkg/core/types/strict_decode_runs.go:35`, `pkg/core/types/strict_decode_runs.go:59`)
- Builtins injection and collision handling follow the spec’s “referenced not defined” + collision rejection rules. (`pkg/gateway/tools/builtins/registry.go:74`)
- SSRF primitives are present (blocked CIDRs, percent decoding, URL credential rejection, redirect hop limit, response size limits). (`pkg/gateway/tools/safety/url.go:18`, `pkg/gateway/tools/safety/transport.go:75`)

## Remediation Progress (Completed 2026-02-25)

1. **P0 proxy bypass** — Completed.
   - Implemented:
     - `pkg/gateway/tools/safety/transport.go`: restricted transport now sets `Proxy=nil`, clears proxy CONNECT headers/callback, and enforces direct validated dialing.
   - Tests:
     - `pkg/gateway/tools/safety/transport_test.go::TestNewRestrictedHTTPClient_DisablesProxy`.

2. **P1 tool error content loss** — Completed.
   - Implemented:
     - `pkg/gateway/runloop/runloop.go`: tool-result content fallback now distinguishes success vs error paths; `(nil, error)` now yields text content from error message.
   - Tests:
     - `pkg/gateway/runloop/runloop_test.go::TestRunBlocking_ToolErrorAddsNonEmptyContent`.

3. **P1 blocking timeout behavior** — Completed.
   - Implemented:
     - `pkg/gateway/handlers/runs.go`: blocking run timeouts (`context.DeadlineExceeded` from run policy timeout) now return normal `types.RunResultEnvelope` with `stop_reason:"timeout"` and HTTP 200.
   - Tests:
     - `pkg/gateway/handlers/runs_test.go::TestRunsHandler_BlockingRunTimeoutReturnsResult`.

4. **P1 gemini-oauth interoperability** — Completed.
   - Implemented:
     - `pkg/gateway/compat/catalog.go`: `ProviderKeyHeader("gemini-oauth") -> X-Provider-Key-Gemini`.
     - `pkg/gateway/upstream/factory.go`: added `gemini-oauth` factory branch.
     - `pkg/gateway/compat/validate.go`: added `gemini-oauth` policy parity with `gemini`.
   - Tests:
     - `pkg/gateway/compat/catalog_test.go::TestProviderKeyHeader_GeminiOAuth`.
     - `pkg/gateway/upstream/factory_test.go::TestFactoryNew_GeminiOAuth`.
     - `pkg/gateway/handlers/models_test.go::TestModelsHandler_AllowlistFiltersAndSynthesizesUnknown` (gemini-oauth auth metadata assertion).
     - `pkg/gateway/handlers/runs_test.go::TestRunsHandler_GeminiOAuthProviderAccepted`.

5. **P2 voice event wrapping in runs stream** — Completed.
   - Implemented:
     - `pkg/gateway/runloop/runloop.go`: gateway-generated voice events are emitted as top-level run events (`audio_chunk`, `audio_unavailable`), while provider stream events remain wrapped as `stream_event`.
   - Tests:
     - `pkg/gateway/runloop/runloop_test.go::TestRunStream_VoiceEventsAreTopLevelRunEvents`.

6. **P2 IPv6 literal handling in restricted transport** — Completed.
   - Implemented:
     - `pkg/gateway/tools/safety/transport.go`: replaced URL-string reconstruction in dial path with direct host/IP validation helper (`validateDialTarget`), eliminating IPv6 literal parse breakage.
   - Tests:
     - `pkg/gateway/tools/safety/transport_test.go::TestValidateDialTarget_IPv6LiteralAllowed`.
     - `pkg/gateway/tools/safety/transport_test.go::TestValidateDialTarget_RejectsBlockedIPv6`.

7. **P2 run stream max-duration consistency** — Completed.
   - Implemented:
     - `pkg/gateway/handlers/runs.go`: added `effectiveRunStreamTimeout()` and enforced effective timeout as `min(run.timeout_ms, VAI_PROXY_SSE_MAX_DURATION)`.
   - Tests:
     - `pkg/gateway/handlers/runs_test.go::TestRunsHandler_Stream_UsesMinOfRunTimeoutAndSSEMaxDuration`.

Validation:
- Full suite passes: `go test ./...`.
