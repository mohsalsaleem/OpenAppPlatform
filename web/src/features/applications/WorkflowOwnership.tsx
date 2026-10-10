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
      aria-label="Workflow ownership"
    >
      <h2>Workflow ownership</h2>
      <p className="small muted">
        Grouping preserves existing triggers. Operator support does not grant
        OAP permission to manage an observed component.
      </p>
      {application.manifest.components.map((component) => {
        const observed = component.management === "observe";
        return (
          <details key={component.name}>
            <summary>
              {component.name} ·{" "}
              {observed ? "Existing workflow" : "OAP image releases"}
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
                  ? "Existing workflow owns deployments. OAP observes health and logs."
                  : capabilities
                    ? capabilities.standard
                      ? "OAP coordinates standard image releases; the target executes them. Requests may be interrupted."
                      : "Standard image releases are unsupported by this target."
                    : "Target capability information is unavailable."}
              </dd>
              <dt>Restart</dt>
              <dd>
                {observed
                  ? "Use the existing operator. OAP restart is blocked while observing."
                  : capabilities
                    ? capabilities.restart
                      ? "Available through OAP for bound, active instances with operate access."
                      : "Unsupported by this target."
                    : "Target capability information is unavailable."}
              </dd>
              <dt>Configuration</dt>
              <dd>
                {observed
                  ? "Native runtime configuration stays with the operator."
                  : "Saving updates the OAP definition. Apply supported runtime changes with a separate deployment."}
              </dd>
              <dt>Management handoff</dt>
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
