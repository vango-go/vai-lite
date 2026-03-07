-- +goose Up
ALTER TABLE usage_events
  ADD COLUMN IF NOT EXISTS access_credential TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS pricing_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE conversations
SET key_source = CASE key_source
  WHEN 'hosted' THEN 'platform_hosted'
  WHEN 'browser_byok' THEN 'customer_byok_browser'
  WHEN 'external_byok' THEN 'customer_byok_external'
  ELSE key_source
END;

UPDATE conversation_messages
SET key_source = CASE key_source
  WHEN 'hosted' THEN 'platform_hosted'
  WHEN 'browser_byok' THEN 'customer_byok_browser'
  WHEN 'external_byok' THEN 'customer_byok_external'
  ELSE key_source
END;

UPDATE usage_events
SET key_source = CASE key_source
  WHEN 'hosted' THEN 'platform_hosted'
  WHEN 'browser_byok' THEN 'customer_byok_browser'
  WHEN 'external_byok' THEN 'customer_byok_external'
  ELSE key_source
END;

UPDATE usage_events
SET access_credential = CASE
  WHEN request_kind LIKE 'gateway_%' THEN 'gateway_api_key'
  ELSE 'session_auth'
END
WHERE access_credential = '';

UPDATE usage_events
SET pricing_snapshot = jsonb_build_object(
  'catalog_version', pricing_version,
  'model', model,
  'provider', provider,
  'billed_cents', estimated_cost_cents
)
WHERE pricing_snapshot = '{}'::jsonb
  AND pricing_version <> '';

-- +goose Down
UPDATE usage_events
SET key_source = CASE key_source
  WHEN 'platform_hosted' THEN 'hosted'
  WHEN 'customer_byok_browser' THEN 'browser_byok'
  WHEN 'customer_byok_external' THEN 'external_byok'
  ELSE key_source
END;

UPDATE conversation_messages
SET key_source = CASE key_source
  WHEN 'platform_hosted' THEN 'hosted'
  WHEN 'customer_byok_browser' THEN 'browser_byok'
  WHEN 'customer_byok_external' THEN 'external_byok'
  ELSE key_source
END;

UPDATE conversations
SET key_source = CASE key_source
  WHEN 'platform_hosted' THEN 'hosted'
  WHEN 'customer_byok_browser' THEN 'browser_byok'
  WHEN 'customer_byok_external' THEN 'external_byok'
  ELSE key_source
END;

ALTER TABLE usage_events
  DROP COLUMN IF EXISTS pricing_snapshot,
  DROP COLUMN IF EXISTS access_credential;
