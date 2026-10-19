# OAP protocol definitions

The API, world model, journal events, envelope and extension contracts, defined
once in Protobuf. Code under `gen/` is generated from these files with
`make gen`, using the tool versions pinned in `tools/versions.env`.

| File | Contents |
|---|---|
| `oap/v1/agent.proto` | `AgentService`: the seven primitives (Observe and its stream Watch, Propose, Apply, Await, Explain, Ask, Login) |
| `oap/v1/world.proto` | The world model: host, applications, components, instances, edges, health, affordances |
| `oap/v1/intent.proto` | Intents agents propose |
| `oap/v1/plan.proto` | Proposals, steps, risk, blast radius, change facts |
| `oap/v1/journal.proto` | The shared event format, journal entries and outcomes with evidence |
| `oap/v1/envelope.proto` | The typed autonomy envelope |
| `oap/v1/errors.proto` | Reasons, error details with remedies, and `ask` requests |

## JSON conventions

Every JSON interface (Connect's JSON protocol, MCP, the CLI's `--json` and
`--events`) encodes messages the same way, through `internal/surface/wire`.
Its golden tests pin these rules:

- **Field names as written in the proto** (`base_revision`). Input also accepts
  lowerCamelCase (`baseRevision`).
- **Enums as their full names** (`"VERDICT_ALLOWED"`). Unknown values are errors.
- **Unset fields are omitted.** Fields marked `optional` have explicit presence: a
  value set to zero appears in JSON, an unset one doesn't.
- **Unknown fields are errors**, so a typo is reported instead of ignored.
- **Timestamps** are RFC 3339 in UTC.
- **64-bit integers only for revisions.** They appear as decimal strings
  (`"revision": "57"`); input accepts numbers or strings. Every other integer is
  32-bit. The list of allowed 64-bit fields is checked by a test.
- **Errors** carry an `oap.v1.ErrorDetail` with a stable `why` code and a remedy.

## Compatibility

`buf lint` (STANDARD rules) runs in `make rewrite-check`. Once `v1` is released,
`buf breaking` will reject incompatible changes.
