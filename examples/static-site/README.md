# Static website on standalone OAP

Copy this Dockerfile and Caddyfile into your website repository. Put the static
files in `site/`, build the image and get its immutable content ID:

```sh
docker build -t my-site:local .
docker image inspect --format '{{.Id}}' my-site:local
```

Update `application.json` with that ID and your authorized ingress domain. The
component has no host port; OAP's packaged ingress proxy owns public TLS and
routes to the ready web instance over the application network. The image's Caddy
is only a non-root HTTP file server; its administration and automatic TLS are off.

Follow [public installation acceptance](../../docs/standalone-public-acceptance.md).
The same public-CLI acceptance script is used by the isolated local test. It does
not introduce a private setup API or bypass planning. This example does not
execute PHP, supply contact forms, or create a database.
