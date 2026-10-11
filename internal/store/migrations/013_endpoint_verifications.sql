-- Verification is bounded observational evidence; it never grants publication.
CREATE TABLE oap_endpoint_verifications (
 application_id text NOT NULL REFERENCES oap_applications(id),
 name text NOT NULL,
 report jsonb NOT NULL,
 PRIMARY KEY(application_id,name)
);
