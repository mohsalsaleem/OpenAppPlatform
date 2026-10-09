# Read only MCP interface

OpenAppPlatform includes a standalone stdio MCP server. It exposes application
inspection and deployment planning through the authenticated REST API. It does
not deploy, edit, scale, delete, or run arbitrary commands.

## Build and run

```sh
python3 scripts/run-local.py go build -o bin/oap-mcp ./cmd/oap-mcp
python3 scripts/run-local.py bin/oap-mcp -url http://127.0.0.1:8787
```

The helper loads OAP_API_TOKEN from local configuration without putting its value
in the command line. A client launches this process and sends newline-delimited
JSON-RPC messages through stdin. stdout contains protocol messages only; startup
errors go to stderr. The server supports protocol version 2024-11-05.

The platform URL must use HTTPS except localhost. Redirects are not followed.
Never put the platform token in prompts or tool arguments.

## Tools

| Tool | Input | Result |
| --- | --- | --- |
| list_applications | None | Application definitions and versions |
| get_application | applicationId | One definition |
| list_deployments | applicationId | Recent frozen releases and component outcomes |
| get_deployment | deploymentId | Durable release progress |
| inspect_target | targetId | Resources inside the configured target |
| plan_deployment | applicationId | Definition version and standard-deployment effects; executed is false |

The default server is deliberately read-only. Its plan output is evidence for a
user or agent to review, not authorization to execute a release. REST deployment
requests can include expectedVersion so a reviewed plan is rejected if the
definition changes before it is queued.

This is an agent interface, not an embedded generative assistant. Provider
selection, model budgets, conversational UI, and approved mutation tools remain
future work. Application data returned by tools is untrusted source material.
