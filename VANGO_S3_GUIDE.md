# vango-s3 Developer Guide (S3/R2 Integration)

This document is the developer-facing guide for `github.com/vango-go/vango-s3` (“vango-s3”). It is derived from (and intended to closely track) the canonical spec: `vango-s3/S3_INTEGRATION_SPEC.md`.

vango-s3 provides **S3-compatible object storage integration** for Vango applications, with **Cloudflare R2** as the default preset. It focuses on Vango-specific boundaries: upload lifecycle correctness, presigned URL workflows, safe errors/logging, tenant key scoping, and scaffolding.

**Scope boundary:** vango-s3 owns the blessed upload lifecycle (intent → presigned PUT → claim/promote), safe errors, key scoping, content validation, and scaffolding. It does **not** wrap the AWS SDK wholesale. For operations outside the lifecycle (server-side generation, migration, admin tools), use `aws-sdk-go-v2` directly via `store.S3Client()`.

## Table of Contents

- Requirements
- Installation
- Environment Variables
- Quickstart (Library Usage)
- Using With Vango (Scaffolded Integration)
- Goals and Mental Model
- Configuration and Tuning
- Upload Lifecycle: Presigned PUT + Claim/Promote
- Intent Tokens
- Conditional Promote (TOCTOU Protection)
- Concurrent Claims and Idempotency
- Size Abuse Mitigation
- Fallback: Server-Proxied Uploads (pkg/upload)
- Key Model and Tenant Scoping
- Presigned URLs (GET and PUT)
- Delivery Posture: Private-by-Default
- BlobRef
- Temp Cleanup (MUST)
- Safe Error Handling
- Safe Logging
- Lifecycle Rules (Vango Access Contract)
- Content Validation
- API Surface
- Testing
- Observability Guidance
- CSP and CORS Guidance
- IAM and Credential Scope Guidance
- Troubleshooting (Common Cases)
- v1.1 Roadmap
- Reference

---

## Requirements

- Go **1.24+**
- An S3-compatible object storage endpoint (Cloudflare R2, AWS S3, MinIO, etc)
- A lifecycle rule that deletes objects under `tmp/` (MUST). See “Temp Cleanup”.
- Backend SHOULD support conditional copy (`CopySourceIfMatch`). If it does not, `ClaimUpload` fails with `ErrPromoteInsecure` unless explicitly opted into reduced security.

---

## Installation

```bash
go get github.com/vango-go/vango-s3
```

Import:

```go
import s3store "github.com/vango-go/vango-s3"
```

---

## Environment Variables

Typical `.env`:

```bash
# Required (or use R2_ACCOUNT_ID to derive endpoint)
S3_ENDPOINT="https://<account-id>.r2.cloudflarestorage.com"
S3_ACCESS_KEY_ID="..."
S3_SECRET_ACCESS_KEY="..."
S3_BUCKET="..."
S3_REGION="auto"  # "auto" for R2; AWS S3 uses actual region

# Required for intent token signing (base64-encoded, >= 32 decoded bytes)
S3_INTENT_SECRET="..."

# Optional R2 convenience (used to derive endpoint if S3_ENDPOINT is unset)
R2_ACCOUNT_ID="..."

# Optional tuning (you may prefer configuring these via code)
S3_PRESIGN_PUT_EXPIRY="15m"
S3_PRESIGN_GET_EXPIRY="1h"
S3_MAX_UPLOAD_SIZE="104857600"
```

Security rules:

- `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_INTENT_SECRET` are secrets. Never log them.
- Presigned URLs and intent tokens are bearer credentials. Never log them.
- `S3_ENDPOINT` can contain an R2 account id. Treat it as sensitive in logs.

Generate intent secret:

```bash
openssl rand -base64 48
```

---

## Quickstart (Library Usage)

### Connect at startup (generic S3)

```go
store, err := s3store.New(ctx, s3store.Config{
    Endpoint:        os.Getenv("S3_ENDPOINT"),
    AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
    Bucket:          os.Getenv("S3_BUCKET"),
    Region:          os.Getenv("S3_REGION"),
    IntentSecret:    os.Getenv("S3_INTENT_SECRET"), // base64-encoded
})
if err != nil {
    panic(err)
}
defer store.Close()
```

### R2 convenience constructor

