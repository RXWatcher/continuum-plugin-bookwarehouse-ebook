-- app_config holds plugin-owned configuration that the admin SPA can edit
-- without going through the host's plugin Configure form. Singleton row, JSON
-- blob keyed by config section. The bootstrap fields (database_url, base_url,
-- api_key) stay in the host's plugin config; everything else moves here.
CREATE TABLE IF NOT EXISTS app_config (
  id         INT PRIMARY KEY DEFAULT 1,
  data       JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_config_singleton CHECK (id = 1)
);

INSERT INTO app_config (id, data)
VALUES (1, '{}'::jsonb)
ON CONFLICT (id) DO NOTHING;
