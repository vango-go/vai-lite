-- +goose Up
CREATE TABLE IF NOT EXISTS app_users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app_orgs (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  allow_byok_override BOOLEAN NOT NULL DEFAULT false,
  hosted_usage_enabled BOOLEAN NOT NULL DEFAULT true,
  default_model TEXT NOT NULL DEFAULT 'oai-resp/gpt-5-mini',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app_memberships (
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);

CREATE TABLE IF NOT EXISTS gateway_api_keys (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS gateway_api_keys_org_idx ON gateway_api_keys (org_id);

CREATE TABLE IF NOT EXISTS provider_secrets (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  object_name TEXT NOT NULL UNIQUE,
  vault_object_id TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  UNIQUE (org_id, provider)
);

CREATE TABLE IF NOT EXISTS wallet_ledger (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  amount_cents BIGINT NOT NULL,
  currency TEXT NOT NULL DEFAULT 'usd',
  description TEXT NOT NULL,
  external_ref TEXT,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS wallet_ledger_org_idx ON wallet_ledger (org_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS wallet_ledger_external_ref_idx ON wallet_ledger (org_id, external_ref) WHERE external_ref IS NOT NULL;

CREATE TABLE IF NOT EXISTS stripe_topups (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  amount_cents BIGINT NOT NULL,
  currency TEXT NOT NULL DEFAULT 'usd',
  checkout_session_id TEXT UNIQUE,
  status TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS processed_stripe_events (
  event_id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  title TEXT NOT NULL,
  model TEXT NOT NULL,
  key_source TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS conversations_org_idx ON conversations (org_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS conversation_messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  parent_id TEXT,
  role TEXT NOT NULL,
  body_text TEXT NOT NULL,
  tool_trace JSONB NOT NULL DEFAULT '[]'::jsonb,
  input_attachments JSONB NOT NULL DEFAULT '[]'::jsonb,
  key_source TEXT NOT NULL,
  usage JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS conversation_messages_conversation_idx ON conversation_messages (conversation_id, created_at);

CREATE TABLE IF NOT EXISTS message_revisions (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  message_id TEXT NOT NULL REFERENCES conversation_messages(id) ON DELETE CASCADE,
  previous_body_text TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS message_revisions_message_idx ON message_revisions (message_id, created_at DESC);

CREATE TABLE IF NOT EXISTS attachments (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  message_id TEXT REFERENCES conversation_messages(id) ON DELETE SET NULL,
  filename TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  blob_ref JSONB NOT NULL,
  created_by TEXT NOT NULL REFERENCES app_users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_events (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  conversation_id TEXT REFERENCES conversations(id) ON DELETE SET NULL,
  message_id TEXT REFERENCES conversation_messages(id) ON DELETE SET NULL,
  request_kind TEXT NOT NULL,
  key_source TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  billable BOOLEAN NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  estimated_cost_cents BIGINT NOT NULL DEFAULT 0,
  pricing_version TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS usage_events_org_idx ON usage_events (org_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS message_revisions;
DROP TABLE IF EXISTS conversation_messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS processed_stripe_events;
DROP TABLE IF EXISTS stripe_topups;
DROP TABLE IF EXISTS wallet_ledger;
DROP TABLE IF EXISTS provider_secrets;
DROP TABLE IF EXISTS gateway_api_keys;
DROP TABLE IF EXISTS app_memberships;
DROP TABLE IF EXISTS app_orgs;
DROP TABLE IF EXISTS app_users;
