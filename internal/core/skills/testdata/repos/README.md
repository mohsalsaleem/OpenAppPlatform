# Reference repositories for the author-app skill

R3.16's acceptance: a fresh agent, following only the skill, turns each of
these into an image reference and an `oap.yaml` that OAP accepts, with no
secret value in them.

- `notes-go-postgres`: a Go API with Postgres and a migrations command; no
  Dockerfile.
- `shop-node-worker`: a Node web server and an email worker in one image,
  with Redis.

Each has a committed `.env` whose secret-looking values contain `canary`.
None of them may appear in anything the agent writes. These are fixtures:
the code isn't built or run.