```go
store, err := s3store.NewR2(ctx, s3store.R2Config{
    AccountID:       os.Getenv("R2_ACCOUNT_ID"),
    AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
    Bucket:          os.Getenv("S3_BUCKET"),
    IntentSecret:    os.Getenv("S3_INTENT_SECRET"),
})
```

`NewR2` derives the endpoint from account id and defaults region to `"auto"`.

### Depend on the interface (testability)

Application code should depend on `s3store.Store`:

```go
type Store interface {
    CreateUploadIntent(ctx context.Context, opts s3store.IntentOptions) (*s3store.UploadIntent, error)
    ClaimUpload(ctx context.Context, intentToken string, tenantPrefix string, finalKey string, opts ...s3store.ClaimOption) (*s3store.BlobRef, error)
    PresignGet(ctx context.Context, key string, expiry time.Duration, opts ...s3store.PresignGetOption) (string, error)
    PresignPut(ctx context.Context, key string, expiry time.Duration, opts ...s3store.PresignPutOption) (*s3store.PresignPutResult, error)
    S3Client() *s3.Client
    Close() error
}
```

---

## Using With Vango (Scaffolded Integration)

### Scaffold (R2 default)

```bash
vango create myapp --with r2
```

This scaffolds:

- `.env` placeholders for `S3_*`, `R2_ACCOUNT_ID`, `S3_INTENT_SECRET` (permissions enforced to `0600`)
- `internal/blob/blob.go` store initialization
- `internal/blob/service.go` app-level service wrapper
- `app/routes/api/upload.go` upload intent endpoint (presigned PUT)
- `app/routes/api/claim.go` claim/promote endpoint
- `docs/blob.md` lifecycle cleanup + IAM guidance
- `vango.json` includes `"blob": { "enabled": true, "provider": "r2", "tmp_cleanup_configured": false }`

### Generic S3 scaffold

```bash
vango create myapp --with s3
```

### Non-interactive scaffolding (CI/agents)

If any `--s3-*` / `--r2-account-id` flag is provided, the CLI fails closed unless all required fields are provided.

Example:

```bash
vango create myapp --with r2 \
  --r2-account-id "$R2_ACCOUNT_ID" \
  --s3-access-key-id "$S3_ACCESS_KEY_ID" \
  --s3-secret-access-key "$S3_SECRET_ACCESS_KEY" \
  --s3-bucket "$S3_BUCKET" \
  --s3-intent-secret "$S3_INTENT_SECRET"
```

---

## Core Concepts

## Goals and Mental Model

What vango-s3 is designed to make boring and correct:

1. Upload lifecycle: presigned PUT → signed intent token → claim/promote with TOCTOU protection.
2. Credential hygiene: never leak access keys, secrets, presigned URLs, or signed query params in errors/logs.
3. Key scoping: tenant-aware key layout with strict validation and normalization.
4. Content safety: allowlists and size constraints enforced at intent creation and verified at claim time.
5. Promote semantics: conditional copy+delete behind a single `ClaimUpload` call (fail-closed by default if conditional copy is not available).

What vango-s3 does not do (v1):

- Wrap the AWS SDK wholesale (use `aws-sdk-go-v2` directly via `S3Client()` for non-lifecycle operations).
- Multipart/resumable uploads.
- CDN/custom domains/public buckets.
- Strict size enforcement at the PUT layer (presigned PUT limitation; see “Size Abuse Mitigation”).

### The blessed lifecycle

vango-s3’s default upload model keeps file bytes off your Vango server:

1. `CreateUploadIntent` (Action work): validate constraints, allocate a temp key, presign a PUT, sign an intent token.
2. Browser uploads directly to S3/R2 using the presigned PUT URL.
3. `ClaimUpload` (Action work): verify token + tenant scope, `HeadObject` to validate uploaded object, then promote via conditional `CopyObject` (ETag TOCTOU protection), then `DeleteObject` temp.

### Private by default

All objects are private by default. Access is granted via presigned GET or the redirect handler.

---

## Configuration and Tuning

`s3store.Config` controls behavior. Key fields:

