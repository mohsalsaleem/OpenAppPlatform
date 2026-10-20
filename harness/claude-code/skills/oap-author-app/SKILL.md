---
name: oap-author-app
description: Put an application from a repository (any language or framework) on a server managed by OAP (OpenAppPlatform) — work out its container image and oap.yaml, declare its secrets and ask the owner for their values, then propose, apply and verify. Use when asked to deploy, host or run an app with OAP.
---

# Put an application on OAP

OAP serves this guide itself, so it always matches the server you work
with. Load it from the server and follow it exactly; don't rely on
memory or on this file for the details.

1. Read the guide, by whichever way you reach OAP:
   - MCP: the prompt `author-app`, or the resource `oap://skills/author-app`.
   - CLI: `oapctl skill author-app` (with `OAP_AGENT=<your agent name>`).
   - HTTPS: `GET /.well-known/oap/skills/author-app.md` with your token.
2. Read the schema of `oap.yaml`: MCP resource `oap://schema/application`,
   or `oapctl skill schema`.
3. Read the server's briefing (`oap://briefing` or `oapctl briefing`) for
   what already runs, what you may do, and the owner's house rules.

If you aren't signed in to OAP yet, sign in first:
`oapctl login https://<oap api host> --agent <name>`, and tell the owner the
code it prints so they can approve you.

Never put secret values in anything you write or say; the guide explains
how the owner provides them.
