ARG ALPINE_IMAGE
FROM ${ALPINE_IMAGE}
RUN adduser -D -u 10001 worker
USER worker
COPY worker /worker
HEALTHCHECK --interval=1s --timeout=2s --retries=3 CMD ["/worker", "check-health"]
ENTRYPOINT ["/worker"]
