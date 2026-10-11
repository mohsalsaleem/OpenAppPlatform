# Runtime Input Example

This reference-only template demonstrates the P0.1 contract. Replace the target and
image, create the application, then use its returned ID for the exact grant in
[Runtime Secret References](../../docs/runtime-secret-references.md). Supply the
value through the operator, restart OAP, and request a standard deployment.

An unavailable grant must reject deployment without creating a native resource.
This example does not provision a database or verify database connectivity; those
belong to P0.3. Never put a connection string or credential in this template.