- Connection: `Endpoint`, `R2AccountID` (derive endpoint), `AccessKeyID`, `SecretAccessKey`, `Bucket`, `Region`, `ForcePathStyle`
- Token signing: `IntentSecret` (required), `IntentSecretFallbacks` for rotation
- Constraints: `MaxUploadSize`, `AllowedContentTypes`, `DefaultContentType`
- Presign defaults: `PresignPutExpiry`, `PresignGetExpiry`
- Key layout: `TempKeyPrefix` (default `"tmp"`), `FileKeyPrefix` (default `"files"`)
- Claim verification: `VerifySize`, `VerifyContentType` (defaults true), `DeleteOnClaimRejection` (default true)
- TOCTOU posture: `AllowInsecurePromoteWithoutETag` (default false)
- Token parsing: `MaxIntentTokenSize` (default 4096 decoded bytes)
- Error formatting: `IncludeBucketInErrors` (default false)
- Operational: `RequestTimeout` (default 30s), `Logger` (optional, defaults to `slog.Default()`)

`Config` now uses bool fields for claim verification settings to match the canonical spec. For v1.x migration, temporary pointer overrides (`VerifyContentTypeOverride`, `VerifySizeOverride`, `DeleteOnClaimRejectionOverride`) are also available when you need explicit false overrides while preserving default-true behavior.

---

## Upload Lifecycle: Presigned PUT + Claim/Promote

### Step 1: Create an upload intent (server)

This must run off the session loop, typically inside a Vango Action work function.

```go
prefix, err := s3store.Prefix(orgID) // returns "t/{orgID}"
if err != nil {
    return nil, err
}

intent, err := store.CreateUploadIntent(ctx, s3store.IntentOptions{
    TenantPrefix: prefix,
    Filename:     in.Filename,
    ContentType:  in.ContentType,
    Size:         in.Size, // declared size bound into token
})
if err != nil {
    return nil, err
}
```

Returned `UploadIntent` includes:

- `Token`: signed bearer intent token used for `ClaimUpload`
- `PresignedURL`: presigned PUT URL (never log)
- `BoundContentType`: the content-type bound/normalized by the server; clients must use this value
- `RequiredHeaders`: headers the client must send for the PUT (and CORS must allow)
- `ExpiresAt`: token/put expiry

**Filename metadata:** if `Filename` is provided, the server stores a sanitized filename as S3 user metadata (`x-amz-meta-filename`) by requiring it as a signed header. This is not part of the key and must not contain PII you are unwilling to store.

What `CreateUploadIntent` enforces:

- Content-type normalization: empty becomes `DefaultContentType`
- Content-type allowlist (if configured): rejects with `ErrContentTypeRejected`
- Declared size <= `MaxUploadSize`: rejects with `ErrSizeExceeded`
- Allocates temp key under `tmp/{tenantPrefix}/{ulid}`
- Presigns PUT with `Content-Type` bound into signed headers where supported
- Signs intent token binding tenant, temp key, bound content type, max size, and expiry

### Step 2: Upload bytes directly to S3/R2 (client)

```js
await fetch(intent.upload_url, {
  method: 'PUT',
  headers: {
    'Content-Type': intent.content_type,
    ...intent.headers,
  },
  body: file,
});
```

Presigned PUT is the authorization. No CSRF is required for the PUT itself because it does not hit your origin.

### Step 3: Claim/promote (server)

Claim must run in an Action work function (blocking I/O).

```go
prefix, err := s3store.Prefix(orgID)
if err != nil {
    return nil, err
}
finalKey, err := s3store.FinalKeyChecked(prefix, "avatars")
if err != nil {
    return nil, err
}

ref, err := store.ClaimUpload(ctx, intentToken, prefix, finalKey)
if err != nil {
    return nil, err
}
```

What `ClaimUpload` enforces:

- Token parse size cap (`MaxIntentTokenSize`) and strict parsing → `ErrIntentMalformed`
- Constant-time HMAC verification (primary + fallbacks) → `ErrIntentInvalid`
- Version check (v1) → `ErrIntentInvalid`
- Strict expiry check (no grace) → `ErrIntentExpired`
- Tenant binding check: caller-supplied `tenantPrefix` must match token → `ErrTenantMismatch`
- Key scope:
  - temp key must be under `tmp/{tenantPrefix}/...` → `ErrKeyOutOfScope`
  - final key must be under `files/{tenantPrefix}/...` → `ErrKeyOutOfScope`
