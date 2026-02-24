# VAI Gateway Hosting Architecture and Design

This document describes a recommended hosted architecture for VAI as a production AI gateway that supports:
- `POST /v1/messages` with SSE streaming
- future Live Audio Mode with WebSockets (per `LIVE_AUDIO_MODE_DESIGN.md`)
- BYOK and/or managed upstream provider keys
- gateway API keys (hosted) and optional auth relaxation (self-hosted)
- observability/evals as add-on services over time

The recommendation here is intentionally “boring infra” first: a stateless Go service on containers/VMs with managed Postgres + Redis, optionally fronted by Cloudflare for edge security/performance.

---

## 1. Principles and Requirements

### 1.1 Core requirements

1. Long-lived connections:
   - SSE streams lasting tens of seconds to minutes
   - WebSocket sessions lasting minutes to hours
2. Predictable networking semantics:
   - no proxy buffering of SSE
   - correct WebSocket upgrades and idle timeouts
3. Horizontal scalability:
   - stateless request handling
   - centralized auth/policy, usage accounting, and rate limiting
4. Secure-by-default:
   - prevent “open relay” abuse
   - strict payload limits and egress controls
   - no secrets in logs
5. Self-hostable:
   - run on a VM or Kubernetes without Cloudflare

### 1.2 Non-requirements (initial)

1. Multi-region active/active from day 1
2. Edge compute as the primary runtime (Workers-only gateway)
3. Perfect global low latency for live audio on day 1

---

## 2. Recommended Hosted Topology

### 2.1 High-level architecture (hosted)

Recommended default:
- Cloudflare as an **edge front door** (WAF, DDoS, TLS, bot mgmt, coarse rate limits)
- A single-region origin running the Go gateway in containers (ECS Fargate or Kubernetes)
- Postgres for durable state (tenants, keys, policies, usage ledger)
- Redis for distributed limits and session coordination (rate limit counters, concurrency caps)

```mermaid
flowchart LR
  Client((Clients\nSDKs/curl))

  CF[Cloudflare Edge\nTLS/WAF/Bot/Edge RL]
  ALB[Origin Load Balancer\nSSE + WebSocket]
  GW[VAI Gateway\nGo service\n(stateless)]

  PG[(Postgres\nRDS/Cloud SQL)]
  RD[(Redis\nElastiCache/MemoryStore)]

  Obs[Logs/Metrics/Traces\n(OpenTelemetry + vendor)]
  Up[LLM Providers\nAnthropic/OpenAI/Gemini/...]

  Client --> CF --> ALB --> GW
  GW --> PG
  GW --> RD
  GW --> Obs
  GW --> Up
```

Notes:
- Cloudflare is optional, but recommended for hosted. The origin must work without it.
- The gateway must tolerate clients that do not support HTTP/2 well; SSE should work over HTTP/1.1.

### 2.2 Where to run the gateway compute

Good options:
- AWS ECS Fargate (simpler than Kubernetes for a small team; excellent for stateless services)
- GKE Autopilot (if you are already on GCP and want managed Kubernetes)

Avoid for primary runtime (for this product shape):
- “pure serverless” runtime where connection duration and socket semantics are constrained.

---

## 3. Cloudflare: How to Use It (and How Not To)

### 3.1 What Cloudflare should do

1. TLS termination and cert lifecycle
2. WAF rules for obvious abuse patterns
3. Bot mitigation
4. Basic edge rate limiting (coarse, not tenant-accurate)
5. Optional: CDN caching for static assets (docs, status page)
6. Optional: Access policies for admin endpoints (internal only)

### 3.2 What Cloudflare should not do (initially)

1. Run the full gateway compute at the edge (Workers-only)
2. Hold durable tenant state (beyond simple KV-like config caches)
3. Implement fine-grained per-tenant rate limiting as the source of truth

Rationale:
- SSE and WebSocket behavior can be sensitive to buffering and timeouts.
- You want the exact same binary to run in hosted and self-hosted modes.
- As you add “obs/evals” services, you’ll want durable storage and long-running jobs.

### 3.3 Cloudflare configuration checklist (conceptual)

1. Ensure SSE is not buffered:
   - gateway emits periodic `ping` events and flushes on each event
2. WebSockets enabled for live mode
3. Timeouts aligned with your SSE/WS SLAs
4. Request body size limits aligned with your multimodal policy

---

## 4. Origin Network and Load Balancer Behavior

### 4.1 SSE requirements

The load balancer and any reverse proxies must:
- pass through `text/event-stream` without buffering
- support long-lived HTTP responses
- not forcefully compress in a way that breaks streaming flush behavior
- have idle timeouts >= your maximum expected silent interval (or rely on `ping`)

Gateway requirements:
- use `http.Flusher` to flush each SSE event
- send keepalive pings when upstream is quiet
- detect client disconnect and close upstream provider stream

### 4.2 WebSocket requirements (Live mode)

The load balancer must:
- support HTTP Upgrade to WebSocket
- have idle timeouts compatible with live sessions

Gateway requirements:
- enforce per-tenant max concurrent WS sessions
- enforce inbound audio frame limits and backpressure policies (per `LIVE_AUDIO_MODE_DESIGN.md`)

---

## 5. Data Stores

### 5.1 Postgres (durable control-plane + ledger)

Use Postgres for:
- tenants/projects
- gateway API keys and policies
- allowlisted models per tenant
- usage ledger (append-only events)
- audit logs for admin actions (later)

Schema principles:
- append-only usage ledger (immutable rows), then rollups/materialized views for billing analytics
- store secrets encrypted at rest if you ever store managed upstream keys (or store only references to KMS/Vault)

