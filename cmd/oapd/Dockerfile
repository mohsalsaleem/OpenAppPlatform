# oapd's image, built by make oapd-image from a context holding only the
# static binary (.local/oapd-context).
FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache git ca-certificates tzdata
ARG TARGETARCH=amd64
COPY oapd-linux-${TARGETARCH} /usr/local/bin/oapd
VOLUME /var/lib/oapd
ENTRYPOINT ["/usr/local/bin/oapd"]
