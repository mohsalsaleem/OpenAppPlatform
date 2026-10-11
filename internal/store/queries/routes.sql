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
