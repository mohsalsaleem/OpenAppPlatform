-- Local durable events only. No network destination or delivery worker is enabled.
CREATE TABLE oap_notification_outbox (
 id text PRIMARY KEY,
 deployment_id text NOT NULL REFERENCES oap_deployments(id),
 application_id text NOT NULL REFERENCES oap_applications(id),
 state text NOT NULL,
 operation text NOT NULL,
 commit_sha text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(deployment_id,state)
);
CREATE INDEX oap_notification_application ON oap_notification_outbox(application_id,created_at DESC);
