FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
COPY --chown=10001:10001 site/ /srv/
COPY Caddyfile /etc/caddy/Caddyfile
ENV HOME=/tmp
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 CMD wget -q -O /dev/null http://127.0.0.1:8080/ || exit 1
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
