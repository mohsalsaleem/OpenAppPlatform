# Roadmap

OAP is for one person running a few applications on their own server, with
AI agents doing most of the operating. The design is in
[the architecture](docs/architecture/agent-native-rewrite.md); this page is
the order of work after v0.2.0. Each item becomes tasks in a plan the owner
approves before work starts.

## v0.2.0 (released)

The agent-native rewrite: onboarding from a password, one API over the CLI,
MCP, Connect, gRPC and JSON, scoped agents and the policy envelope,
deploys without dropped requests, extensions. See the
[release notes](docs/release-notes/v0.2.0.md).

## v0.2.1

- **Remove an application, and scale a component to 0.** A failed create
  offers its own removal. Proposed in the
  [rewrite plan](docs/architecture/rewrite-plan.md#proposals-awaiting-the-owner).

## v0.3 candidates

- **Notifications by email**, and digests (R4.4).
- **Compose import**: an `oap.yaml` from a `compose.yaml` (R4.5).
- **The agent store and harness packages**: an agent resumes on a new
  machine without copying credentials (R5.4).
- **Onboarding logs and `oapctl logs`**, including oapd's log through the
  owner API.
- **`ingress.https`**: remove the field, or honour it.
- **A notice ask kind**: an agent tells the owner something without
  waiting for an answer.
- **Signed releases.**
- Wording fixes from the first real-server run.

The proposals behind these are in the
[rewrite plan](docs/architecture/rewrite-plan.md#proposals-awaiting-the-owner).
