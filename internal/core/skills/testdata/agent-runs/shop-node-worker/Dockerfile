# Suggested replacement for the repository's Dockerfile (not required for this
# proposal, which uses the published ghcr.io/example/shop:2.1.0). Adds a
# HEALTHCHECK so the worker's `health: {mode: image}` has something to run,
# and a multi-stage build with a pinned base image.
FROM node:22.11-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

FROM node:22.11-alpine
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY package.json ./
COPY src ./src
COPY docker-entrypoint.sh /usr/local/bin/
USER node
EXPOSE 3000
# Web: GET /health. Worker (ROLE=worker): the node worker process is alive.
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD \
  if [ "$ROLE" = "worker" ]; then pgrep -f src/worker.js >/dev/null; \
  else wget -q -O /dev/null "http://127.0.0.1:${PORT:-3000}/health"; fi
# ROLE=worker runs the email worker; anything else runs the web server.
ENTRYPOINT ["docker-entrypoint.sh"]
