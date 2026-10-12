# Private Image Example

For standalone Docker, replace the image in `application.json` with your private
image (prefer a digest), set its internal port and select your Docker target.
Configure that target with `pullPolicy: if-missing`. Create the application, then
grant `registry-read-v1` to its assigned application ID, `web` component, target
and registry host using [private registry access](../../docs/private-registry-access.md).
The image must contain its own entrypoint; use an image health check if available.
Review and deploy after restarting OAP with the private owner-configured binding.

For Coolify, omit `registryCredentialRef` and configure native server registry
login in Coolify. Existing linked/adopted resources keep their native settings.

The reproducible local suite uses a generated private image and in-memory registry
fixture instead of requiring this external image or a real registry account.
