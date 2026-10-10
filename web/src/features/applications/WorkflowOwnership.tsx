import type { Application, Capabilities } from "../../api";

export function WorkflowOwnership({
  application,
  capabilities,
}: {
  application: Application;
  capabilities?: Capabilities;
}) {
  return (
    <section
      className="panel workflow-ownership"
      aria-label="Workflow Ownership"
    >
      <h2>Workflow Ownership</h2>
      <p className="small muted">
        Your existing build and deployment triggers remain in place. Management
        is configured separately for each component.
      </p>
      {application.manifest.components.map((component) => {
        const observed = component.management === "observe";
        return (
          <details key={component.name}>
            <summary>
              {component.name} ·{" "}
              {observed ? "Existing Workflow" : "OAP Image Releases"}
            </summary>
            <dl>
              <dt>Build</dt>
              <dd>
                {observed
                  ? "Existing operator or external pipeline. OAP does not start builds."
                  : "Supply a built image. An OAP builder is optional and must be configured separately."}
              </dd>
              <dt>Deploy</dt>
              <dd>
                {observed
                  ? "The existing workflow owns deployments. OAP observes health and logs."
                  : capabilities
                    ? capabilities.standard
                      ? "Open App Platform coordinates image deployments through this platform. Deployment may interrupt requests."
                      : "Standard image releases are unsupported by this target."
                    : "Target capability information is unavailable."}
              </dd>
              <dt>Restart</dt>
              <dd>
                {observed
                  ? "Use the existing operator. OAP restart is blocked while observing."
                  : capabilities
                    ? capabilities.restart
                      ? "Available for active instances when you have permission to operate this application."
                      : "Unsupported by this target."
                    : "Target capability information is unavailable."}
              </dd>
              <dt>Configuration</dt>
              <dd>
                {observed
                  ? "Native runtime configuration stays with the operator."
                  : "Saving updates the OAP definition. Apply supported runtime changes with a separate deployment."}
              </dd>
              <dt>Management Handoff</dt>
              <dd>
                {observed
                  ? capabilities
                    ? capabilities.managementHandoff
                      ? "Eligible image-backed resources require an explicit, version-checked handoff. Native source handoff is unavailable."
                      : "Unavailable for this target."
                    : "Target capability information is unavailable."
                  : "OAP coordinates this component. Existing native triggers are preserved."}
              </dd>
            </dl>
          </details>
        );
      })}
    </section>
  );
}
