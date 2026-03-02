# Live Playback Fix Notes (Proxy Chatbot)

## Context
`cmd/proxy-chatbot` live mode (`/live`) was receiving STT and TTS correctly, but assistant audio was silent on macOS with a Bluetooth headset.

Observed behavior:
- Transcript and captions worked.
- Live audio events showed valid chunks with non-zero bytes and peak values.
- Local playback failed and got disabled for the session.

## What Failed
The playback process was `ffplay`-based, and the implementation used arguments that were not compatible with the user’s installed ffplay build (`8.0.1` from Homebrew).

Two concrete failures from logs:
- `Failed to set value '-fflags' for option 'nostdin': Option not found`
- `Failed to set value '1' for option 'ac': Option not found`

Result:
- `ffplay` exited with status 1.
- The chatbot correctly disabled playback after error handling.
- Live session continued (text/transcript/tool flow stayed healthy), but no audible output.

## Root Cause
Two combined issues:
1. **Argument compatibility mismatch** for this ffplay build (`-nostdin`, `-ac`).
2. **Playback lifecycle fragility** for streamed segments; the pipeline needed deterministic per-segment finalization and reset behavior.

## Solution Implemented
### 1) Simplified and hardened playback path
- Live playback is now ffplay-only in `proxy-chatbot`.
- Removed unsupported/fragile argument usage for this environment.
- Kept explicit raw PCM input shape (`s16le`, 24kHz) and resample-to-48k output for headset compatibility.

Current ffplay command construction (`cmd/proxy-chatbot/live_playback_ffplay.go`):
- `-nodisp`
- `-autoexit`
- `-loglevel error`
- `-fflags nobuffer`
- `-flags low_delay`
- `-probesize 32`
- `-analyzeduration 0`
- `-volume <0-100>`
- `-f s16le`
- `-ar 24000`
- `-af aresample=48000`
- `-i pipe:0`

### 2) Segment-scoped playback lifecycle
Implemented in `cmd/proxy-chatbot/live_playback_ffplay.go` and `cmd/proxy-chatbot/live_events.go`:
- Lazy start on first chunk (`Write` ensures process started).
- `Finalize()` on `assistant_audio_end`:
  - closes stdin,
  - waits for ffplay process,
  - clears process state.
- `Reset()` on `audio_reset` / interrupt:
  - kills process immediately and clears state.

This makes each assistant utterance deterministic and flushes audio reliably.

### 3) Retained safe failure behavior
If playback write fails:
- one reset/retry attempt is made,
- on retry failure playback is disabled for that live session,
- live session itself continues (STT/captions/tools/history still function).

## Refactor Done Alongside Fix
The original 1,000+ line live handler was split into focused files:
- `cmd/proxy-chatbot/live_mode.go`
- `cmd/proxy-chatbot/live_events.go`
- `cmd/proxy-chatbot/live_mic_ffmpeg.go`
- `cmd/proxy-chatbot/live_playback_ffplay.go`
- `cmd/proxy-chatbot/live_playback_tracker.go`
- `cmd/proxy-chatbot/live_utils.go`

This reduced coupling and made playback debugging/fixes isolated.

## Tests Added/Updated
In `cmd/proxy-chatbot/live_audio_test.go`:
- ffplay-only player path.
- ffplay args include explicit raw PCM input shape and expected filter.
- audio device env wiring (`SDL_AUDIO_DEVICE_NAME`).
- macOS mic arg selection behavior.
- playback write error path: one retry then disable.

Validation run:
- `go test ./cmd/proxy-chatbot -count=1`
- `go test ./... -count=1`

## Operational Notes
Useful env vars for live demo:
- `VAI_LIVE_MIC_NAME` (preferred on macOS; resolves AVFoundation audio input by name)
- `VAI_LIVE_MIC_INDEX` (macOS AVFoundation mic index)
- `VAI_LIVE_FFMPEG_INPUT` (explicit ffmpeg input)
- `VAI_LIVE_FFPLAY_AUDIO_DEVICE` (output device name)
- `VAI_LIVE_FFPLAY_VOLUME` (0-100)

If audio is silent again, first capture the exact `live playback error: ...` line; this code now surfaces ffplay stderr tail directly for fast diagnosis.

## Live History Warnings During Tool Turns
If you see warnings like:

`live history warning: expected len X, local len Y`

there are two contributing factors:

1. Prior SDK bug (fixed):
   - Live `run_event` frames were being emitted twice on `LiveSession.Events()`.
   - This caused duplicated `[tool]` lines and duplicated history warnings.
   - Fix is in `sdk/live.go` by avoiding double emission of wrapped `LiveRunEvent`.

2. Local history strictness:
   - `ExpectedLen` is a strict consistency check for deterministic history deltas.
   - Live turns that do not emit deltas can still advance gateway-side history, so local caller-owned history may temporarily diverge.

Current behavior in `proxy-chatbot`:
- History warnings are **off by default** to avoid noisy false positives during normal live use.
- Set `VAI_LIVE_HISTORY_WARN=1` to re-enable these mismatch warnings for debugging.
