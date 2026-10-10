ALTER TABLE oap_source_events DROP CONSTRAINT oap_source_events_state_check;
ALTER TABLE oap_source_events ADD CONSTRAINT oap_source_events_state_check CHECK(state IN ('received','building','built','released','attention','observing','observed'));
ALTER TABLE oap_source_events ADD COLUMN observation_started_at timestamptz NOT NULL DEFAULT now();
