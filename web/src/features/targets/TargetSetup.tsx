import { useState } from "react";
export function TargetSetup({ existingIds = [] }: { existingIds?: string[] }) {
  const [draft, setDraft] = useState({
    id: "coolify-staging",
    name: "My Coolify",
    url: "",
    projectId: "",
    serverId: "",
    environment: "staging",
    tokenEnv: "COOLIFY_TOKEN",
  });
  const [copied, setCopied] = useState(false);
  let endpoint = false;
  try {
    const u = new URL(draft.url);
    endpoint =
      !u.username &&
      !u.password &&
      !u.search &&
      !u.hash &&
      (u.protocol === "https:" ||
        (u.protocol === "http:" &&
          ["localhost", "127.0.0.1"].includes(u.hostname)));
  } catch {}
  const valid =
    endpoint &&
    !existingIds.includes(draft.id) &&
    /^[a-z][a-z0-9-]{0,47}$/.test(draft.id) &&
    /^[a-z][a-z0-9-]{0,47}$/.test(draft.environment) &&
    draft.name.trim() &&
    draft.projectId.trim() &&
    draft.serverId.trim() &&
    /^[A-Za-z_][A-Za-z0-9_]*$/.test(draft.tokenEnv) &&
    ![
      "DATABASE_URL",
      "OAP_SETUP_TOKEN",
      "OAP_API_TOKEN",
      "OAP_BUILD_TOKEN",
      "OAP_STAGING_HOOK_SECRET",
      "OAP_STAGING_HOOK_CREDENTIAL",
    ].includes(draft.tokenEnv);
  const recipe = JSON.stringify([{ ...draft, operator: "coolify" }], null, 2);
  return (
    <details className="panel">
      <summary>Set Up an Existing Coolify Connection</summary>
      <p className="muted">
        Prepare an OAP connection to your existing project. Native builds,
        domains and deployment triggers stay with Coolify.
      </p>
      <p className="setup-notice">
        Configuration guide: these fields generate a file entry. They do not
        save a connection. Follow the server setup steps below.
      </p>
      <div className="form-grid">
        {Object.entries(draft).map(([key, value]) => (
          <label key={key}>
            {
              (
                {
                  id: "Target ID",
                  name: "Connection Name",
                  url: "Coolify URL",
                  projectId: "Coolify Project ID",
                  serverId: "Coolify Server ID",
                  environment: "Operator Environment",
                  tokenEnv: "Credential Environment Variable",
                } as Record<string, string>
              )[key]
            }
            <input
              value={value}
              onChange={(e) => {
                setCopied(false);
                setDraft({ ...draft, [key]: e.target.value });
              }}
            />
          </label>
        ))}
      </div>
      <ol>
        <li>
          Create or select a Coolify API token with access to the intended
          project. Store its value in the OAP controller environment using the
          variable name above.
        </li>
        <li>
          Add the generated entry to your existing target configuration array.
          Preserve your other entries; do not overwrite the whole file.
        </li>
        <li>
          Mount the configuration, set <code>OAP_TARGETS_FILE</code> to its path
          and restart only the OAP controller. Then verify the connection below
          before grouping resources.
        </li>
      </ol>
      <p className="small muted">
        Use a new target ID for a different project/environment; do not repoint
        an existing connection.
      </p>
      <p className="small muted">
        This guide does not save credentials or configure the server
        automatically. Do not paste token values into these fields.
      </p>
      {valid ? (
        <>
          <pre>{recipe}</pre>
          <button
            className="button"
            onClick={() =>
              navigator.clipboard.writeText(recipe).then(() => setCopied(true))
            }
          >
            {copied ? "Configuration copied" : "Copy Target Configuration"}
          </button>
        </>
      ) : (
        <p role="status">
          Complete the connection fields using an HTTPS URL and a dedicated
          credential reference.
        </p>
      )}
    </details>
  );
}
