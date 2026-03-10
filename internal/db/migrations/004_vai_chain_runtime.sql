-- +goose Up
CREATE TABLE IF NOT EXISTS vai_sessions (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  external_session_id TEXT,
  created_by_principal_id TEXT NOT NULL DEFAULT '',
  created_by_principal_type TEXT NOT NULL DEFAULT '',
  actor_id TEXT,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  latest_chain_id TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS vai_sessions_org_external_idx
  ON vai_sessions (org_id, external_session_id)
  WHERE external_session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS vai_chains (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  session_id TEXT REFERENCES vai_sessions(id) ON DELETE SET NULL,
  external_session_id TEXT,
  created_by_principal_id TEXT NOT NULL DEFAULT '',
  created_by_principal_type TEXT NOT NULL DEFAULT '',
  actor_id TEXT,
  parent_chain_id TEXT REFERENCES vai_chains(id) ON DELETE SET NULL,
  forked_from_run_id TEXT,
  status TEXT NOT NULL,
  chain_version BIGINT NOT NULL DEFAULT 1,
  message_count_cached INTEGER NOT NULL DEFAULT 0,
  token_estimate_cached INTEGER NOT NULL DEFAULT 0,
  current_defaults_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  resume_token_hash TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS vai_chains_session_idx
  ON vai_chains (session_id, created_at ASC);
CREATE INDEX IF NOT EXISTS vai_chains_org_updated_idx
  ON vai_chains (org_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS vai_chain_messages (
  id TEXT PRIMARY KEY,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  run_id TEXT,
  role TEXT NOT NULL,
  sequence_in_chain INTEGER NOT NULL,
  content_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS vai_chain_messages_chain_sequence_idx
  ON vai_chain_messages (chain_id, sequence_in_chain);
CREATE INDEX IF NOT EXISTS vai_chain_messages_run_idx
  ON vai_chain_messages (run_id);

CREATE TABLE IF NOT EXISTS vai_runs (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  session_id TEXT REFERENCES vai_sessions(id) ON DELETE SET NULL,
  parent_run_id TEXT,
  rerun_of_run_id TEXT,
  idempotency_key TEXT,
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  stop_reason TEXT NOT NULL DEFAULT '',
  effective_config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  effective_request_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  usage_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  tool_count INTEGER NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS vai_runs_chain_started_idx
  ON vai_runs (chain_id, started_at ASC);
CREATE INDEX IF NOT EXISTS vai_runs_session_started_idx
  ON vai_runs (session_id, started_at ASC);

CREATE TABLE IF NOT EXISTS vai_run_items (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES vai_runs(id) ON DELETE CASCADE,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  step_index INTEGER NOT NULL DEFAULT 0,
  step_id TEXT,
  execution_id TEXT,
  sequence_in_run INTEGER NOT NULL DEFAULT 0,
  sequence_in_chain INTEGER NOT NULL DEFAULT 0,
  content_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  tool_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  asset_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS vai_run_items_run_sequence_idx
  ON vai_run_items (run_id, sequence_in_run ASC, created_at ASC);
CREATE INDEX IF NOT EXISTS vai_run_items_chain_sequence_idx
  ON vai_run_items (chain_id, sequence_in_chain ASC, created_at ASC);
CREATE INDEX IF NOT EXISTS vai_run_items_execution_idx
  ON vai_run_items (execution_id)
  WHERE execution_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS vai_run_tool_calls (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES vai_runs(id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL DEFAULT 0,
  execution_id TEXT NOT NULL,
  tool_call_id TEXT,
  name TEXT NOT NULL,
  effect_class TEXT NOT NULL DEFAULT 'unknown',
  input_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  call_item_id TEXT,
  result_item_id TEXT,
  is_error BOOLEAN NOT NULL DEFAULT false,
  execution_outcome TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS vai_run_tool_calls_run_idx
  ON vai_run_tool_calls (run_id, step_index, created_at ASC);

CREATE TABLE IF NOT EXISTS vai_attachments (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  principal_id TEXT NOT NULL,
  principal_type TEXT NOT NULL,
  actor_id TEXT,
  attachment_role TEXT NOT NULL,
  mode TEXT NOT NULL,
  protocol TEXT NOT NULL,
  credential_scope_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  resume_token_hash TEXT NOT NULL DEFAULT '',
  node_id TEXT NOT NULL DEFAULT '',
  close_reason TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS vai_attachments_chain_started_idx
  ON vai_attachments (chain_id, started_at DESC);

CREATE TABLE IF NOT EXISTS vai_idempotency_records (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  principal_id TEXT NOT NULL,
  chain_id TEXT NOT NULL DEFAULT '',
  operation TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  result_ref_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS vai_idempotency_scope_idx
  ON vai_idempotency_records (org_id, principal_id, chain_id, operation, idempotency_key);
CREATE INDEX IF NOT EXISTS vai_idempotency_expires_idx
  ON vai_idempotency_records (expires_at);

CREATE TABLE IF NOT EXISTS vai_compactions (
  id TEXT PRIMARY KEY,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  created_by_run_id TEXT,
  replaced_through_item_id TEXT,
  summary_item_id TEXT,
  snapshot_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS vai_compactions_chain_idx
  ON vai_compactions (chain_id, created_at DESC);

CREATE TABLE IF NOT EXISTS vai_provider_continuations (
  id TEXT PRIMARY KEY,
  chain_id TEXT NOT NULL REFERENCES vai_chains(id) ON DELETE CASCADE,
  run_id TEXT REFERENCES vai_runs(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  handle_kind TEXT NOT NULL,
  handle_value_encrypted TEXT NOT NULL,
  valid_from_chain_version BIGINT NOT NULL DEFAULT 0,
  invalidated_at TIMESTAMPTZ,
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS vai_provider_continuations_chain_idx
  ON vai_provider_continuations (chain_id, created_at DESC);

CREATE TABLE IF NOT EXISTS vai_assets (
  id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL,
  storage_provider TEXT NOT NULL,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  media_type TEXT NOT NULL,
  validated_media_type TEXT,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL DEFAULT '',
  scan_status TEXT NOT NULL DEFAULT 'unknown',
  metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS vai_assets_org_created_idx
  ON vai_assets (org_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS vai_assets_org_object_idx
  ON vai_assets (org_id, bucket, object_key);

-- +goose Down
DROP TABLE IF EXISTS vai_assets;
DROP TABLE IF EXISTS vai_provider_continuations;
DROP TABLE IF EXISTS vai_compactions;
DROP TABLE IF EXISTS vai_idempotency_records;
DROP TABLE IF EXISTS vai_attachments;
DROP TABLE IF EXISTS vai_run_tool_calls;
DROP TABLE IF EXISTS vai_run_items;
DROP TABLE IF EXISTS vai_runs;
DROP TABLE IF EXISTS vai_chain_messages;
DROP TABLE IF EXISTS vai_chains;
DROP TABLE IF EXISTS vai_sessions;
