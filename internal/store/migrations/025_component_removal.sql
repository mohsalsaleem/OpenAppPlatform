ALTER TABLE oap_deployments DROP CONSTRAINT oap_deployments_operation_check;
ALTER TABLE oap_deployments ADD CONSTRAINT oap_deployments_operation_check
 CHECK (operation IN ('deploy','restart','scale-down','remove-component'));
ALTER TABLE oap_action_plans DROP CONSTRAINT oap_action_plans_action_check;
ALTER TABLE oap_action_plans ADD CONSTRAINT oap_action_plans_action_check
 CHECK (action IN ('create','configure','deploy','restart','recover','scale-down','remove-component'));
