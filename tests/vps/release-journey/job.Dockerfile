# Migration-shaped pre-deploy job for the v0.1 VPS release journey.
# Exits with JOB_EXIT_CODE (default 0) so a failing migration can be injected
# through the spec without building another image.
FROM alpine:3.23
RUN adduser -D -u 10001 job
USER job
ENTRYPOINT ["/bin/sh", "-c", "echo \"migration starting\"; sleep 2; code=\"${JOB_EXIT_CODE:-0}\"; echo \"migration exiting ${code}\"; exit \"${code}\""]
