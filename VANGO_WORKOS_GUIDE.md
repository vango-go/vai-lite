# Vango + WorkOS Developer Guide

This guide explains how to use `github.com/vango-go/vango-workos` to add WorkOS AuthKit authentication, cookie-backed sessions, Vango session-bridge hooks, periodic session revalidation, webhooks, admin portal links, and a small enterprise API surface to a Vango application.

Key idea: WorkOS is HTTP request/response, while Vango sessions are long-lived (WebSocket) and only run HTTP middleware at session start/resume. `vango-workos` bridges validated HTTP identity into Vango’s runtime-only auth projections.

## What You Get

- AuthKit routes: `/auth/signin`, `/auth/signup`, `/auth/callback`, `/auth/logout` (POST), `/auth/signed-out`, `/auth/signed-out.js`
  - all auth entry/callback/logout responses send `Cache-Control: no-store` and `Pragma: no-cache`
- Encrypted cookie sessions (AES-256-GCM, HttpOnly), with optional secret fallbacks for rotation
- HTTP-boundary middleware that:
  - reads/decrypts the session cookie
  - verifies WorkOS access tokens via JWKS (local JWT verification)
  - optionally refreshes access tokens using refresh tokens
  - projects a `*workos.Identity` onto the `net/http` request context (via `vango.WithUser`)
- Vango session bridge hooks (`OnSessionStart`, `OnSessionResume`) that rehydrate runtime auth state (`pkg/auth`)
- Periodic session revalidation (`AuthCheckConfig`) that detects server-side revocation via WorkOS sessions API
- Webhook handler with signature verification + subscription helpers
- Admin Portal link generation
- Enterprise read/write APIs (users, orgs, memberships, SSO connections, directory sync, audit log emission)
- Deterministic test kit (`TestAuth`, `TestIdentity`, `HydrateSessionForTest`)

## Installation

In your Vango app:

```bash
go get github.com/vango-go/vango-workos
```

If you are developing inside this monorepo, use a `replace` directive to point at the local `vango-workos` module:

```go
replace github.com/vango-go/vango-workos => ../vango-workos
```

## Required WorkOS Dashboard Configuration

You will need:

- A WorkOS API key (`sk_...`) and client ID (`client_...`).
- An AuthKit redirect URI matching your app route (e.g. `https://app.example.com/auth/callback`).
- A sign-out redirect URL matching `BaseURL + SignOutRedirectURI` (default: `https://app.example.com/auth/signed-out`).
- A webhook secret (`whsec_...`) if you plan to receive webhooks.

## Environment Variables

The package does not read environment variables directly; your app should map env vars into `workos.Config`.

Suggested env vars (from `vango-workos/README.md`):

```bash
# Required
WORKOS_API_KEY="sk_live_..."
WORKOS_CLIENT_ID="client_..."
WORKOS_REDIRECT_URI="http://localhost:8080/auth/callback"
WORKOS_COOKIE_SECRET="your-secure-secret-at-least-32-chars"
WORKOS_BASE_URL="http://localhost:8080"

# Optional
WORKOS_WEBHOOK_SECRET="whsec_..."
WORKOS_COOKIE_SECRET_FALLBACKS="oldsecret1,oldsecret2"
WORKOS_JWT_ISSUER="https://api.workos.com"
WORKOS_JWT_AUDIENCE="$WORKOS_CLIENT_ID"
```

Security requirements:

- `CookieSecret` and each entry in `CookieSecretFallbacks` must be at least 32 characters.
- Use HTTPS in production. If you configure `CookieSameSite=none`, cookies will be forced `Secure`.

## Canonical Wiring (Recommended)

This is the “standard” integration pattern:

1. Create a WorkOS client (`workos.New`).
2. Install HTTP middleware (`app.Server().Use(client.Middleware())`).
3. Wire bridge hooks into the Vango config (`OnSessionStart`, `OnSessionResume`).
4. Configure periodic revalidation (`Session.AuthCheck: client.RevalidationConfig()`).
5. Mount WorkOS auth routes + webhooks on an `http.ServeMux`, and mount the Vango app at `/`.