- `HeadObject` temp key:
  - not found → `ErrObjectNotFound`
  - access denied / missing bucket → `ErrAccessDenied` / `ErrBucketNotFound`
- Size/type verification (defaults on):
  - size too large vs token max → `ErrSizeExceeded` (+ best-effort temp delete by default)
  - content-type mismatch (stored metadata) → `ErrContentTypeRejected` (+ best-effort temp delete by default)
- Optional content sniffing: `WithClaimContentSniff(true)` downloads first 512 bytes and checks MIME via `http.DetectContentType`.
- Promote semantics:
  - Captures ETag from `HeadObject`
  - Copies using `CopySourceIfMatch` (conditional copy) → blocks TOCTOU overwrite between head and copy
  - On precondition failed: `ErrPromoteConflict`
  - If conditional copy is unavailable: `ErrPromoteInsecure` (unless explicitly opted into reduced security)
- Deletes temp key best-effort after promote
- Returns `BlobRef` (durable reference to store in your DB)

---

## Intent Tokens

Intent tokens are **signed, not encrypted**. Do not include PII in token fields. Treat tokens as bearer credentials; never log them.

Tokens bind:

- token version
- JTI (unique id, ULID)
- tenant scope
- temp key
- bound content type
- max size (declared size)
- expiry

Secret rotation:

- Signing uses the primary secret.
- Verification tries primary then `IntentSecretFallbacks` in order.

---

## Conditional Promote (TOCTOU Protection)

Claim upload is vulnerable to a race between `HeadObject` and `CopyObject` if the client can overwrite the temp key. vango-s3 uses:

- `CopySourceIfMatch: <etag from head>`

Fail-closed posture:

- If ETag is missing or conditional copy is unsupported, `ClaimUpload` fails with `ErrPromoteInsecure` unless `AllowInsecurePromoteWithoutETag` is set true (not recommended).

---

## Concurrent Claims and Idempotency

vango-s3 does **not** guarantee single-claim semantics. Two concurrent claims can successfully copy the same source object into two different final keys.

Recommended app-level idempotency:

- Use JTI (`BlobRef.JTI`) as a DB uniqueness constraint.
- If you need to prevent duplicate promoted objects entirely, implement a pre-claim “lock row” keyed by JTI.

---

## Size Abuse Mitigation

Presigned PUT does not enforce `content-length-range`. A malicious client can upload an oversized object before claim rejects it.

Layered mitigations:

- Keep PUT expiry short (default 15m)
- Configure bucket/prefix max upload size limits if provider supports
- Enable `DeleteOnClaimRejection` (default true)
- Configure lifecycle cleanup for `tmp/` (MUST)
- Add monitoring on claim failures by reason

---

## Fallback: Server-Proxied Uploads (pkg/upload)

For environments where direct-to-S3 is not feasible, use vango-s3’s adapter for `github.com/vango-go/vango/pkg/upload`.

```go
uploadStore := s3store.NewUploadStore(store, s3store.UploadStoreConfig{
    TenantFromRequest: func(r *http.Request) (string, error) {
        // Look up tenant from auth/session
        return orgID, nil
    },
})

app.HandleUpload("/api/upload/proxy", uploadStore) // includes CSRF protection
```

Adapter behavior:

- Authenticates/derives tenant at HTTP boundary
- Validates constraints via `CreateUploadIntent`
- Streams bytes via server-side PUT to the presigned URL
- Returns the intent token as `temp_id`

`pkg/upload` will include `intent_token` and `expires_at` in the JSON response when the returned `temp_id` is an intent token.

Credential safety:

- The adapter returns `SafeError` on failures and will not include presigned URLs in `err.Error()`.

---

## Key Model and Tenant Scoping

### Tenant prefix helpers

- `Prefix(tenantID)` → `t/{tenantID}` (validated: ASCII alnum + `-`/`_`, max 128)
- `MustPrefix(tenantID)` panics on invalid input (only use when tenant id is trusted)

### Key helpers

Default-prefix helpers (for default `tmp/files` layout):

- `TempKey(prefix)` → `tmp/{prefix}/{ulid}`
- `FinalKey(prefix, subpath)` → `files/{prefix}/{clean(subpath)}/{ulid}` (returns `""` on invalid input)
- `FinalKeyChecked(prefix, subpath)` returns `(string, error)`
- `FinalKeyDeterministic(prefix, subpath)` → `files/{prefix}/{clean(subpath)}`
- `TempKeyBelongsToTenant(key, tenantID)`
- `FinalKeyBelongsToTenant(key, tenantID)`

