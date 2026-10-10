CREATE TABLE oap_runtime_variables (
 target_id text NOT NULL,
 resource_id text NOT NULL,
 key text NOT NULL,
 provider_uuid text NOT NULL DEFAULT '',
 value_hash text NOT NULL DEFAULT '',
 intent_hash text NOT NULL DEFAULT '',
 PRIMARY KEY(target_id,resource_id,key),
 FOREIGN KEY(target_id,resource_id) REFERENCES oap_bindings(target_id,resource_id)
);