```go
package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/vango-go/vango"
	workos "github.com/vango-go/vango-workos"
)

func main() {
	fallbacks := splitCSV(os.Getenv("WORKOS_COOKIE_SECRET_FALLBACKS"))

	wosClient, err := workos.New(workos.Config{
		APIKey:             os.Getenv("WORKOS_API_KEY"),
		ClientID:           os.Getenv("WORKOS_CLIENT_ID"),
		RedirectURI:        os.Getenv("WORKOS_REDIRECT_URI"),
		SignOutRedirectURI: "/auth/signed-out", // must be an absolute path
		CookieSecret:       os.Getenv("WORKOS_COOKIE_SECRET"),
		CookieSecretFallbacks: fallbacks,
		BaseURL:           os.Getenv("WORKOS_BASE_URL"),
		WebhookSecret:     os.Getenv("WORKOS_WEBHOOK_SECRET"),
		EnableAuditLogs:   true,
	})
	if err != nil {
		panic(err)
	}

	bridge := wosClient.SessionBridge()

	app, err := vango.New(vango.Config{
		OnSessionStart:  bridge.OnSessionStart,
		OnSessionResume: bridge.OnSessionResume,
		Session: vango.SessionConfig{
			AuthCheck: wosClient.RevalidationConfig(),
		},
	})
	if err != nil {
		panic(err)
	}

	// HTTP-boundary auth middleware must be installed on the server.
	app.Server().Use(wosClient.Middleware())

	// Logout is POST-only, so protect it with Vango's CSRF middleware.
	csrfMw := app.Server().CSRFMiddleware()

	mux := http.NewServeMux()

	// AuthKit routes.
	wosClient.RegisterAuthHandlers(mux, csrfMw)

	// Optional: WorkOS webhooks.
	mux.Handle("/webhooks/workos", wosClient.WebhookHandler(
		workos.OnDirectoryUserCreated(func(ctx context.Context, e workos.WebhookEvent) {
			// Unmarshal e.Data and handle the event (do not block Vango session loops).
		}),
	))

	// Mount Vango.
	mux.Handle("/", app)
	app.Server().SetHandler(mux)

	if err := app.RunAddr(":8080"); err != nil {
		panic(err)
	}
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
```

### Why This Wiring Matters

- `Middleware()` must run at the HTTP boundary to attach identity to the request context. Without it, `OnSessionResume` will fail with `workos: missing identity on resume (middleware not applied?)`.
- `OnSessionStart`/`OnSessionResume` are the handoff points where HTTP identity is projected into Vango’s runtime auth state (`pkg/auth`).
- `AuthCheck` runs periodically inside Vango sessions to detect server-side session revocation. It is not a token-expiry check.

## Authentication Flow (What Happens)

### Sign-in and sign-up

- `GET /auth/signin`: generates an AuthKit authorization URL, sets a short-lived state cookie, redirects to WorkOS.
- `GET /auth/signup`: same, but sets `screen_hint=sign_up`.
- both responses are explicitly non-cacheable (`Cache-Control: no-store`, `Pragma: no-cache`) so auth redirects and state-bearing responses are not cached by browsers or intermediaries.

### Callback

- `GET /auth/callback`:
  - validates the `state` against the state cookie (CSRF protection)
  - exchanges `code` for `AccessToken`, `RefreshToken`, and user info via WorkOS User Management
  - verifies the access token locally (JWKS + issuer + audience)
  - stores tokens, an authoritative `org_id` binding (when available), and an identity hint (UX-only) in an encrypted cookie
  - redirects to `return_to` if safe, otherwise `/`
  - every response path is explicitly non-cacheable (`Cache-Control: no-store`, `Pragma: no-cache`), including validation failures and successful cookie-setting redirects

Safe redirect rules for `return_to`:

- Relative redirects must be absolute-path (`/...`) and must not start with `//`.
- Absolute redirects are allowed only if the origin exactly matches `BaseURL`.

