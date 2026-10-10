CREATE TABLE oap_source_heads (
 workspace_id text NOT NULL DEFAULT 'default' REFERENCES oap_workspaces(id) CHECK(workspace_id='default'),
 hook_id text NOT NULL, application_id text NOT NULL REFERENCES oap_applications(id),
 event_id text NOT NULL REFERENCES oap_source_events(id), commit_sha text NOT NULL,
 PRIMARY KEY(hook_id,application_id)
);
