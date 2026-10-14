# Documentation

Open App Platform runs a multi-component application on one server and lets an
external AI agent operate it. Start with the [project README](../README.md), then
use this index.

![OAP architecture](images/architecture.svg)

## Start here

- [Positioning](positioning.md): the problem, the comparison with Compose, Kamal and k3s, and the reference application
- [Roadmap](../ROADMAP.md): milestones and execution order
- [Operating model](operating-model.md): principles, roles, what the agent gets and guardrails
- [Spec ownership](spec-ownership.md): where the application spec lives and how multi-repo releases work
- [Environments and verified change](environments.md): previews, declared checks, guarded rollout and optional environment comparison
- [Standalone agent runbook](standalone-agent-runbook.md): install, grant an agent and run the full lifecycle without the dashboard
- [Feature reference](feature-reference.md): detailed behavior and limits of current features

## Agent interface

- [Agent CLI](agent-cli.md): JSON commands, errors, exit codes and waiting
- [Headless bootstrap and recovery](headless-bootstrap.md): owner setup, agent grants, install, update and rescue
- [Portable application lifecycle](portable-lifecycle.md): the `oap.dev/v1alpha1` spec, validate, diff, plan and execute
- [Incident diagnosis](incident-diagnosis.md): bounded evidence for diagnose, correct and verify
- [MCP interface](mcp.md) and [M2 scoped agent operation](m2-agent-operation.md)
- [Operation skill](../skills/open-app-platform/SKILL.md)

## Applications on standalone Docker

- [Bare Docker targets](bare-docker.md): instances, scale-down, persistence and limits
- [Persistent volumes and resource limits](persistent-volumes.md)
- [Long-running workers](workers.md)
- [Runtime secret references](runtime-secret-references.md) and [recoverable secret store](encrypted-secret-store.md)
- [Private registry access](private-registry-access.md)
- [Existing database links](existing-database-links.md)
- [Native HTTP health checks](native-health-checks.md)

## Releases and recovery

- [Image rollback](image-rollback.md)
- [Release comparison](release-comparison.md), [release controls](release-controls.md) and [release readiness](release-readiness.md)
- [Recovery runbook](recovery-runbook.md) and [recovery acceptance](recovery-acceptance.md)
- [Target connection evidence and recovery](target-recovery.md)

## Installation, development and operations

- [Installation, updates and metadata recovery](installation.md)
- [Development, testing and deployment](development.md)
- [Reproducible local testing](local-testing.md)
- [Footprint measurement](footprint.md)
- [Owner access and workspace boundaries](owner-access.md)
- [Server build and deploy](server-build-deploy.md)

## Architecture

- [System design](architecture/system-design.md)
- [Low-level design](architecture/low-level-design.md)
- [Alignment audit](architecture/alignment-audit.md)
- [Product and architecture draft](product-and-architecture.md) (original design notes)
- [OpenAPI contract](openapi.json)

## Proposals

- [Docker Compose import](compose-import.md)
- [Lightweight indexed logging](logging-and-search.md)

## Coolify and existing resources (retained, expansion parked)

- [Discovery and application assembly](discovery-and-assembly.md)
- [Application environments](application-environments.md)
- [Target setup and definition review](target-setup-and-definition-review.md)
- [GitHub releases](github-releases.md)
- [Stable endpoints](stable-endpoints.md), [endpoint management](endpoint-management.md) and [endpoint verification](endpoint-verification.md)
- [Route membership](route-membership.md), [automatic route membership](automatic-route-membership.md) and [health-aware routing](health-aware-routing.md)
- [Operator-first audit](operator-first-audit.md)

## Acceptance records and history

- [v0.1 standalone release boundary](v0.1-standalone-acceptance.md) and [v0.1 test evidence](v0.1-test-evidence.md)
- [v0.1 acceptance](v0.1-acceptance.md) and [v0.1 release notes](releases/v0.1.md)
- [M0 exit review](m0-exit-review.md), [M1 security and visibility](m1-security-visibility.md)
- [Connected preview acceptance](connected-preview-acceptance.md)
- [Verification report](verification.md)
- [UI flows](ui-flows.md) (dashboard, parked)
- Archived roadmaps: [through 14 October](../ROADMAP-history-through-2026-10-14.md) and [through 13 October](../ROADMAP-history-through-2026-10-13.md)
- [Editable design page](https://chatgpt.com/space/page_48ca8aa82f6881918cae167d00860fe9)

## Diagrams

The diagrams in [`images/`](images) are hand-written SVG with built-in light and
dark themes. Edit them directly; keep text as real `<text>` elements so they stay
searchable and accessible.
