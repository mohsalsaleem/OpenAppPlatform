# Build on the existing server and deploy through Coolify

The authorized SSH alias `hz_primary` is the Coolify host: its Docker daemon
contains the retained staging fixtures. The existing registry is bound to
127.0.0.1:5001; shared PostgreSQL is container c0kss8s04coc8804ggskko04 on the
`coolify` network. This avoids another registry or GitHub CI.

`scripts/build-on-server.py` implements the trusted image-builder stdin/stdout
contract. It archives the exact local Git object (no working-tree files or secrets),
checks the configured GitHub origin, sends that archive over SSH, and builds/pushes
on the server. OCI revision/source labels and the registry digest are inspected.
A stable build ID owns a locked intent/receipt journal on the server. Completed
requests reuse their receipt. Interrupted builds hold for owner review; the helper
does not blindly rebuild them. Build output goes to stderr, while stdout contains
only the commit/digest result. Use trusted repositories and a configured SSH alias;
this is a single-owner server builder, not a multi-tenant build sandbox.

The platform supports `OAP_TARGETS_FILE` and `OAP_GITHUB_HOOKS_FILE` for read-only
mounted configuration files in a Coolify image application. Command-line flags
remain compatible. Operator secrets stay in environment references.

Deploy the image by immutable registry digest, with a dedicated staging database
and least-privilege role in shared PostgreSQL. Owner mode and secure cookies stay
enabled. First-run signup requires the private setup secret stored in Coolify.
Source configuration and image receipts are non-secret; passwords and setup tokens
must never enter Git or command arguments.
