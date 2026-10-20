package host

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Hand-off and uninstall (R3.14, docs/architecture/onboarding.md).

// ImagesPath lists the images OAP brought to the host, one per line, so
// uninstall removes those and no others.
const ImagesPath = "/var/lib/oap/images"

var sshKey = regexp.MustCompile(`^ssh-[a-z0-9-]+ [A-Za-z0-9+/=]+( [^'"\n\r]*)?$`)

// RemoveKeyScript records the bootstrap key's removal in the host manifest
// and removes every authorized_keys line holding the key, with or without
// options such as a break-glass expiry. It prints how many lines it
// removed. The record comes first: once the key is gone, OAP can't write
// to the host.
func RemoveKeyScript(pub string, at time.Time) (string, error) {
	sc, err := RemoveKeyOnlyScript(pub)
	if err != nil {
		return "", err
	}
	return RecordScript("bootstrap-key", "undo", at) + sc, nil
}

// RemoveKeyOnlyScript removes the key without recording it, for the end
// of an uninstall, when OAP's directory is already gone.
func RemoveKeyOnlyScript(pub string) (string, error) {
	pub = strings.TrimSpace(pub)
	if !sshKey.MatchString(pub) {
		return "", fmt.Errorf("host: %q isn't an SSH public key", pub)
	}
	blob := strings.Fields(pub)[1]
	return fmt.Sprintf(`set -eu
f=~/.ssh/authorized_keys
[ -f "$f" ] || { echo 0; exit 0; }
n=$(grep -cF ' %[1]s' "$f" || true)
grep -vF ' %[1]s' "$f" > "$f.oap-tmp" || true
cat "$f.oap-tmp" > "$f"
rm -f "$f.oap-tmp"
echo "$n"
`, blob), nil
}

// LoadImageCommand loads an image sent on stdin and records it as one OAP
// brought.
func LoadImageCommand(image string) (string, error) {
	if !imageRef.MatchString(image) {
		return "", fmt.Errorf("host: %q isn't an image reference", image)
	}
	return fmt.Sprintf("docker load && mkdir -p /var/lib/oap && chmod 700 /var/lib/oap && "+
		"{ grep -qxF '%[1]s' %[2]s 2>/dev/null || printf '%%s\\n' '%[1]s' >> %[2]s; }", image, ImagesPath), nil
}

// ForgetScript removes OAP's directory once the manifest shows nothing
// left to reverse.
const ForgetScript = "set -eu\nrm -rf /var/lib/oap\n"

// UninstallStep is one step of an uninstall, in the order it runs.
type UninstallStep struct {
	Fix    string `json:"fix"`
	Title  string `json:"title"`
	Script string `json:"script,omitempty"`
	// Kept is why a step can't be undone and stays on the host.
	Kept string `json:"kept,omitempty"`
	// DataLoss marks a step that deletes applications' data.
	DataLoss bool `json:"data_loss,omitempty"`
}

// PlanUninstall reverses what the manifest shows applied: the newest
// first, OAP itself with its applications' data, then each fix that has an
// undo. Fixes without one are kept and reported; break-glass access ends
// with the key's removal, which the caller does last.
func PlanUninstall(installID, manifest string, at time.Time) ([]UninstallStep, error) {
	applied := Applied(manifest)
	var steps []UninstallStep
	for i := len(applied) - 1; i >= 0; i-- {
		f := applied[i]
		switch {
		case f == "install-oap":
			sc, err := Install{ID: installID}.Uninstall()
			if err != nil {
				return nil, err
			}
			steps = append(steps, UninstallStep{Fix: f, DataLoss: true,
				Title:  "Remove OAP: every application, its data volumes, oapd, Caddy, their network and the images OAP brought",
				Script: sc + RecordScript(f, "undo", at)})
		case f == "break-glass":
		case Scripts[f].Undo != "":
			steps = append(steps, UninstallStep{Fix: f, Title: "Undo the fix " + f, Script: Scripts[f].Undo + "\n" + RecordScript(f, "undo", at)})
		default:
			steps = append(steps, UninstallStep{Fix: f, Title: "Keep " + f,
				Kept: "it has no undo; removing it could break other software on the host"})
		}
	}
	return steps, nil
}