### 5.2 Redis (hot-path enforcement)

Use Redis for:
- distributed rate limiting counters (token bucket / leaky bucket approximations)
- concurrency caps
- (later) short-lived session state for live mode resume windows

Operational rules:
- Redis is not the source of truth for billing/usage; it’s enforcement.
- Keep keys low-cardinality and set TTLs.

---

## 6. Auth and Keying Model

### 6.1 Hosted default: gateway key + BYOK

Hosted should require:
- `Authorization: Bearer vai_sk_...` (gateway key)
- plus BYOK headers for upstream providers (until managed keys are added)

Why the gateway key matters even with BYOK:
- without it, the service is an unauthenticated public socket endpoint (abuse target)
- you lose tenant identity (no per-tenant policy/rate limits/usage)
- you cannot safely add “obs/evals” services and expect isolation

### 6.2 Self-host modes (reduce friction safely)

For self-host, support an explicit `auth_mode`:
- `required`: gateway key required (recommended when exposed beyond localhost)
- `optional`: allow missing gateway key; apply only global limits
- `disabled`: no gateway auth; rely on network boundaries (localhost/private VPC)

Recommendation:
- Hosted: `required`
- Self-host:
  - default to `required` when binding to non-loopback
  - allow `disabled` only when binding to loopback by default

### 6.3 BYOK headers

Recommended BYOK headers (v1):
- `X-Provider-Key-Anthropic`
- `X-Provider-Key-OpenAI` (also used for `oai-resp/*`)
- `X-Provider-Key-Gemini`
- `X-Provider-Key-Groq`
- `X-Provider-Key-Cerebras`
- `X-Provider-Key-OpenRouter`
- `X-Provider-Key-Cartesia` (voice: `/v1/messages` voice + Live STT/TTS fallback)
- `X-Provider-Key-ElevenLabs` (voice: Live TTS when enabled)

Hard requirements:
- never log these headers
- never return them in errors
- apply strict header size limits

---

## 7. Rate Limiting and Quotas

### 7.1 Layering

1. Cloudflare edge rate limits:
   - coarse, IP-based or simple header-based rules
   - not the source of truth
2. Gateway rate limits:
   - per-tenant API key token bucket (requests/minute)
   - per-tenant concurrency cap (in-flight requests, streams, and WS sessions)
3. Provider-facing pacing (later):
   - optional per-provider throttles to avoid upstream bans

### 7.2 What to limit (suggested v1)

- Max request body bytes
- Max decoded base64 bytes per request
- Max streaming duration
- Max concurrent SSE streams per tenant
- Max concurrent WS sessions per tenant (live mode)

---

## 8. Observability: Logs, Metrics, Traces

### 8.1 Request identity

Every request/stream/session should have:
- `request_id` (ULID or similar)
- `tenant_id` / `project_id` (when authenticated)

### 8.2 Metrics (minimum useful set)

- HTTP requests total by route/status/provider
- latency histograms by route/provider
- streaming completion vs disconnect vs upstream error
- rate limit denials (and which limiter)
- WS session counts and close reasons (live mode)

### 8.3 Tracing

Add OpenTelemetry:
- trace for each HTTP request and WS session
- spans for upstream provider calls
- propagate `traceparent`

---

## 9. Deployment and CI/CD

### 9.1 Build artifacts

- `cmd/vai-proxy` produces a single binary.
- Build a container image:
  - pinned base image
  - non-root user
  - minimal runtime deps

### 9.2 Release strategy

- Progressive rollout (1% -> 10% -> 100%)
- Health checks:
  - `healthz` for liveness
  - `readyz` for config/store readiness
- Rollback on:
  - elevated error rates
  - elevated SSE disconnects
  - elevated latency p95/p99

---

## 10. Self-Host Reference Deployment

### 10.1 Single VM (fastest path)

- Systemd service runs `vai-proxy`
- Caddy/Nginx in front for TLS and basic auth (optional)
- `auth_mode=disabled` if binding to localhost only
- Postgres/Redis optional:
  - v1 can run purely in-memory limits for dev
  - for multi-tenant self-host, use Postgres + Redis

### 10.2 Kubernetes

- Helm chart or Kustomize:
  - Deployment + HPA
  - Service + Ingress
  - ConfigMap/Secret for settings

---

## 11. Roadmap: Multi-Region and Live Audio

### 11.1 Multi-region

Phase approach:
1. single-region origin + Cloudflare global edge
2. add a second region and shift traffic gradually
3. decide data strategy:
   - single-writer Postgres + read replicas, or
   - per-region ledger with asynchronous aggregation (more complex)

### 11.2 Live audio mode implications

Live audio (WS) pushes you toward:
- sticky-ish session behavior (not necessarily LB stickiness, but stable per-connection handling)
- stricter connection timeout tuning
- more explicit backpressure and concurrency caps

---

## 12. What This Enables (Obs, Evals, and More)

Once the gateway has:
- tenant identity (gateway keys)
- durable ledger (Postgres)
- enforcement (Redis)
- consistent request IDs and metrics

…you can add:
- request/response logging policies (redaction, sampling)
- eval jobs (async queue workers)
- model routing policies (per-tenant allowlists and fallbacks)
- “replay” tooling (carefully, with privacy controls)

---

## 13. Open Decisions

1. Cloud provider choice for origin (AWS ECS Fargate vs GKE Autopilot).
2. Whether `/v1/runs` ships immediately after `/v1/messages` or after the first hosted GA.
3. Whether self-host defaults to `auth_mode=required` unless explicitly disabled.
4. Usage ledger retention and privacy policy (especially if you ever log prompts).
