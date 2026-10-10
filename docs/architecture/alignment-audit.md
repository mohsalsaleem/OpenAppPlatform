# Architecture alignment audit and correction

11 October 2026. This correction addresses the discrepancies between the agreed
stack/product direction and the implementation. It does not replace the ordered
M0 roadmap or imply completion of its later capabilities.

| Finding | Correction | Evidence |
| --- | --- | --- |
| Custom UI foundation replaced Tailwind/shadcn | Tailwind Vite integration, official repository-owned shadcn primitives, Radix dialogs/tabs, migrated controls/forms/tables/badges/navigation | `web/components.json`, `web/src/components/ui`, dashboard browser workflows |
| sqlc was called optional and unused | Named store SQL, pinned sqlc generation, generated pgx parameter/result types and active store helpers | `sqlc.yaml`, `internal/store/queries`, `internal/store/db`, PostgreSQL/race tests |
| Handwritten client/types diverged from OpenAPI | Updated schemas/operation responses and all dashboard API calls use generated types with typed openapi-fetch operations | `docs/openapi.json`, `web/src/generated/api.ts`, `web/src/api.ts`, TypeScript compilation |
| Live progress/logs used only polling | Authenticated SSE application/release and selected instance log snapshots, reconnect/backoff, revocation checks and bounded writes | `internal/httpapi/streams.go`, `web/src/lib/streams.ts`, stream system/browser tests |
| Entire application allowed only one active release | Durable component writes and transitive dependency/service read claims; explicitly selected component releases in UI/API | Migration 011, claim unit/system tests, independent admission and coordinator reconstruction |
| Existing operators were treated as replaceable build/deploy machinery | Retain the corrected operator-first workflow; preserve native triggers/configuration and observe exact source commits | `operator-first-audit.md`, existing assembly/source acceptance contracts |
| Architecture docs described superseded behavior | Current system design and LLD reconciled; stack is an implementation contract, not silently optional | `system-design.md`, `low-level-design.md`, product design and roadmap |
| Public repository lacked an open-source license | MIT license with shadcn attribution and third-party notices | `LICENSE`, `THIRD_PARTY_NOTICES.md` |

## Deliberate boundaries

- Short application transaction locks remain necessary for admission, version
  snapshots and configuration. Execution conflicts are component-scoped. Recovery
  retains uncertain claims; whole-snapshot rollback/configuration operations can
  conservatively require all active application work to finish.
- Explicit controller/auth SQL still uses pgx for transaction-local authorization
  and locks. Store query definitions and generated helpers are compiled by sqlc;
  introducing sqlc does not replace PostgreSQL transaction semantics.
- SSE transports authoritative snapshots; the server samples stored state rather
  than introducing a separate event broker. Provider health and native source
  activity still poll bounded adapter APIs. Logs remain bounded per-instance
  snapshots, not an indexed historical logging backend.
- Native source mutation, workload breadth, stable routing, indexed search and AI
  diagnosis retain their original roadmap gates. Grouping stays read-only.
- One controller version dispatches during an upgrade. Legacy active releases are
  conservatively backfilled with claims; unclaimed releases fence admissions.

## Reproducible verification

`make test-local` must pass for the exact source tree before pushing. Its pinned
build checks sqlc/OpenAPI generation drift, Go static analysis and frontend
compilation; it runs race-enabled unit/system, native Docker, browser and MCP
checks. New tests cover independent release recovery, dependency/attention fences,
SSE snapshots/reconnect, scope and live revocation. Local reports include screenshots.
GitHub CI remains manual-only. Staging is a separate opt-in acceptance environment.
