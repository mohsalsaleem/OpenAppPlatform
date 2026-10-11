CREATE TABLE oap_route_automation (
 application_id text NOT NULL REFERENCES oap_applications(id),
 name text NOT NULL,
 owner_id text NOT NULL REFERENCES oap_users(id),
 enabled boolean NOT NULL DEFAULT false,
 authority_hash text NOT NULL,
 directory_hash text NOT NULL,
 native_hash text NOT NULL,
 pending_id text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'idle',
 message text NOT NULL DEFAULT '',
 checked_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(application_id,name)
);
