ARG ALPINE_IMAGE
FROM ${ALPINE_IMAGE}
RUN adduser -D -u 10001 application
USER application
COPY application /application
HEALTHCHECK --interval=1s --timeout=3s --retries=3 --start-period=3s CMD ["/application","check-health"]
ENTRYPOINT ["/application"]
