# Bakes a VERSION into the hello-api image so each release has a distinct,
# immutable image while the spec's runtime configuration stays unchanged.
ARG BASE
FROM ${BASE}
ARG VERSION
ENV VERSION=${VERSION}
