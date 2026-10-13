CREATE TABLE IF NOT EXISTS oap_agent_control (
 workspace_id text PRIMARY KEY CHECK (workspace_id='default'),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,
 reason text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO oap_agent_control(workspace_id) VALUES('default') ON CONFLICT DO NOTHING;
