ARG ALPINE_IMAGE
FROM ${ALPINE_IMAGE}
ARG BINARY
ARG PORT
RUN adduser -D -u 10001 fixture
USER fixture
COPY ${BINARY} /fixture
ENV FIXTURE_PORT=${PORT}
HEALTHCHECK --interval=1s --timeout=2s --retries=10 CMD wget -q -O /dev/null http://127.0.0.1:${FIXTURE_PORT}/health/ready || exit 1
ENTRYPOINT ["/fixture"]
