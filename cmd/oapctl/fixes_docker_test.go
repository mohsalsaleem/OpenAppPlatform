package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// TestFixApplyThenUndoRestoresHost applies each undoable fix to a fixture
// host and undoes it, then compares a checksum of every file and the list
// of directories with the starting state. The host is a fresh container
// of each supported system; swapon, swapoff and timedatectl are stubbed
// because a container can't change the kernel's swap or systemd, and the
// stubs must see each apply matched by its undo. Gated on OAP_TEST_DOCKER=1.
func TestFixApplyThenUndoRestoresHost(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	// changesFiles lists fixes whose apply must change the filesystem;
	// time-sync only changes systemd's setting, seen in the stubbed calls.
	changesFiles := map[string]bool{"swap-file": true}
	stubs := map[string]string{
		"swap-file":      "swapon /swapfile\nswapoff /swapfile\n",
		"time-sync":      "timedatectl set-ntp true\ntimedatectl set-ntp false\n",
		"open-web-ports": "ufw allow 80/tcp\nufw allow 443/tcp\nufw allow 443/udp\nufw delete allow 80/tcp\nufw delete allow 443/tcp\nufw delete allow 443/udp\n",
	}
	for _, image := range []string{"ubuntu:26.04", "debian:13"} {
		for id, sc := range host.Scripts {
			if sc.Undo == "" {
				continue
			}
			t.Run(image+"/"+id, func(t *testing.T) {
				driver := fmt.Sprintf(`set -eu
mkdir -p /tmp/stubs
for c in swapon swapoff timedatectl ufw; do printf '#!/bin/sh\necho "%%s $*" >> /tmp/calls\n' "$c" > /tmp/stubs/$c; chmod +x /tmp/stubs/$c; done
: > /tmp/calls
export PATH=/tmp/stubs:$PATH
snap() {
  find / -xdev \( -path /proc -o -path /sys -o -path /tmp -o -path /dev -o -path /run -o -path /var/lib/oap \) -prune -o -type d -print | sort
  find / -xdev \( -path /proc -o -path /sys -o -path /tmp -o -path /dev -o -path /run -o -path /var/lib/oap \) -prune -o -type l -printf '%%p -> %%l\n' | sort
  find / -xdev \( -path /proc -o -path /sys -o -path /tmp -o -path /dev -o -path /run -o -path /var/lib/oap \) -prune -o -type f -print0 | sort -z | xargs -0 sha256sum
}
snap > /tmp/before
cat > /tmp/apply <<'OAP_APPLY'
%s
OAP_APPLY
cat > /tmp/undo <<'OAP_UNDO'
%s
OAP_UNDO
sh /tmp/apply
if [ %t = true ] && snap | cmp -s - /tmp/before; then echo "apply changed nothing" >&2; exit 1; fi
sh /tmp/undo
snap > /tmp/after
diff /tmp/before /tmp/after >&2 || { echo "undo left changes" >&2; exit 1; }
cat /tmp/calls
`, sc.Apply, sc.Undo, changesFiles[id])
				var out, errb bytes.Buffer
				cmd := exec.Command("docker", "run", "--rm", "-i", "--network", "none", "--tmpfs", "/tmp:exec", image, "sh", "-s")
				cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(driver), &out, &errb
				if err := cmd.Run(); err != nil {
					t.Fatalf("%v\n%s", err, errb.String())
				}
				if out.String() != stubs[id] {
					t.Fatalf("stubbed calls %q, want %q", out.String(), stubs[id])
				}
			})
		}
	}
}
