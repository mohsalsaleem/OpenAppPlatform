#!/bin/sh
# OAP host probe (docs/architecture/onboarding.md, step 2). Read-only: it
# writes nothing and changes nothing. Output is one key=value per line;
# repeated keys are lists.
kv() { printf '%s=%s\n' "$1" "$2"; }
has() { command -v "$1" >/dev/null 2>&1; }

kv probe.version 1
kv uid "$(id -u)"
if [ -r /etc/os-release ]; then
  . /etc/os-release
  kv os.id "${ID:-}"
  kv os.version "${VERSION_ID:-}"
  kv os.name "${PRETTY_NAME:-}"
fi
kv arch "$(uname -m)"
kv kernel "$(uname -r)"
kv cpus "$(grep -c '^processor' /proc/cpuinfo 2>/dev/null)"
kv mem.total_kib "$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null)"
kv swap.total_kib "$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo 2>/dev/null)"
kv disk.free_kib "$(df -Pk / 2>/dev/null | awk 'NR == 2 {print $4}')"

if has timedatectl; then
  synced="$(timedatectl show -p NTPSynchronized --value 2>/dev/null)"
  # systemd before 239 has no "show"; read the status text instead.
  [ -n "$synced" ] || synced="$(timedatectl status 2>/dev/null | awk -F ': ' '/synchronized:/ {print $2; exit}')"
  kv time.synced "$synced"
fi

if has sshd; then
  sshd -T 2>/dev/null | awk '$1 == "passwordauthentication" || $1 == "permitrootlogin" {print "ssh." $1 "=" $2}'
fi

if has ss; then
  ss -Htlnp 2>/dev/null | awk '{
    proc = ""; if (match($0, /users:\(\("[^"]*"/)) proc = substr($0, RSTART + 9, RLENGTH - 10)
    print "listen=" $4 " " proc
  }'
fi

dockerd_pid="$(pidof dockerd 2>/dev/null | awk '{print $1}')"
if [ -n "$dockerd_pid" ]; then
  exe="$(readlink -f /proc/"$dockerd_pid"/exe 2>/dev/null)"
  kv docker.daemon "$exe"
  if has dpkg-query; then
    kv docker.package "$(dpkg -S "$exe" 2>/dev/null | cut -d: -f1)"
  elif has rpm; then
    kv docker.package "$(rpm -qf "$exe" 2>/dev/null)"
  fi
fi
if [ -d /etc/systemd/system/docker.service.d ]; then
  for f in /etc/systemd/system/docker.service.d/*; do [ -e "$f" ] && kv docker.dropin "${f##*/}"; done
fi
if has docker; then
  kv docker.version "$(docker version --format '{{.Server.Version}}' 2>/dev/null)"
  # Names and states only; labels are reduced to whether OAP made it.
  docker ps -a --format '{{.Names}}	{{.State}}	{{.Labels}}' 2>/dev/null |
    awk -F '\t' '{ print "container=" $1 "\t" $2 "\t" (index($3, "io.openappplatform.") ? "oap" : "other") }'
fi
kv probe.done 1
