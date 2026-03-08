-- +goose Up
CREATE TABLE IF NOT EXISTS gateway_request_logs (
  request_id TEXT PRIMARY KEY,
  org_id TEXT NOT NULL REFERENCES app_orgs(id) ON DELETE CASCADE,
  gateway_api_key_id TEXT NOT NULL REFERENCES gateway_api_keys(id) ON DELETE CASCADE,
  session_id TEXT,
  chain_id TEXT NOT NULL,
  parent_request_id TEXT REFERENCES gateway_request_logs(request_id) ON DELETE SET NULL,
  endpoint_kind TEXT NOT NULL,
  endpoint_family TEXT NOT NULL,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  key_source TEXT NOT NULL DEFAULT '',
  access_credential TEXT NOT NULL DEFAULT '',
  status_code INTEGER NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  input_context_fingerprint TEXT,
  output_context_fingerprint TEXT,
  request_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
  response_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
  request_body TEXT NOT NULL DEFAULT '',
  response_body TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  error_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_request_logs_org_completed_idx
  ON gateway_request_logs (org_id, completed_at DESC);
CREATE INDEX IF NOT EXISTS gateway_request_logs_org_session_idx
  ON gateway_request_logs (org_id, session_id, completed_at DESC);
CREATE INDEX IF NOT EXISTS gateway_request_logs_org_chain_idx
  ON gateway_request_logs (org_id, chain_id, completed_at DESC);
CREATE INDEX IF NOT EXISTS gateway_request_logs_org_api_key_idx
  ON gateway_request_logs (org_id, gateway_api_key_id, completed_at DESC);
CREATE INDEX IF NOT EXISTS gateway_request_logs_link_lookup_idx
  ON gateway_request_logs (org_id, gateway_api_key_id, endpoint_family, session_id, output_context_fingerprint, completed_at DESC);
CREATE INDEX IF NOT EXISTS gateway_request_logs_link_lookup_no_session_idx
  ON gateway_request_logs (org_id, gateway_api_key_id, endpoint_family, output_context_fingerprint, completed_at DESC)
  WHERE session_id IS NULL;

CREATE TABLE IF NOT EXISTS gateway_run_traces (
  request_id TEXT PRIMARY KEY REFERENCES gateway_request_logs(request_id) ON DELETE CASCADE,
  session_id TEXT,
  chain_id TEXT NOT NULL,
  parent_request_id TEXT,
  run_config JSONB NOT NULL DEFAULT '{}'::jsonb,
  usage JSONB NOT NULL DEFAULT '{}'::jsonb,
  stop_reason TEXT NOT NULL DEFAULT '',
  turn_count INTEGER NOT NULL DEFAULT 0,
  tool_call_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_run_traces_chain_idx
  ON gateway_run_traces (chain_id, created_at DESC);

CREATE TABLE IF NOT EXISTS gateway_run_steps (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES gateway_run_traces(request_id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  response_body TEXT NOT NULL DEFAULT '',
  response_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (request_id, step_index)
);

CREATE TABLE IF NOT EXISTS gateway_run_tool_calls (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL REFERENCES gateway_run_traces(request_id) ON DELETE CASCADE,
  step_index INTEGER NOT NULL,
  tool_call_id TEXT NOT NULL,
  name TEXT NOT NULL,
  input_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  result_body TEXT NOT NULL DEFAULT '',
  is_error BOOLEAN NOT NULL DEFAULT false,
  error_summary TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS gateway_run_tool_calls_request_idx
  ON gateway_run_tool_calls (request_id, step_index, created_at);

-- +goose Down
DROP TABLE IF EXISTS gateway_run_tool_calls;
DROP TABLE IF EXISTS gateway_run_steps;
DROP TABLE IF EXISTS gateway_run_traces;
DROP TABLE IF EXISTS gateway_request_logs;