Custom-prefix helpers (for non-default `Config.TempKeyPrefix` / `Config.FileKeyPrefix`):

- `TempKeyWithPrefix(tempKeyPrefix, tenantPrefix)`
- `FinalKeyWithPrefix(fileKeyPrefix, tenantPrefix, subpath)`
- `FinalKeyWithPrefixChecked(fileKeyPrefix, tenantPrefix, subpath)`
- `FinalKeyDeterministicWithPrefix(fileKeyPrefix, tenantPrefix, subpath)`
- `FinalKeyDeterministicWithPrefixChecked(fileKeyPrefix, tenantPrefix, subpath)`
- `TempKeyBelongsToTenantWithPrefix(key, tenantID, tempKeyPrefix)`
- `FinalKeyBelongsToTenantWithPrefix(key, tenantID, fileKeyPrefix)`

Subpath normalization rejects traversal (`..`), empty segments, control chars, backslashes, overly long segments, and keys > 1024 bytes.

### Custom key prefixes

If you customize prefixes in config, use the explicit helper variants so generated keys match claim-time scope checks:

```go
finalKey := s3store.FinalKeyWithPrefix(cfg.FileKeyPrefix, tenantPrefix, "avatars")
ok := s3store.FinalKeyBelongsToTenantWithPrefix(finalKey, tenantID, cfg.FileKeyPrefix)
```

---

## Presigned URLs

### PresignPut (advanced)

Most apps should use `CreateUploadIntent`. `PresignPut` is available for advanced use.

```go
res, err := store.PresignPut(ctx, key, 15*time.Minute,
    s3store.WithPutContentType("image/png"),
)
```

### PresignGet

```go
url, err := store.PresignGet(ctx, ref.Key, time.Hour,
    s3store.WithGetContentDisposition(`attachment; filename="report.pdf"`),
)
```

### ServeViaRedirect

Serve private objects to authenticated users without proxying bytes:

```go
h := s3store.ServeViaRedirect(store, s3store.RedirectConfig{
    Expiry: 5 * time.Minute,
    AuthFromRequest: func(r *http.Request) (string, error) {
        return orgID, nil
    },
    AuthorizeKey: func(tenantID, key string) error {
        if !s3store.FinalKeyBelongsToTenant(key, tenantID) {
            return vango.Forbidden()
        }
        return nil
    },
})
```

`AuthorizeKey` is required. If omitted, `ServeViaRedirect` fails closed with HTTP 500 and does not call `PresignGet`.

---

## BlobRef

`BlobRef` is the durable reference type you store in your DB:

```go
type BlobRef struct {
    Bucket string `json:"bucket"`
    Key    string `json:"key"`
    ETag   string `json:"etag,omitempty"`
    Size   int64  `json:"size,omitempty"`
    JTI    string `json:"jti,omitempty"`
}
```

`BlobRef.String()` returns `s3://{bucket}/{key}`.

---

## Temp Cleanup (MUST)

You must configure a lifecycle rule that deletes objects under `tmp/` after a short retention window (recommended 24 hours).

For scaffolded Vango apps:

- `vango.json` includes `blob.tmp_cleanup_configured` (default false).
- `vango dev` warns if blob is enabled and cleanup is not acknowledged.

---

## Safe Error Handling

All public vango-s3 methods return errors wrapped as `*s3store.SafeError` with a safe `.Error()` string.

Rules:

- Never log presigned URLs, intent tokens, secrets.
- Avoid logging full keys (tenant scoped); prefer token JTI for correlation.
- Log `err.Error()` only (safe message), not full chains / verbose formats.

Important: `SafeError` wraps a cause for `errors.Is/As`, but the wrapped cause can contain sensitive upstream AWS SDK details. Do not stringify full error chains in production (avoid `%+v`).

Error safety contract (what you can rely on):

- `err.Error()` is safe for production logs and end-user error messages.
- `errors.Unwrap(err)` may include sensitive details; treat as debug-only.
- Bucket names are excluded from safe messages by default. If you enable `IncludeBucketInErrors`, bucket name is appended to safe messages for debugging only.