### Middleware (every HTTP request)

`client.Middleware()`:

1. Reads/decrypts the session cookie.
2. Verifies the access token locally using cached JWKS.
3. If the token is expired:
   - refreshes tokens (unless `DisableRefreshInMiddleware=true`), then re-verifies
4. Creates a `*workos.Identity` from the JWT claims (and may fill missing `Email`/`Name` from the identity hint for UX).
5. Derives `Identity.OrgID` / `Principal.TenantID` authoritatively:
   - prefer JWT `org_id` claim when present
   - otherwise use the cookie’s `org_id` binding (written only from WorkOS responses)
   - if both are missing, performs a bounded `ValidateSession` lookup and upgrades the cookie
6. Writes the updated cookie when needed (refresh occurred, org binding upgraded, or hint was missing).
7. Attaches identity to the HTTP request context (`vango.WithUser`).

Important behavior:

- If JWKS is unavailable (`ErrJWKSUnavailable`), the middleware does not clear the cookie and passes the request through without an identity projection.

### Vango Session Bridge (WebSocket session start/resume)

Bridge hooks run only at session start or resume:

- `bridge.OnSessionStart`: if there’s a `*workos.Identity` on the HTTP request context, it writes runtime-only auth projections:
  - `auth.Set(session, identity)`
  - `auth.SetPrincipal(session, auth.Principal{...})`
- `bridge.OnSessionResume`: requires identity from the HTTP request context and performs an active WorkOS session validation (`ValidateSession`). If inactive, resume is rejected.

## Using Identity in Vango Code

### In components and route handlers (Vango context)

Use `workos.CurrentIdentity(ctx)` or `workos.RequireIdentity(ctx)`:

```go
identity, ok := workos.CurrentIdentity(ctx)
if !ok {
	// Not authenticated.
}

// or
identity, err := workos.RequireIdentity(ctx)
if err != nil {
	// auth.ErrUnauthorized (or a different auth error)
}
```

### In `context.Context` code (actions, resources, services)

Use `workos.IdentityFromContext(ctx)`:

```go
identity, ok := workos.IdentityFromContext(ctx)
if !ok {
	// Not authenticated (auth.ErrUnauthorized is usually appropriate).
}
```

You can also attach an identity to a context for tests or internal calls:

```go
ctx = workos.WithIdentity(ctx, workos.TestIdentity())
```

## Route Protection and Authorization

### Require authentication

Use Vango route middleware. `vango-workos` exports a convenience alias:

```go
var RequireAuth = authmw.RequireAuth
```

In file-based routing:

```go
package dashboard

import (
	workos "github.com/vango-go/vango-workos"
	"github.com/vango-go/vango/pkg/router"
)

func Middleware() []router.Middleware {
	return []router.Middleware{
		workos.RequireAuth,
	}
}
```

### Require a WorkOS permission

If your WorkOS access tokens include permissions, you can enforce them:

1. With route middleware:

```go
package admin

import (
	workos "github.com/vango-go/vango-workos"
	"github.com/vango-go/vango/pkg/authmw"
	"github.com/vango-go/vango/pkg/router"
)

func Middleware() []router.Middleware {
	return []router.Middleware{
		authmw.RequireAuth,
		authmw.RequirePermission[*workos.Identity](func(i *workos.Identity) bool {
			return i.HasPermission("projects:delete")
		}),
	}
}
```

2. Or inside component code:

```go
if err := workos.RequirePermission(ctx, "projects:delete"); err != nil {
	return err // auth.ErrForbidden
}
```

Notes:

- Do not use HTTP middleware for path-based authorization. HTTP middleware is for identity projection only.
- `Identity.Permissions` is populated from JWT claims (and depends on how your WorkOS environment is configured).

## Periodic Session Revalidation (AuthCheck)

`client.RevalidationConfig()` returns a `*vango.AuthCheckConfig` that:

