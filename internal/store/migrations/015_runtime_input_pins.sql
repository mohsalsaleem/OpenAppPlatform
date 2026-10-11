-- Private recovery metadata. Never expose fingerprints through API/export models.
CREATE TABLE oap_runtime_input_pins (
 deployment_id text NOT NULL REFERENCES oap_deployments(id),
 component text NOT NULL,
 fingerprint text NOT NULL CHECK(length(fingerprint)=64),
 PRIMARY KEY(deployment_id,component)
);
