AI 1 Plan:

"""
## Stage 3 Optimized Plan: Live Interruption, Immediate Stop, and Timestamp-Based `talk_to_user` Truncation

### Summary
Implement interruption as a deterministic extension of stage 2:
1. Keep stage 2 behavior unchanged for `<5s` grace cancellations.
2. Add a separate `interrupt` cancellation path for `>5s` while assistant audio is active.
3. Stop playback immediately on interrupt.
4. Truncate only `talk_to_user.input.content` to what was played, then append ` [user interrupt detected]`.
5. Preserve all other context/history exactly, including tool consistency.

This plan is Cartesia-first and backward-compatible with current live clients.

---

### Product Rules (Locked)
1. Confirmed interruption trigger is STT text (non-empty transcript), not raw audio energy.
2. If interruption occurs within 5s and existing stage-2 conditions pass, run stage-2 grace cancel + audio aggregation exactly as today.
3. If interruption occurs after 5s while assistant audio is active and unfinished, cancel as `interrupt`, do not aggregate prior user audio, and commit truncated assistant speech.
4. Inside grace, non-`talk_to_user` tool calls still block cancel (existing safety).
5. Outside grace, interruption is allowed even if non-`talk_to_user` tools were called.
6. Marker suffix is exactly ` [user interrupt detected]`.

---

### Public/API and Interface Changes

#### 1) Live playback progress over existing frame
File: [pkg/core/types/live.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/types/live.go)

1. Extend `LivePlaybackStateFrame` with optional `played_ms`.
2. Expand supported `state` values to `playing | finished | stopped`.
3. Keep current compatibility behavior when `played_ms` is absent.

#### 2) Formal interrupt reason on existing cancel event
File: [pkg/core/types/live.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/types/live.go)

1. Reuse existing `LiveTurnCancelledEvent` and standardize `reason` values: `grace_period` and `interrupt`.
2. No new cancel frame type is required.

#### 3) TTS timestamp plumbing (internal API)
File: [pkg/core/voice/tts/provider.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/provider.go)

1. Add `WordTimestamps` type with normalized millisecond timings.
2. Add optional fields to `StreamingContextOptions`: `AddTimestamps`, `UseNormalizedTimestamps`.
3. Add timestamp channel support on `StreamingContext`: `Timestamps()`, internal `PushTimestamps`, internal `FinishTimestamps`.
4. All additions are optional and non-breaking for providers that do not emit timestamps.

#### 4) Cartesia timestamp support
File: [pkg/core/voice/tts/cartesia.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia.go)

1. Send `add_timestamps=true` and `use_normalized_timestamps=true` for live TTS contexts.
2. Parse `type:"timestamps"` responses and publish normalized word timing batches to `StreamingContext`.
3. Preserve current chunk/done/error behavior.

#### 5) Streaming wrapper passthrough
File: [pkg/core/voice/streaming_tts.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/streaming_tts.go)

1. Add `Timestamps()` passthrough for live handler consumption.

---

### Gateway Design and Implementation

File: [pkg/gateway/handlers/live.go](/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go)

#### 1) Extend turn runtime state
Add the following to `liveTurnRuntime`:
1. `cancelReason string` (`"" | "grace_period" | "interrupt"`).
2. `playbackPlayedMS int` (latest client-reported played position, monotonic).
3. `audioSentMS int` (server-tracked emitted audio duration for fallback truncation).
4. `baseHistory []types.Message` (history snapshot at turn start).
5. `turnUserMessage types.Message` (committed user audio message used for this turn).
6. `historyDelta []types.Message` (applied `RunHistoryDeltaEvent` appends for in-flight reconstruction).
7. `spoken *liveSpokenTracker` with `talkCallID`, `talkFullText`, `wordMarks`.

#### 2) Playback-state ingestion
Update control frame parsing:
1. Accept `playback_state.state="playing"` in addition to existing states.
2. On `playing`, update `activeTurn.playbackPlayedMS = max(old, played_ms)`.
3. On `finished|stopped`, update played position if present and set `playbackFinished=true`.

#### 3) Unified cancel decision function
Add a single decision helper called by `sttLoop`:
1. Return `{cancel bool, reason string, appendPrefix bool}`.
2. `reason=grace_period` only when stage-2 conditions pass.
3. `reason=interrupt` only when `audioStarted=true`, `playbackFinished=false`, and now is outside grace.
4. `appendPrefix=true` only for grace path.
5. `appendPrefix=false` for interrupt path.

#### 4) STT loop behavior
On confirmed transcript text:
1. Keep current `sttSawText` and `lastSpeechAt` updates.
2. Evaluate cancel decision.
3. If cancel:
   - Set `activeTurn.lifecycle=cancelled`.
   - Set `activeTurn.cancelReason`.
   - Set or clear `aggregatePrefix` based on decision.
   - Cancel run context.
   - Emit `turn_cancelled` with reason.
4. If no cancel, continue normal stage-2 behavior.

#### 5) Talk capture and timestamp capture
In `liveTalkTurnState`:
1. Capture `talk_to_user` streamed argument deltas into `turn.spoken.talkFullText`.
2. Capture talk tool call ID from tool metadata (`call_id` / tool block ID).
3. Consume TTS timestamps and append normalized word marks into `turn.spoken.wordMarks`.
4. Continue streaming audio/text as currently implemented until cancel.

#### 6) Audio emission accounting
In `forwardTTSAudio`:
1. For each emitted audio chunk, update `turn.audioSentMS` from chunk byte size and sample rate.
2. Mark `audioStarted=true` as today.

#### 7) Interrupted turn finalization path
In `runTurn`, when controller returns `context.Canceled` and turn is cancelled:
1. If `cancelReason=grace_period`, preserve current stage-2 behavior and return without committing turn history.
2. If `cancelReason=interrupt`, build interrupted history and emit `turn_complete`.

Interrupted history building algorithm:
1. Base candidate history:
   - Use `pendingResult.history` when available.
   - Else build from `baseHistory + turnUserMessage + historyDelta`.
2. Compute truncated talk text:
   - Primary: map `playbackPlayedMS` to word timing marks and cut at last played word boundary.
   - Secondary fallback: use `audioSentMS` ratio against `talkFullText` length.
   - Final fallback: marker-only text.
3. Append marker suffix.
4. Patch only `talk_to_user.input.content` in the last matching assistant `tool_use`.
5. Ensure matching `tool_result` for that talk call exists; append synthetic delivered result if missing.
6. Commit `s.history` and emit `turn_complete` with `stop_reason=cancelled`.

#### 8) Truncation helper details
Add deterministic helper functions:
1. Normalize token matching between provider words and original content.
2. Map word index to original content rune index.
3. Preserve immediate trailing punctuation when safe.
4. Return trimmed text plus marker.
5. Guarantee non-empty output for interrupted talk by returning marker-only when no played segment maps.

---

### Proxy Chatbot Client Design and Implementation

Files:
- [cmd/proxy-chatbot/live_mode.go](/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode.go)
- [cmd/proxy-chatbot/audio_io.go](/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/audio_io.go)

#### 1) Emit periodic playback progress
1. Track per active audio turn: sample rate, bytes queued/written, playback start time, last sent played value.
2. Emit `playback_state{state:"playing", played_ms}` every 100ms while audio is open.
3. Include final `played_ms` on `stopped` and `finished`.

#### 2) Immediate stop on interrupt reason
1. On `turn_cancelled(reason="interrupt")`, hard-stop player immediately (no silence padding).
2. Add `pcmPlayer.Kill()` for force-stop path.
3. Send immediate `playback_state{state:"stopped", played_ms}` after kill.

#### 3) Grace cancel behavior unchanged
1. On `turn_cancelled(reason="grace_period")`, keep existing close/finalize flow.

#### 4) Event ignore policy update
1. Continue ignoring late deltas/chunks/tool calls for cancelled turns.
2. For interrupt-cancelled turns, allow `turn_complete` processing so local history syncs immediately.
3. For grace-cancelled turns, keep current ignore behavior.

---

### Implementation Phases (Execution Order)

1. Add type/API extensions in `pkg/core/types/live.go`.
2. Add TTS timestamp channel/options in `pkg/core/voice/tts/provider.go`.
3. Add Cartesia timestamp request/parse in `pkg/core/voice/tts/cartesia.go`.
4. Add `StreamingTTS.Timestamps()` in `pkg/core/voice/streaming_tts.go`.
5. Add gateway turn runtime fields and cancel decision helper in `pkg/gateway/handlers/live.go`.
6. Add playback-state `played_ms` ingestion and `playing` handling in gateway.
7. Add talk/timestamp capture and truncation helpers in gateway.
8. Add interrupted history commit path in `runTurn`.
9. Add client playback progress emission in `cmd/proxy-chatbot/live_mode.go`.
10. Add force-stop player and interrupt-specific handling in client.
11. Update docs in:
   - [LIVE_AUDIO_MODE_DESIGN.md](/Users/collinshill/Documents/projects/vai-lite/LIVE_AUDIO_MODE_DESIGN.md)
   - [README.md](/Users/collinshill/Documents/projects/vai-lite/README.md)

---

### Test Plan

#### 1) Gateway logic tests
File: [pkg/gateway/handlers/live_test.go](/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live_test.go)

1. Cancel matrix:
   - `audioStarted + <=5s + no non-talk` => grace cancel.
   - `audioStarted + <=5s + non-talk` => no cancel.
   - `audioStarted + >5s` => interrupt cancel.
   - `audioStarted + >5s + non-talk` => interrupt cancel.
   - `!audioStarted + >5s` => no cancel.
2. Playback mark parsing:
   - accepts `state=playing`.
   - updates monotonic `played_ms`.
3. Interrupt finalization:
   - emits `turn_cancelled(reason=interrupt)`.
   - emits `turn_complete(stop_reason=cancelled)` with patched talk content marker.
4. Grace regression:
   - grace cancellation still clears pending and uses aggregate prefix.
5. Tool consistency:
   - interrupted history never leaves dangling `talk_to_user` tool use without tool result.

#### 2) Truncation helper tests
File: [pkg/gateway/handlers/live_test.go](/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live_test.go) or extracted helper test file.

1. Exact cut at word boundary.
2. Mid-word playback rounds down to previous whole word.
3. Repeated word matching robustness.
4. Punctuation retention.
5. No timestamp fallback behavior.

#### 3) Cartesia provider tests
File: [pkg/core/voice/tts/cartesia_test.go](/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia_test.go) (new)

1. `timestamps` event parsing to channel.
2. Timestamp channel closes cleanly on done/close.
3. No regression on chunk/done/error handling.

#### 4) Client live mode tests
File: [cmd/proxy-chatbot/live_mode_test.go](/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode_test.go)

1. Emits periodic `playback_state(state=playing, played_ms=...)`.
2. Emits final played mark on stopped/finished.
3. Interrupt reason triggers hard kill path.
4. Grace reason keeps existing stop path.
5. Interrupt-cancelled turn still accepts subsequent `turn_complete` for history sync.

#### 5) Audio player tests
File: [cmd/proxy-chatbot/audio_io_test.go](/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/audio_io_test.go)

1. `Kill()` closes process without padding.
2. Existing `Close()` behavior remains unchanged.

---

### Acceptance Scenarios

1. Story interruption outside grace:
   - Assistant speaks.
   - User interrupts after 5s.
   - Playback stops immediately.
   - History stores truncated `talk_to_user` with marker.
   - Next user speech is a new turn.

2. Stage-2 grace preserved:
   - User resumes within 5s.
   - Turn canceled as grace.
   - Aggregated user audio reruns.
   - No interrupted assistant content committed.

3. Non-talk tool safety:
   - Within grace and non-talk tool executed => no cancel.
   - Outside grace and audio active => interrupt cancel allowed.

4. Late audio chunk race:
   - After interrupt cancel, late chunks do not play.
   - History remains coherent.

---

### Assumptions and Defaults

1. Provider scope is Cartesia-only for stage 3.
2. `played_ms` is best-effort client telemetry; server uses last known value.
3. If timestamps are unavailable, fallback truncation is conservative.
4. Marker text remains fixed and user-visible in history: ` [user interrupt detected]`.
5. All protocol changes are additive and backward-compatible.
"""

