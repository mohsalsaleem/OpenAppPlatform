# oapd's image, built by make oapd-image from a context holding only the
# static binary (.local/oapd-context). Packages for the target architecture
# are installed by apk running natively on the build machine, so building
# for another architecture never emulates a CPU.
ARG ALPINE=alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8

FROM --platform=$BUILDPLATFORM ${ALPINE} AS packages
ARG TARGETARCH
RUN case "$TARGETARCH" in amd64) arch=x86_64 ;; arm64) arch=aarch64 ;; *) echo "unsupported $TARGETARCH" >&2; exit 1 ;; esac && \
    apk add --no-cache --root /rootfs --arch "$arch" --initdb --no-scripts \
      --keys-dir "/usr/share/apk/keys/$arch" --repositories-file /etc/apk/repositories \
      git ca-certificates tzdata

FROM ${ALPINE}
COPY --from=packages /rootfs/ /
ARG TARGETARCH
COPY oapd-linux-${TARGETARCH} /usr/local/bin/oapd
VOLUME /var/lib/oapd
ENTRYPOINT ["/usr/local/bin/oapd"]
