# Injected failing release: same API, but its image health check always fails,
# so readiness never passes and the release must not complete.
ARG BASE
FROM ${BASE}
ENV VERSION=broken
HEALTHCHECK --interval=3s --timeout=2s --retries=2 CMD exit 1
