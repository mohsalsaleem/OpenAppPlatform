-- name: ListApplicationGroups :many
SELECT g.id,g.name,g.version,jsonb_agg(jsonb_build_object(
 'id',a.id,'groupId',a.group_id,'manifest',a.spec,'createdAt',a.created_at,'updatedAt',a.updated_at,'version',a.version) ORDER BY (a.environment='staging') DESC,a.environment)
 FROM oap_application_groups g JOIN oap_applications a ON a.group_id=g.id GROUP BY g.id ORDER BY g.name,g.id;

-- name: GetApplicationGroupID :one
SELECT group_id FROM oap_applications WHERE id=$1;

-- name: LockApplicationGroup :one
SELECT version FROM oap_application_groups WHERE id=$1 FOR UPDATE;

-- name: CountGroupEnvironments :one
SELECT count(*) FROM oap_applications WHERE group_id=$1;

-- name: MoveEnvironmentIfGroup :execrows
UPDATE oap_applications SET group_id=$1 WHERE id=$2 AND group_id=$3;

-- name: BumpApplicationGroups :execrows
UPDATE oap_application_groups SET version=version+1 WHERE id=ANY($1::text[]);

-- name: LockEnvironmentName :one
SELECT name FROM oap_applications WHERE id=$1 AND group_id=$2 FOR UPDATE;

-- name: InsertApplicationGroup :execrows
INSERT INTO oap_application_groups(id,name) VALUES($1,$2);

-- name: MoveEnvironment :execrows
UPDATE oap_applications SET group_id=$1 WHERE id=$2;

-- name: BumpApplicationGroup :execrows
UPDATE oap_application_groups SET version=version+1 WHERE id=$1;

-- name: UpsertTarget :execrows
INSERT INTO oap_targets(id,spec,token_env) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET spec=$2,token_env=$3;

-- name: GetTarget :one
SELECT spec,token_env FROM oap_targets WHERE id=$1;

-- name: ListTargets :many
SELECT spec FROM oap_targets ORDER BY id;

-- name: InsertApplication :one
INSERT INTO oap_applications(id,name,environment,spec,group_id) VALUES($1,$2,$3,$4,$1) RETURNING created_at,updated_at,version;

-- name: InsertObservedBinding :execrows
INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal) VALUES($1,$2,$3,$4,1);

-- name: ReserveBinding :execrows
INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(target_id,resource_id) DO NOTHING;

-- name: GetBindingOwner :one
SELECT application_id,component,ordinal FROM oap_bindings WHERE target_id=$1 AND resource_id=$2;

-- name: ReactivateBinding :execrows
UPDATE oap_bindings SET retired_at=NULL WHERE target_id=$1 AND resource_id=$2 AND application_id=$3 AND component=$4 AND ordinal=$5;

-- name: GetApplication :one
SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications WHERE id=$1;

-- name: ListApplications :many
SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications ORDER BY created_at DESC;

-- name: UpdateDeployment :execrows
UPDATE oap_deployments SET state=$2,steps=$3,updated_at=now() WHERE id=$1;

-- name: UpdateApplication :one
UPDATE oap_applications SET spec=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=$3 RETURNING id,spec,created_at,updated_at,version,group_id;

-- name: ListBindings :many
SELECT component,ordinal,resource_id,(retired_at IS NOT NULL)::boolean AS retired FROM oap_bindings WHERE application_id=$1 ORDER BY component,ordinal;

-- name: UpdateDeploymentReturningTimestamp :one
UPDATE oap_deployments SET state=$2,steps=$3,updated_at=now() WHERE id=$1 RETURNING updated_at;

-- name: GetActiveBinding :one
SELECT resource_id FROM oap_bindings WHERE application_id=$1 AND component=$2 AND ordinal=$3 AND retired_at IS NULL;

-- name: RetireBinding :execrows
UPDATE oap_bindings SET retired_at=COALESCE(retired_at,now()) WHERE target_id=$1 AND resource_id=$2 AND application_id=$3 AND component=$4 AND ordinal=$5;

-- name: LockTargetScope :execrows
SELECT pg_advisory_xact_lock(hashtextextended($1,0));

-- name: TargetScopeExists :one
SELECT EXISTS(SELECT 1 FROM oap_targets WHERE id=$1 OR (spec->>'operator'=$2::text AND rtrim(spec->>'url','/')=$3::text AND spec->>'projectId'=$4::text AND spec->>'environment'=$5::text));

-- name: InsertTarget :execrows
INSERT INTO oap_targets(id,spec,token_env) VALUES($1,$2,$3);

-- name: ListRuntimeVariables :many
SELECT key,provider_uuid,value_hash,intent_hash FROM oap_runtime_variables WHERE target_id=$1 AND resource_id=$2;

-- name: BeginRuntimeVariable :execrows
INSERT INTO oap_runtime_variables(target_id,resource_id,key,provider_uuid,intent_hash) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(target_id,resource_id,key) DO UPDATE SET intent_hash=$5 WHERE oap_runtime_variables.provider_uuid=$4;

-- name: CommitRuntimeVariable :execrows
UPDATE oap_runtime_variables SET provider_uuid=$4,value_hash=$5,intent_hash='' WHERE target_id=$1 AND resource_id=$2 AND key=$3 AND intent_hash=$5 AND (provider_uuid='' OR provider_uuid=$4);

-- name: DeleteRuntimeVariable :execrows
DELETE FROM oap_runtime_variables WHERE target_id=$1 AND resource_id=$2 AND key=$3 AND provider_uuid=$4;

-- name: GetDeployment :one
SELECT id,application_id,state,spec,steps,created_at,updated_at,definition_version,operation,source FROM oap_deployments WHERE id=$1;

-- name: ListDeployments :many
SELECT id,application_id,state,spec,steps,created_at,updated_at,definition_version,operation,source FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC LIMIT 50;

-- name: HasLegacyActiveRelease :one
SELECT EXISTS(SELECT 1 FROM oap_deployments d WHERE application_id=$1 AND state IN ('queued','running','attention') AND NOT EXISTS(SELECT 1 FROM oap_release_claims c WHERE c.release_id=d.id));

-- name: HasComponentConflict :one
SELECT EXISTS(SELECT 1 FROM oap_release_claims c JOIN oap_deployments d ON d.id=c.release_id WHERE d.application_id=$1 AND d.state IN ('queued','running','attention') AND c.component=$2 AND (c.mode='write' OR $3::text='write'));

-- name: InsertReleaseClaim :exec
INSERT INTO oap_release_claims(release_id,component,mode) VALUES($1,$2,$3);