AI 2 Plan: 

"""
# Stage 3 — Live Audio Interruptions (Barge‑In) With Playback‑Accurate Truncation

## Summary
Implement “barge‑in” interruption for `/v1/live` so that when the user starts speaking while assistant audio is playing:
1. Assistant audio stops immediately (client drops buffered audio).
2. The server determines **how much audio actually played** using **client playback marks** (`played_ms`) plus **Cartesia word timestamps**.
3. The server finalizes the interrupted turn by **truncating only** `talk_to_user.input.content` to what was played and appending: ` [user interrupt detected]`.
4. Stage 2 grace aggregation remains authoritative **within 5 seconds** when its cancellation conditions are met (your chosen rule): in that case we **discard/cancel + append** and do **not** save truncated assistant content.

This plan is designed specifically for the current repo implementation:
- Server: `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go`
- Reference client: `/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode.go`
- TTS: Cartesia streaming websocket in `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia.go`

---

## Goals
- **Stop audio quickly** and deterministically (flush client buffers; drop late server chunks).
- **Truncate the exact `talk_to_user.content` string** (not a reconstructed approximation).
- Preserve existing **Stage 2 grace behavior** and constraints:
  - Within 5s, cancel only if assistant playback hasn’t finished AND no non-`talk_to_user` tools have run.
- Keep history coherent for the next turn (no dangling tool calls; deterministic message ordering).

## Non‑Goals (v1 Stage 3)
- Server-side energy/RMS “instant stop” (too false-positive without AEC).
- Perfect truncation without timestamps or playback marks (we degrade conservatively).
- Full multi-provider alignment support (ElevenLabs alignment can be added later).

---

## Key Behavioral Rules (Decision Complete)

### A) Grace-cancel still wins inside grace window (your choice)
When STT produces confirmed text while an assistant turn is grace-cancelable:
- Do exactly Stage 2:
  - cancel the in-progress turn
  - append new PCM to the original via `aggregatePrefix`
  - emit `turn_cancelled(reason="grace_period")`
  - **no truncated assistant history is saved**

### B) Interrupt path applies when grace-cancel does NOT apply
If the assistant is playing audio and the user speaks, but the turn is **not** grace-cancelable (deadline passed, or non-talk tools ran, or playback already finished, etc.):
- Treat as interrupt:
  - emit `audio_reset(reason="barge_in")` immediately
  - freeze truncation snapshot (`interruptPlayedMS`, collected timestamps, collected talk text)
  - stop sending further `audio_chunk`/text deltas for that turn
  - cancel the run if still running
  - finalize the turn with `turn_complete` where only `talk_to_user.input.content` is truncated + marker

### C) Interrupt does **not** use `turn_cancelled`
`turn_cancelled` is reserved for Stage 2 grace aggregation semantics (client ignores that turn entirely today). Interrupt must still produce a `turn_complete` that updates history.

---

## 1) Wire Protocol Additions (Backward-Compatible)

### 1.1 Client → Server: `playback_mark` (progress)
Add a new control frame so the server can know what was actually played.
- File: `/Users/collinshill/Documents/projects/vai-lite/pkg/core/types/live.go`
- New frame:
```json
{"type":"playback_mark","turn_id":"turn_123","played_ms":1875}
```

Rules:
- `played_ms` is monotonic and **turn-relative**.
- Client SHOULD send every ~250ms while playing and once at stop/finish.

### 1.2 Server → Client: `audio_reset` (hard stop)
Add a server event instructing client to drop buffered audio immediately for a turn.
- File: `/Users/collinshill/Documents/projects/vai-lite/pkg/core/types/live.go`
- New event:
```json
{"type":"audio_reset","turn_id":"turn_123","reason":"barge_in"}
```

Client must:
- stop playback immediately (no “drain silence”)
- ignore any later `audio_chunk` for that `turn_id`
- send `playback_state{state:"stopped"}` (existing frame) after stopping

Backward compatibility:
- Old clients ignore `audio_reset`; server still cancels run and truncates history, but audio may trail on old clients (acceptable).

---

## 2) Cartesia TTS: Word Timestamp Support (for truncation)

### 2.1 Extend streaming context API to surface timestamps
- Files:
  - `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/provider.go`
  - `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia.go`

Add to `tts.StreamingContextOptions`:
- `AddTimestamps bool`
- `UseNormalizedTimestamps bool` (Cartesia supports it; default false)

Add to `tts.StreamingContext`:
- `timestampsCh chan WordTimestampsBatch`
- `Timestamps() <-chan WordTimestampsBatch`
- internal `PushTimestamps(batch)` + `FinishTimestamps()` (closed on context end)

Where:
```go
type WordTimestampsBatch struct {
  Words    []string
  StartSec []float64
  EndSec   []float64
}
```

### 2.2 Cartesia request/response wiring
In `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia.go`:
- Add fields to `cartesiaStreamingRequest`:
  - `AddTimestamps bool  'json:"add_timestamps,omitempty"'`
  - `UseNormalizedTimestamps bool 'json:"use_normalized_timestamps,omitempty"'`
- When live mode creates a streaming context, set:
  - `AddTimestamps=true`
  - `UseNormalizedTimestamps=true`
- Extend `cartesiaWSResponse` to parse:
  - `type:"timestamps"` with `word_timestamps:{words,start,end}`
- Push timestamp batches onto `StreamingContext.timestampsCh`.

No cancel-context API required (Cartesia cancel doesn’t stop in-flight generation); closing the WS is sufficient and we will also drop late chunks at the live handler layer.

---

## 3) Voice Streaming Wrapper: expose timestamps to live handler
- File: `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/streaming_tts.go`
Add:
- `func (s *StreamingTTS) Timestamps() <-chan tts.WordTimestampsBatch` which forwards `s.ttsCtx.Timestamps()`.

This avoids leaking internal `ttsCtx` plumbing into the live handler.

---

## 4) Gateway `/v1/live`: Server-Side Interruption Implementation

### 4.1 Track per-turn playback progress + interrupt snapshot
- File: `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go`

Extend `liveTurnRuntime` with:
- `playedMS int` (last client mark)
- `interruptRequested bool`
- `interruptPlayedMS int` (snapshot at interrupt moment)
- `interruptFrozen bool` (freeze talk text/timestamps collection)
- `talkCallID string` (tool_use id for talk_to_user)
- `talkFullText strings.Builder` (exact `content` string reconstructed from streamed args)
- `talkTimestamps []WordTimestampsBatch` (batches accumulated until freeze)
- `suppressOutgoing bool` (drop audio/text for this turn after interrupt)

### 4.2 Parse `playback_mark`
Add control frame handling:
- Update `handleControlFrame()` in `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go` to accept `"playback_mark"`.
- Validate:
  - `turn_id` non-empty
  - `played_ms >= 0`
- If `activeTurn.id == turn_id`, set `activeTurn.playedMS = max(activeTurn.playedMS, played_ms)`.

### 4.3 Detect interrupt vs grace-cancel (STT confirmed speech)
Modify `sttLoop()` in `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go`:

On non-empty transcript delta:
1. Keep existing bookkeeping: `sttSawText=true`, `lastSpeechAt=now`.
2. If there is an active turn and assistant audio is actively playing:
   - condition: `activeTurn.audioStarted && !activeTurn.playbackFinished`
3. If `isGraceCancelableLocked(activeTurn, now)` is true:
   - do existing Stage 2 cancellation (unchanged)
4. Else:
   - perform interrupt exactly once per turn:
     - if `activeTurn.interruptRequested` already true: do nothing
     - set:
       - `interruptRequested=true`
       - `interruptPlayedMS=activeTurn.playedMS`
       - `interruptFrozen=true`
       - `suppressOutgoing=true`
     - emit `audio_reset{turn_id, reason:"barge_in"}`
     - cancel `activeTurn.runCancel` if non-nil (stops run quickly)
     - trigger immediate finalize (see 4.5)

Important: do **not** set `aggregatePrefix` for interrupt.

### 4.4 Freeze outbound streaming immediately after interrupt
Update live streaming paths to check `turn.suppressOutgoing`:
- In `liveTalkTurnState.handleStreamEvent()`:
  - Before sending `assistant_text_delta` or `talk_to_user_text_delta`, check `session` active turn is suppressed for this `turn_id`; if suppressed, drop the event.
  - For `talk_to_user` deltas: only append into `turn.talkFullText` if `!turn.interruptFrozen`.
- In `liveTalkTurnState.forwardTTSAudio()`:
  - Before sending each `audio_chunk`, check suppression; if suppressed, return and close TTS context quickly.
  - This ensures the server stops emitting audio even if provider streams extra chunks.

### 4.5 Finalization behavior for interrupts
We must finalize immediately (no grace waiting) and produce `turn_complete` with truncated history.

There are two runtime situations:

**Case 1 — controller already completed and we’re awaiting grace (`pendingResult != nil`):**
- We already have `pendingResult.history` with a full assistant tool_use talk_to_user.
- Mutate only that talk_to_user block:
  - compute `truncated := truncateTalk(turn.talkFullText, turn.talkTimestamps, turn.interruptPlayedMS)`
  - set `Input["content"] = truncated + " [user interrupt detected]"` (or marker-only if empty)
- Force finalize now:
  - bypass grace checks for interrupted turns (explicit branch in `shouldFinalizeTurnNow()` and `turnFinalizeStatus()`).

**Case 2 — controller was still running and got canceled:**
- Today `runTurn` returns early on `context.Canceled` for grace-cancel. For interrupt we change that:
  - If `err != nil`, `errors.Is(err, context.Canceled)` and `turn.interruptRequested == true`:
    - Use `result.Messages` as the base history snapshot (it should include the user turn transcript because the controller preprocess happens before streaming).
    - If the snapshot already contains a talk_to_user tool_use for this turn, mutate it; else append:
      1) assistant message containing a `ToolUseBlock{Name:"talk_to_user", ID:turn.talkCallID, Input:{"content": truncated+marker}}`
      2) user message containing matching `ToolResultBlock{ToolUseID:turn.talkCallID, Content:[{"type":"text","text":"delivered"}]}`
    - Set `pendingResult.history` to this updated history and finalize immediately via `turn_complete`.

This satisfies:
- “everything else stays the same” (base history snapshot from controller)
- “only truncate talk_to_user argument” (we only touch that field; we do not rewrite other blocks)

### 4.6 Truncation algorithm (deterministic, exact-string cut)
Create helper in `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live.go` (or a small internal file in the same package):

**Inputs**
- `fullText` (exact, accumulated `talk_to_user.content`)
- `word timestamp batches` (from provider)
- `playedMS` (from playback marks snapshot)

**Output**
- exact prefix of `fullText` that corresponds to words with `end_time <= playedMS`, plus punctuation retention.

**Steps**
1. Flatten timestamp batches into ordered list of `(wordNorm, endMS)`.
2. Find `lastTSIndex` where `endMS <= playedMS`. If none: return `""`.
3. Tokenize `fullText` into ordered tokens with spans:
   - token = contiguous letters/digits (keep internal `'` and `-`)
   - store `start,end,norm`
4. Greedy sequential aligner:
   - walk timestamp words and match to the next token with the same `norm` (allow limited lookahead, e.g. 8 tokens) to handle punctuation/quotes.
   - record mapping `tsIndex -> tokenEndChar`.
5. Determine cut position = mapping for `lastTSIndex`. If missing, degrade conservatively to `""`.
6. Extend cut to include immediately-following punctuation/quotes in the original string up to whitespace.
7. `cut = strings.TrimRight(fullText[:cutPos], " \n\t")`

**Marker append**
- If `cut == ""`: output should still include marker when stored: `" [user interrupt detected]"` (exactly as requested, leading space).
- Otherwise: `cut + " [user interrupt detected]"`.

**Fallback rule (when timestamps unavailable/mapping fails)**
- Do not guess a cut point using bytes/time. Return `""` (marker-only). This is safer than claiming the user heard text they didn’t.

---

## 5) Proxy Chatbot Client: Playback Marks + Hard Reset

### 5.1 Send `playback_mark` periodically (best-effort authority)
- File: `/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode.go`

Add a playback tracker per `turn_id`:
- On first `audio_chunk` for a turn:
  - set `turnPlaybackStart = time.Now()`
  - reset `bytesWritten=0`
- On each chunk write:
  - `bytesWritten += len(decodedPCM)`
- Derived:
  - `audioDurationMS = bytesWritten / (sampleRateHz * 2) * 1000`
  - `elapsedMS = time.Since(turnPlaybackStart).Milliseconds()`
  - `playedMS = min(elapsedMS, audioDurationMS)`
  - optionally subtract a small conservative margin (e.g. 50–150ms) but clamp at 0
- Every 250ms while `turnAudioOpen`:
  - send `{"type":"playback_mark","turn_id":..., "played_ms": playedMS}`

Also send one final `playback_mark` immediately before sending `playback_state finished/stopped`.

### 5.2 Handle `audio_reset` immediately
- File: `/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode.go`

On `audio_reset{turn_id}`:
- stop playback immediately (kill process, do not call the draining `Close()` path)
- mark that `turn_id` is “reset/ignored for audio” so any late `audio_chunk` is dropped
- send existing `playback_state{state:"stopped"}` for that `turn_id`

Implementation detail:
- Add a `Kill()` method to `/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/audio_io.go` (or equivalent) for immediate stop without padding silence.

---

## 6) Tests (must be added/updated)

### 6.1 Gateway unit tests: truncation
- New tests in `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/`:
  - exact word boundary cut
  - punctuation retention
  - repeated tokens
  - no timestamps → marker-only
  - playedMS before first word → marker-only

### 6.2 Gateway live tests: interrupt vs grace matrix
- File: `/Users/collinshill/Documents/projects/vai-lite/pkg/gateway/handlers/live_test.go`

Add tests:
1. **Grace-cancel wins**:
   - active turn is grace-cancelable (within deadline, no nonTalkToolCalled, playback not finished)
   - STT delta arrives while audioStarted=true
   - expect `turn_cancelled(reason="grace_period")` and `aggregatePrefix` set (existing behavior)
   - ensure **no** `audio_reset`
2. **Interrupt outside grace**:
   - active turn not grace-cancelable but audio playing
   - playback_mark sets played_ms
   - STT delta arrives
   - expect `audio_reset`
   - expect `turn_complete` history contains truncated talk_to_user content + marker
3. **Interrupt while pendingResult awaiting grace**:
   - set pendingResult + talkFullText/timestamps
   - interrupt triggers immediate finalize (no grace waiting)

### 6.3 Proxy-chatbot tests
- File: `/Users/collinshill/Documents/projects/vai-lite/cmd/proxy-chatbot/live_mode_test.go`
Add:
- playback_mark frames emitted and monotonic
- audio_reset kills player and ignores late audio chunks but still processes turn_complete

---

## Public API / Type Changes
- `/Users/collinshill/Documents/projects/vai-lite/pkg/core/types/live.go`
  - add `LivePlaybackMarkFrame`, `LiveAudioResetEvent`
- `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/provider.go`
  - add timestamps options + `StreamingContext.Timestamps()`
- `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/tts/cartesia.go`
  - send `add_timestamps`, parse `timestamps` response
- `/Users/collinshill/Documents/projects/vai-lite/pkg/core/voice/streaming_tts.go`
  - expose timestamps forwarding

All additive; no breaking changes to existing JSON frame shapes.

---

## Acceptance Scenarios (what “done” means)
1. **Grace path unchanged (<5s, cancelable):**
   - user speaks again quickly → turn_cancelled(grace_period) → audio stops → next commit is aggregated → assistant responds to combined message
2. **Interrupt path (not cancelable):**
   - user speaks while assistant audio playing → `audio_reset` stops audio quickly → server finalizes with truncated talk_to_user + marker → next user utterance becomes a new turn
3. **Late chunks safety:**
   - after audio_reset, neither client nor server allows old-turn audio to continue playing
4. **History correctness:**
   - next LLM turn never sees assistant content beyond what was played (talk_to_user content truncated)

---

## Explicit Assumptions / Defaults
- Interrupt trigger is **STT-confirmed text delta**, not energy threshold.
- Marker is appended exactly as: ` [user interrupt detected]`.
- If timestamps or playback marks are missing/unreliable, truncation degrades to marker-only (never over-claims).
- We truncate the **most recent** `talk_to_user` tool_use in the interrupted turn (if multiple exist).
"""