- runs every `RevalidationInterval` (default: 5m)
- validates the WorkOS session is still active via `ValidateSession` (a network call)
- uses `FailureMode: RevalidationFailureMode` (default: `vango.FailOpenWithGrace`) with `MaxStaleSession` (default: 15m)
- when session is expired, forces reload and sends the user to `/auth/signin`

Security/availability tradeoffs:

- `vango.FailOpenWithGrace` (default): keeps sessions alive during transient WorkOS/API outages, but a revoked WorkOS session may remain usable until the next successful revalidation or until `MaxStaleSession` elapses.
- `vango.FailClosed` (strict): expires sessions immediately on any revalidation failure (more secure, but can force logouts during WorkOS/API outages).

To opt into strict mode:

```go
RevalidationFailureMode: vango.FailClosed,
```

Regardless of periodic settings, high-value operations should call `ctx.RevalidateAuth()` immediately before sensitive mutations. `ctx.RevalidateAuth()` is fail-closed: it requires a successful active check and returns an error on any failure.

If you do not want periodic checks, set:

```go
DisablePeriodicSessionValidation: true
```

## Webhooks

### Registering the webhook endpoint

Mount it on a mux:

```go
mux.Handle("/webhooks/workos", client.WebhookHandler(
	workos.OnDirectoryUserCreated(handleUserCreated),
	workos.OnDirectoryUserDeleted(handleUserDeleted),
	workos.OnAnyEvent(handleAny),
))
```

For mutating webhook consumers, prefer the retry-aware entrypoint:

```go
store := workos.NewMemoryWebhookIdempotencyStore() // single-process only

mux.Handle("/webhooks/workos", client.WebhookHandlerWithOptions(
	workos.WebhookHandlerOptions{
		IdempotencyStore: store,
	},
	workos.OnUserCreatedErr(func(ctx context.Context, e workos.WebhookEvent) error {
		// enqueue durable work or commit your transaction here
		return nil
	}),
))
```

Requirements:

- `WebhookSecret` must be set, otherwise the handler returns `500`.
- Requests must be `POST`.
- The handler enforces `WebhookMaxBodyBytes` (default: 1 MiB).
- Signature verification uses the `WorkOS-Signature` header.
- WorkOS delivery is at-least-once. If you mutate state, use `WebhookHandlerWithOptions(...)` and key idempotency by `WebhookEvent.ID`.
- Error-capable handlers (`OnUserCreatedErr`, `OnAnyEventErr`, etc.) intentionally return non-2xx on failure so WorkOS retries delivery.
- Subscriber panics are recovered by `vango-workos`; dispatch stops and the endpoint returns `500` instead of relying on a global panic middleware to keep the process alive.
- `NewMemoryWebhookIdempotencyStore()` is suitable only for tests, local development, and single-instance deployments. Use a shared backend for multi-instance deployments.

### Handling webhook payloads

`workos.WebhookEvent` provides:

- `ID` string (delivery idempotency key)
- `Event` string (e.g. `dsync.user.created`)
- `Data` as `json.RawMessage`

In your handler, unmarshal `Data` into whatever shape you need:

```go
type DirectoryUserCreated struct {
	ID string `json:"id"`
	// Add fields you depend on; keep it minimal and version-tolerant.
}

func handleUserCreated(ctx context.Context, e workos.WebhookEvent) {
	var payload DirectoryUserCreated
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		return
	}
	// Perform your side effects here (db writes, enqueue jobs, etc.).
}
```

Recommended delivery contract:

- treat `WebhookEvent.ID` as the idempotency key
- keep handlers fast; enqueue durable work rather than doing slow side effects inline
- return an error only when you want WorkOS to retry the delivery
- if a subscriber panics, `vango-workos` recovers it, aborts the delivery, and returns `500`; global server panic recovery is still useful generally, but webhook safety does not depend on it
- distributed-store contract:
  - `Claim(key, leaseTTL)` acquires an in-flight lease only when the key is absent or expired
  - `MarkProcessed(key, token, ttl)` atomically converts the matching in-flight lease into a processed marker
  - `Release(key, token)` deletes only the matching in-flight lease so stale workers cannot clear a newer claim

