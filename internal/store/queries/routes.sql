-- name: InsertRoutePublication :exec
INSERT INTO oap_route_publications(id,application_id,owner_id,plan,state) VALUES($1,$2,$3,$4,'prepared');

-- name: ListRoutePublications :many
SELECT id,owner_id,plan,state,created_at,updated_at FROM oap_route_publications WHERE application_id=$1 ORDER BY (state='publishing') DESC,created_at DESC LIMIT 20;

-- name: GetRoutePublication :one
SELECT id,owner_id,plan,state,created_at,updated_at FROM oap_route_publications WHERE application_id=$1 AND id=$2;

-- name: SetRoutePublicationState :exec
UPDATE oap_route_publications SET state=$3,updated_at=now() WHERE application_id=$1 AND id=$2;

-- name: HasUnresolvedRoutePublication :one
SELECT EXISTS(SELECT 1 FROM oap_route_publications WHERE application_id=$1 AND state='publishing' AND id<>$2);

-- name: SaveEndpointVerification :exec
INSERT INTO oap_endpoint_verifications(application_id,name,report) VALUES($1,$2,$3)
ON CONFLICT(application_id,name) DO UPDATE SET report=excluded.report;

-- name: GetEndpointVerification :one
SELECT report FROM oap_endpoint_verifications WHERE application_id=$1 AND name=$2;

-- name: GetRouteAutomation :one
SELECT * FROM oap_route_automation WHERE application_id=$1 AND name=$2;

-- name: ListRouteAutomation :many
SELECT * FROM oap_route_automation WHERE enabled ORDER BY application_id,name;

-- name: SaveRouteAutomation :exec
INSERT INTO oap_route_automation(application_id,name,owner_id,enabled,authority_hash,directory_hash,native_hash)
VALUES($1,$2,$3,true,$4,$5,$6)
ON CONFLICT(application_id,name) DO UPDATE SET owner_id=excluded.owner_id,enabled=true,authority_hash=excluded.authority_hash,directory_hash=excluded.directory_hash,native_hash=excluded.native_hash,pending_id='',status='idle',message='',checked_at=now();

-- name: UpdateRouteAutomation :exec
UPDATE oap_route_automation SET enabled=$3,native_hash=$4,pending_id=$5,status=$6,message=$7,checked_at=now() WHERE application_id=$1 AND name=$2;

-- name: RouteAutomationOwnerActive :one
SELECT EXISTS(SELECT 1 FROM oap_users WHERE id=$1 AND role='owner' AND active);
