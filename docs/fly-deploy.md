# Fly Deploy

This repository now builds a single Vango monolith that serves:

- the Vango web app
- WorkOS auth routes
- raw gateway compatibility routes at `/v1/*`
- Stripe webhooks

## Required secrets

Set these in Fly before first deploy:

- `DATABASE_URL`
- `DATABASE_URL_DIRECT`
- `WORKOS_API_KEY`
- `WORKOS_CLIENT_ID`
- `WORKOS_COOKIE_SECRET`
- `WORKOS_BASE_URL`
- `STRIPE_SECRET_KEY`
- `STRIPE_WEBHOOK_SECRET`
- `S3_ACCESS_KEY_ID`
- `S3_SECRET_ACCESS_KEY`
- `S3_BUCKET`
- `S3_INTENT_SECRET`

Set either:

- `R2_ACCOUNT_ID`

or:

- `S3_ENDPOINT`
- `S3_REGION`

## Optional app config

- `APP_DEFAULT_MODEL`
- `APP_TOPUP_OPTIONS`
- `WORKOS_REDIRECT_URI`
- `WORKOS_COOKIE_SECRET_FALLBACKS`
- `WORKOS_WEBHOOK_SECRET`

## Deploy

```bash
fly launch --copy-config --no-deploy
fly secrets set \
  DATABASE_URL="..." \
  DATABASE_URL_DIRECT="..." \
  WORKOS_API_KEY="..." \
  WORKOS_CLIENT_ID="..." \
  WORKOS_COOKIE_SECRET="..." \
  WORKOS_BASE_URL="https://vai-lite-beta.fly.dev" \
  STRIPE_SECRET_KEY="..." \
  STRIPE_WEBHOOK_SECRET="..." \
  S3_ACCESS_KEY_ID="..." \
  S3_SECRET_ACCESS_KEY="..." \
  S3_BUCKET="..." \
  S3_INTENT_SECRET="..." \
  R2_ACCOUNT_ID="..."
fly deploy
```

The Fly release command runs the monolith once with `APP_MIGRATE_ONLY=1`, which applies Neon migrations and exits before the app machine starts.
