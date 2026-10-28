# Documentation

![OAP architecture](images/rewrite-c4.svg)

## Using OAP

- [Getting started](getting-started.md): a server's password to a running
  application
- [Agents guide](agents.md): device login, MCP, the skill and the briefing
- [Security model](security.md): actors, what protects what, known limits
- [Release notes](release-notes/v0.2.0.md) and the [roadmap](../ROADMAP.md)

## Design

- [Agent-native architecture](architecture/agent-native-rewrite.md): the
  world model, the seven primitives, policy, identity and state
- [Onboarding](architecture/onboarding.md): from a server password to a
  running OAP
- [ADR 0001](architecture/adr/0001-edge-membership-without-reload.md): edge
  membership without reloading Caddy

## How it was built

- [Rewrite plan](architecture/rewrite-plan.md): every task, its acceptance
  and its evidence
- [Release plan](architecture/release-plan.md): v0.2.0
- [Evidence](evidence/): receipts and logs from real servers