Sentinel errors (safe to match via `errors.Is`):

- `ErrObjectNotFound`, `ErrAccessDenied`, `ErrBucketNotFound`
- `ErrContentTypeRejected`, `ErrSizeExceeded`
- `ErrKeyInvalid`, `ErrKeyOutOfScope`
- `ErrTenantIDInvalid`, `ErrTenantMismatch`
- `ErrPromoteFailed`, `ErrPromoteConflict`, `ErrPromoteInsecure`
- `ErrIntentInvalid`, `ErrIntentExpired`, `ErrIntentMalformed`

---

## Safe Logging

Never log:

- Presigned URLs (GET or PUT)
- Intent tokens
- Access keys, secret keys, intent secrets
- Signed query parameters (`X-Amz-*`)

Prefer logging:

- token JTI (`BlobRef.JTI`) for correlation
- tenant id (if allowed by your policy), otherwise a stable hash
- safe error strings (`SafeError.Error()`)
- sizes and content types (for constraint diagnostics)

If you emit your own structured logs for lifecycle events, do not include `temp_key`, `final_key`, `intent_token`, or `presigned_url` fields.

---

## Lifecycle Rules (Vango Access Contract)

Blocking S3 operations are allowed only in:

- Action work functions
- Resource loaders (reads/presign only)
- HTTP handlers (upload, redirect, webhook)

They are **not** allowed in render closures, setup callbacks, event handlers, or lifecycle callbacks.

---

## CORS and CSP (Browser Uploads)

If you upload directly to S3/R2 via presigned PUT:

- CSP must allow the storage endpoint in `connect-src`
- Bucket must allow CORS for your origin, methods `PUT` (and `GET` if displaying assets via presigned GET)
- `AllowedHeaders` must include all headers returned in `intent.RequiredHeaders` (including `Content-Type` and any `x-amz-*` headers like `x-amz-meta-filename`)

Avoid `AllowedOrigins: ["*"]` in production.

---

## Content Validation

Validation happens at two phases:

1. At intent creation (`CreateUploadIntent`):
   - Normalizes content-type: empty becomes `DefaultContentType`.
   - Enforces allowlist if configured (`AllowedContentTypes`).
   - Enforces declared size `<= MaxUploadSize`.
2. At claim time (`ClaimUpload`):
   - `HeadObject` checks object exists.
   - If `VerifySize`: rejects if stored `ContentLength > token.MaxSize`.
   - If `VerifyContentType`: rejects if stored `ContentType` is empty or differs from token-bound content-type.
   - If `DeleteOnClaimRejection`: best-effort `DeleteObject(temp_key)` on size/type rejection.

Presigned PUT limitation:

- S3-style presigned PUT does not strictly enforce upload size or file type at the storage layer. Claim-time verification is authoritative.

Content sniffing:

- Stored `ContentType` comes from upload metadata (client-controlled). If you need higher assurance, enable sniffing:

```go
ref, err := store.ClaimUpload(ctx, token, prefix, finalKey, s3store.WithClaimContentSniff(true))
```

---

## API Surface

vango-s3 is lifecycle-focused:

- `CreateUploadIntent`: validate constraints + presign PUT + sign intent token
- `ClaimUpload`: verify token + tenant/key scope + head/verify + conditional promote
- `PresignPut`: advanced presign (without intent/token lifecycle)
- `PresignGet`: private downloads
- `ServeViaRedirect`: auth → authorize → presign GET → 302
- `NewUploadStore`: `pkg/upload` adapter for server-proxied fallback
- `S3Client`: escape hatch to use `aws-sdk-go-v2` directly for non-lifecycle operations

Non-goals:

- no `Put/Get/Delete/Head` wrappers
- no public `PromoteTemp` bypass around token verification

---

## IAM / Credential Scope Guidance

Least privilege policy should allow:

- `tmp/*`: Put/Get/Delete (upload + verify + cleanup + promote source)
- `files/*`: Get/Put (download + promote destination)

For R2, scope API tokens to the specific bucket with Object Read & Write permissions.

---

## Testing

Use `s3store.NewTestStore()` for deterministic unit tests. It implements the same public lifecycle API with:

- Real v1 intent tokens
- Claim validation and tenant/key scoping
- Conditional promote conflict simulation
- Deterministic fake presign URLs
- Optional deterministic clock via `NewTestClock` + `WithTestClock`

