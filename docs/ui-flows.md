# Open App Platform UI flows

The visible product name is **Open App Platform**. Repository, Go module, API, and existing technical identifiers stay compatible.

## Reference and scope

Use DigitalOcean App Platform as the interaction reference: an application groups components, activity records deployments, settings edit configuration, and logs are scoped to components. Keep the existing compact slate-and-blue visual system. Implement only working capabilities from the roadmap; avoid placeholder tabs and promised features.

References reviewed October 10, 2026:
- [Manage deployments](https://docs.digitalocean.com/products/app-platform/how-to/manage-deployments/): app selection leads to Activity and deployment detail.
- [Manage services](https://docs.digitalocean.com/products/app-platform/how-to/manage-services/): components are configured inside an application.

This is a simplified information architecture based on those documented flows, rather than a visual clone.

## Current flows

1. **Applications:** scan name, component names/count, environment, and deployment target. Open an app or create one. No global deployment-policy card: strategy belongs to a release action or app settings when multiple supported choices exist.
2. **Create application:** choose name and target, define components, save. Creation saves the definition; deploying remains an explicit reviewed action.
3. **Overview:** inspect the latest release, components, and live instances. Open services, check health, restart an instance, and scale down where supported.
4. **Activity:** inspect durable deploy/restart/scale-down records and component progress; recover an uncertain operation explicitly.
5. **Settings:** edit component image, ports, instance count, variables, and connections. Saving updates the definition; Deploy applies it after review. Advanced manifest details remain collapsed.
6. **Logs:** choose a component instance and view bounded runtime logs.
7. **Deployment targets:** inspect connected operators and resources. Infrastructure navigation stays separate from application navigation.

Deployment strategy should appear only when it changes an available decision. Today standard deployment is the only supported choice; the deployment confirmation explains its interruption risk. Blue-green stays optional and deferred until implemented and verified.

## Roadmap boundaries

Guided discovery/manual assembly is available through Deployment targets → Group services → Select → Review → Create observed application. Existing bindings are reserved atomically; observe-only components show live health and logs with lifecycle actions disabled. Image-backed Coolify components expose a separate reviewed management handoff; source-backed and external Docker components remain observe-only. Git/source workflows, multiple environments per logical app, workers, monitoring, domains, and AI follow their existing roadmap gates. Do not add empty controls for these during UI polishing.

## Owner onboarding and access

First launch opens owner setup protected by the installation's setup secret.
Later visits use email/password sign-in. Additional users accept an owner-issued
invitation; public signup remains disabled. Owners see Workspace access for member
roles/disablement, application-scoped agent credentials and audit. Viewers retain
read access with lifecycle writes disabled. Sign-out is available on desktop and
mobile. Auth was explicitly moved ahead of GitHub release ownership by the owner;
this does not bring forward AI or full multi-tenant hosting.
