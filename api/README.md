# VAI Gateway OpenAPI

- Spec file: `api/openapi.yaml`
- OpenAPI version: 3.1.0

## What it covers

- `/v1/messages` (JSON + SSE)
- `/v1/runs` (blocking) and `/v1/runs:stream` (SSE)
- `/v1/models`
- `/v1/live` (WebSocket upgrade; protocol is documented in `LIVE_AUDIO_MODE_DESIGN.md`)
- `/healthz` and `/readyz`

## Notes

- Requests are **strictly** decoded (unknown fields are rejected).
- Responses are **forward-compatible**: new optional fields and new SSE event types may be added within `/v1`.