## Admin Portal Links

Use `GenerateAdminPortalLink` to generate a link that sends the user to the WorkOS Admin Portal for a specific organization:

```go
link, err := client.GenerateAdminPortalLink(
	ctx,
	identity.OrgID,
	workos.AdminPortalSSO,
	"https://app.example.com/settings/sso",
)
if err != nil {
	return err
}
http.Redirect(w, r, link, http.StatusSeeOther)
```

Available intents:

- `workos.AdminPortalSSO`
- `workos.AdminPortalDSync`
- `workos.AdminPortalAuditLogs`
- `workos.AdminPortalLogStreams`
- `workos.AdminPortalCertRenewal`
- `workos.AdminPortalDomainVerification`

## Enterprise API Surface

The `*workos.Client` implements:

- Sessions:
  - `VerifyAccessToken(ctx, accessToken)` (local JWT verification, JWKS)
  - `RefreshTokens(ctx, refreshToken)` (refresh rotation safe, process-local coordination)
  - `ValidateSession(ctx, userID, sessionID)` (active revocation detection)
  - `RevokeSession(ctx, sessionID)` (logout everywhere)
- Users:
  - `GetUser`, `ListUsers`, `UpdateUser`, `DeleteUser`
- Orgs:
  - `GetOrganization`, `ListOrganizations`
  - `ListOrganizationMemberships`, `GetOrganizationMembership`
- RBAC:
  - `HasRole(userID, orgID, roleSlug)`
  - `ListRoles(opts)`:
    - if `OrganizationID` is set, lists roles for that org
    - otherwise aggregates environment roles across organizations (best-effort)
- SSO:
  - `ListConnections(opts)`
- Directory Sync:
  - `ListDirectories`, `ListDirectoryUsers`, `ListDirectoryGroups`
- Audit logs:
  - `EmitAuditEvent(ctx, event)` (no-op unless `EnableAuditLogs=true`)

### Audit log emission

Call audit log emission from HTTP handlers or action work functions only (never from component render closures / session loop work):

```go
err := client.EmitAuditEvent(ctx, workos.AuditEvent{
	OrganizationID: identity.OrgID,
	Action:         "project.deleted",
	Actor: workos.AuditActor{
		ID:   identity.UserID,
		Type: "user",
		Metadata: map[string]any{
			"email": identity.Email,
		},
	},
	Targets: []workos.AuditTarget{
		{ID: projectID, Type: "project"},
	},
	Context: workos.AuditContext{
		UserAgent: r.UserAgent(),
	},
})
```

## Logout

### How logout works

`POST /auth/logout`:

- clears the local session cookie (forces re-auth on next request)
- best-effort revokes the WorkOS session via `RevokeSession`
- redirects through WorkOS logout when it has a session ID
- lands on `/auth/signed-out`, which broadcasts “logout” to other tabs and clears Vango resume keys, then redirects to `return_to` (safe-redirect rules apply)
- logout, signed-out HTML, and signed-out script responses are also explicitly non-cacheable for the same reason

### Rendering a logout button

Because logout is POST-only and should be CSRF-protected, render it as a form:

```html
<form method="POST" action="/auth/logout">
  <button type="submit">Sign out</button>
</form>
```

## Cookie Secret Rotation

To rotate cookie secrets without signing everyone out:

1. Deploy with:
   - `CookieSecret` = new secret
   - `CookieSecretFallbacks` includes the old secret(s)
2. Wait for cookies to refresh naturally (or trigger a re-auth).
3. Remove old secrets from `CookieSecretFallbacks` in a later deploy.

Internally:

- The cookie is encrypted and authenticated using AES-256-GCM.
- The configured `CookieName` is used as Additional Authenticated Data (AAD), so cookies are bound to the cookie name.

## Multi-Instance Deployments and Refresh Races

By default, the middleware refreshes access tokens when they are expired and a refresh token exists.

Refresh coordination is process-local. In multi-instance deployments without sticky sessions, you may see refresh races across instances.

Mitigation option:

```go
DisableRefreshInMiddleware: true
```

With refresh disabled in middleware:

- expired access tokens cause the cookie to be cleared at the HTTP boundary, which forces a fresh AuthKit sign-in on the next request
- resume revalidation still runs via `OnSessionResume`
- periodic revalidation still runs via `AuthCheck` if enabled

## Testing

### Mocking WorkOS in unit tests

Use `workos.TestAuth` (implements `workos.Auth`) and override only the methods you need:

```go
auth := &workos.TestAuth{
	GetUserFunc: func(ctx context.Context, userID string) (*workos.User, error) {
		return &workos.User{ID: userID, Email: "test@example.com"}, nil
	},
}
```

### Creating test identities

```go
identity := workos.TestIdentity(
	workos.WithUserID("user_test_123"),
	workos.WithPermissions("projects:delete"),
)
```

### Hydrating Vango session auth state in tests

If you are unit-testing Vango code that relies on `pkg/auth` projections, use:

```go
workos.HydrateSessionForTest(session, identity)
```

This writes the same runtime auth projections that the SessionBridge writes in production.

## Troubleshooting

### `workos: missing identity on resume (middleware not applied?)`

Cause: `client.Middleware()` is not installed on the server handler chain that serves the Vango app.

Fix:

- Ensure you called `app.Server().Use(client.Middleware())`.
- Ensure the handler that serves `/` is the Vango app (or mux that routes to it), and that `Use(...)` applies to that handler.

### “Authentication failed” on callback

Common causes:

- `RedirectURI` mismatch with WorkOS Dashboard AuthKit redirect URI
- `JWTIssuer` / `JWTAudience` misconfigured
- JWKS fetch failures (network, wrong `JWKSURL`, timeout)

### Webhooks return 401 “Invalid signature”

Check:

- `WebhookSecret` matches the WorkOS webhook secret
- The incoming request includes the `WorkOS-Signature` header
- Your reverse proxy does not modify the raw request body before it reaches the handler

### Users get redirected to `/` instead of `return_to`

`return_to` is rejected unless it is:

- a safe absolute-path (`/foo` but not `//foo`), or
- an absolute URL whose origin exactly matches `BaseURL`

## Reference

### Standard auth routes

- `GET /auth/signin`
- `GET /auth/signup`
- `GET /auth/callback`
- `POST /auth/logout`
- `GET /auth/signed-out`
- `GET /auth/signed-out.js`

### Important `workos.Config` fields

Required:

- `APIKey` (`sk_...`)
- `ClientID` (`client_...`)
- `RedirectURI` (must match WorkOS Dashboard redirect)
- `CookieSecret` (>= 32 chars)
- `BaseURL` (`http://localhost:8080` in dev; `https://...` in prod)

Frequently used:

- `WebhookSecret`
- `CookieSecretFallbacks`
- `EnableAuditLogs`

Operational tuning:

- `RevalidationInterval`, `RevalidationTimeout`, `MaxStaleSession`
- `DisablePeriodicSessionValidation`
- `DisableRefreshInMiddleware`
- `JWKSCacheDuration`, `JWKSFetchTimeout`, `JWKSURL`, `JWTIssuer`, `JWTAudience`
- `CookieName`, `CookieMaxAge`, `CookieSecure`, `CookieSameSite`
- `WebhookMaxBodyBytes`

### WorkOS Vault Usage via official Go SDK (API Keys and Secrets)

If your app uses WorkOS Vault to store tenant secrets (API keys, tokens, credentials), follow these rules. All Vault API calls must occur in Resource loaders, Action work functions, or HTTP handlers — the same lifecycle rule as any other I/O. Never store decrypted secret values in SharedSignal, GlobalSignal, SessionKey, or session runtime KV. Decrypted material should exist only as transient local variables in the work function that needs it. Always include organization_id in your Vault key context for tenant-scoped secrets, using the identity already available from workos.CurrentIdentity(ctx). Mark any struct field containing secret plaintext with json:"-" to prevent accidental serialization.
