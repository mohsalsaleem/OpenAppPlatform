#!/bin/sh
# Each line is name=result. The container keeps running so the test can
# inspect it.
try() { if sh -c "$2" >/dev/null 2>&1; then echo "$1=ok"; else echo "$1=denied"; fi; }
try root_write 'echo x > /etc/oap-probe'
try tmp_write 'echo x > /tmp/oap-probe'
try volume_write 'echo x > /data/oap-probe'
try chown 'touch /tmp/c && chown 1:1 /tmp/c'
try mknod 'mknod /tmp/null c 1 3'
echo "no_new_privs=$(awk '/^NoNewPrivs/ {print $2}' /proc/self/status)"
echo "cap_eff=$(awk '/^CapEff/ {print $2}' /proc/self/status)"
# Fork until the process limit stops it. busybox sh exits when a fork
# fails, so the bomb runs in a subshell that reports its count as it goes
# (echo is a builtin); the probe itself carries on.
(
  n=0
  while [ "$n" -lt 600 ]; do
    sleep 120 2>/dev/null &
    n=$((n + 1))
    echo "processes=$n"
  done
) 2>/dev/null
echo "probe=done"
exec sleep 600
