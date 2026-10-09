# Hello API

A small Go HTTP service with a readiness endpoint and graceful SIGTERM shutdown.
It runs on bare Docker and can be published to any registry that Coolify can pull.
It is separate from the OpenAppPlatform controller.

```sh
docker build -t oap-hello-api:dev examples/hello-api
docker run --rm -p 127.0.0.1:8790:8080 oap-hello-api:dev
curl http://127.0.0.1:8790/
curl http://127.0.0.1:8790/health/ready
```

Publish the image through your registry workflow and use its digest in a web
component with port 8080. The optional VERSION environment variable identifies
the component when supplied through a Docker component's `env` configuration.
`UPSTREAM_URL` enables `/upstream`, which forwards a bounded request to another
component. See the [connected example](../docker-connected/application.json).