Example:

```go
clock := s3store.NewTestClock(time.Now())
store := s3store.NewTestStore(s3store.WithTestClock(clock))
```

---

## Observability Guidance

vango-s3 emits structured logs and does not emit metrics directly.

Logger wiring:

- `Config.Logger` and `R2Config.Logger` are optional.
- If omitted, runtime logging uses `slog.Default()`.

Structured lifecycle events:

- `s3_intent_created` (`INFO`)
  - `tenant`, `content_type`, `declared_size`, `jti`
- `s3_claim_attempt` (`INFO` on success, `WARN` on failure)
  - always: `outcome`, `duration_ms`, optional `jti`
  - failure: `reason`, `error` (safe string from `err.Error()`)
- `s3_promote_operation` (`INFO` on success/fallback success, `WARN` on failure)
  - always: `jti`, `duration_ms`, `conditional_requested`, `fallback_unconditional`, `outcome`
  - failure: `reason`, `error` (safe string)
- existing warnings remain:
  - `s3_insecure_promote_enabled`
  - `s3_temp_delete_failed`

Sensitive fields are never logged in lifecycle events: no presigned URLs, intent tokens, temp keys, or final keys.

Recommended app-level metrics:

- `upload_intents_total`
- `s3_claim_succeeded_total`
- `s3_claim_failed_total{reason=...}` where reason is derived from `errors.Is` against sentinel errors:
  - `not_found`, `size_exceeded`, `type_rejected`, `promote_failed`, `promote_conflict`, `promote_insecure`, `tenant_mismatch`, `key_out_of_scope`, `intent_invalid`, `intent_expired`, `intent_malformed`
- latency histograms for claim duration (head + copy + delete)

Cardinality guidance:

- keep `reason` bounded
- avoid using full keys as labels
- only label by tenant if acceptable and bounded by your user base

---

## CSP and CORS Guidance

If the browser uploads via presigned PUT:

- CSP `connect-src` must allow your storage endpoint (R2: `https://*.r2.cloudflarestorage.com`).
- Bucket CORS must allow:
  - `AllowedMethods`: `PUT` (and `GET` if you serve assets directly)
  - `AllowedOrigins`: your app origin(s) only
  - `AllowedHeaders`: include **every** header in `intent.RequiredHeaders` plus `Content-Type` / `Content-Length`

Avoid `AllowedOrigins: ["*"]` in production.

---

## IAM and Credential Scope Guidance

Map lifecycle calls to IAM actions:

- presigned PUT: `s3:PutObject` on `tmp/*`
- claim `HeadObject`: `s3:GetObject` on `tmp/*`
- promote copy: `s3:GetObject` on `tmp/*` + `s3:PutObject` on `files/*`
- temp delete: `s3:DeleteObject` on `tmp/*`
- presigned GET: `s3:GetObject` on `files/*`

This reduces blast radius if credentials are compromised.

---

## Troubleshooting (Common Cases)

- Browser CORS error during PUT: bucket CORS `AllowedHeaders` missing signed headers from `intent.RequiredHeaders`
- CSP `connect-src` violation: add storage endpoint to CSP
- 403 on PUT: presigned URL expired; reduce delay or increase `PresignPutExpiry`
- `ErrIntentExpired`: claim happened after expiry; restart flow
- `ErrIntentInvalid`: token signature mismatch; check intent secret + fallbacks
- `ErrTenantMismatch` / `ErrKeyOutOfScope`: bug in tenant resolution or key construction
- `ErrPromoteConflict`: object modified between head and copy; restart flow
- `ErrPromoteInsecure`: backend does not support conditional copy; use compatible backend or explicitly allow insecure promote (not recommended)

---

## v1.1 Roadmap

Planned additive features:

- Presigned POST support (for strict client-side `content-length-range` enforcement)
- PublicRead / public object posture (opt-in)
- CDN/custom domain guidance
- Multipart/resumable uploads

---

## Reference

- Canonical spec: `vango-s3/S3_INTEGRATION_SPEC.md`
- Package overview: `vango-s3/README.md`
- API types: `vango-s3/types.go`
- Constructors/config defaults: `vango-s3/config.go`
- Deterministic test store: `vango-s3/test_store.go`
