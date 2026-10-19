// Package host judges whether a server can run OAP (docs/architecture/
// onboarding.md, step 2). It holds the read-only probe script, parses its
// output into facts and turns facts into typed findings. It runs nothing:
// an adapter runs the probe and hands back the output.
package host

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Probe is the read-only shell script whose output ParseProbe reads.
//
//go:embed probe.sh
var Probe string

// Facts is what the probe saw.
type Facts struct {
	UID                     int
	OSID, OSVersion, OSName string
	Arch, Kernel            string
	CPUs                    int
	MemoryMiB, SwapMiB      int
	DiskFreeMiB             int
	TimeSynced              string // "yes", "no", or "" when unknown
	SSHPasswordLogin        string // "yes", "no", or ""
	SSHRootLogin            string
	Listeners               []Listener
	DockerDaemon            string // path of the running dockerd, "" if none
	DockerPackage           string // package that owns it, "" if unmanaged
	DockerVersion           string
	DockerDropins           []string
	Containers              []Container
}

// Listener is a listening TCP socket.
type Listener struct {
	Addr    string
	Port    int
	Process string
}

// Container is a container on the host, with only whether OAP made it.
type Container struct {
	Name, State string
	OAP         bool
}

// ParseProbe reads the probe's key=value output. Output that doesn't end
// with the probe's completion marker is an error: the probe was cut short.
func ParseProbe(out string) (Facts, error) {
	var f Facts
	done := false
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		switch key {
		case "probe.done":
			done = val == "1"
		case "uid":
			f.UID = atoi(val, -1)
		case "os.id":
			f.OSID = val
		case "os.version":
			f.OSVersion = val
		case "os.name":
			f.OSName = val
		case "arch":
			f.Arch = val
		case "kernel":
			f.Kernel = val
		case "cpus":
			f.CPUs = atoi(val, 0)
		case "mem.total_kib":
			f.MemoryMiB = atoi(val, 0) / 1024
		case "swap.total_kib":
			f.SwapMiB = atoi(val, 0) / 1024
		case "disk.free_kib":
			f.DiskFreeMiB = atoi(val, 0) / 1024
		case "time.synced":
			f.TimeSynced = val
		case "ssh.passwordauthentication":
			f.SSHPasswordLogin = val
		case "ssh.permitrootlogin":
			f.SSHRootLogin = val
		case "listen":
			addr, proc, _ := strings.Cut(val, " ")
			l := Listener{Addr: addr, Process: proc}
			if i := strings.LastIndex(addr, ":"); i >= 0 {
				l.Port = atoi(addr[i+1:], 0)
			}
			f.Listeners = append(f.Listeners, l)
		case "docker.daemon":
			f.DockerDaemon = val
		case "docker.package":
			f.DockerPackage = val
		case "docker.version":
			f.DockerVersion = val
		case "docker.dropin":
			f.DockerDropins = append(f.DockerDropins, val)
		case "container":
			parts := strings.Split(val, "\t")
			if len(parts) == 3 {
				f.Containers = append(f.Containers, Container{Name: parts[0], State: parts[1], OAP: parts[2] == "oap"})
			}
		}
	}
	if !done {
		return Facts{}, fmt.Errorf("host: the probe didn't finish; its output is incomplete")
	}
	return f, nil
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// Severity says what a finding means for onboarding.
type Severity string

const (
	// Stop blocks onboarding until something outside OAP changes.
	Stop Severity = "stop"
	// NeedsFix blocks onboarding until the owner approves its fix.
	NeedsFix Severity = "fix"
	// Info is reported and changes nothing.
	Info Severity = "info"
	// Advice is a recommendation offered at hand-off.
	Advice Severity = "advice"
)

// Tier is a fix's risk tier.
type Tier string

const (
	Routine     Tier = "routine"
	Sensitive   Tier = "sensitive"
	Destructive Tier = "destructive"
)

// Fix is a change OAP can make to resolve a finding once the owner approves.
type Fix struct {
	ID      string `json:"id"`
	Tier    Tier   `json:"tier"`
	Summary string `json:"summary"`
	// Undoable is true when the fix records an undo in the host manifest.
	Undoable bool `json:"undoable"`
}

// Finding is one result of a check.
type Finding struct {
	Why      string   `json:"why"`
	Severity Severity `json:"severity"`
	Observed string   `json:"observed"`
	Required string   `json:"required,omitempty"`
	Remedy   string   `json:"remedy"`
	Fix      *Fix     `json:"fix,omitempty"`
}

// Supported lists the systems OAP installs on (owner decision, 2026-10-19).
var Supported = []string{"ubuntu 22.04", "ubuntu 24.04", "debian 12"}

// Requirements.
const (
	MinMemoryMiB     = 900  // a "1 GB" VPS reports about 985 MiB
	SwapBelowMiB     = 2048 // hosts with less memory than this need swap
	MinDiskFreeMiB   = 5120
	MinDockerMajor   = 24
	supportedSummary = "Ubuntu 22.04 or 24.04, or Debian 12"
)

// Check judges facts. Findings come in a fixed order: access, system,
// resources, Docker, ports, time, what else is on the host, SSH.
func Check(f Facts) []Finding {
	var out []Finding
	add := func(fd Finding) { out = append(out, fd) }

	if f.UID != 0 {
		add(Finding{Why: "host.access.not_root", Severity: Stop, Observed: fmt.Sprintf("connected as uid %d", f.UID),
			Required: "root", Remedy: "Connect as root. Connecting as a user with sudo isn't supported yet."})
	}
	if !supported(f.OSID, f.OSVersion) {
		name := f.OSName
		if name == "" {
			name = strings.TrimSpace(f.OSID + " " + f.OSVersion)
		}
		add(Finding{Why: "host.os.unsupported", Severity: Stop, Observed: name, Required: supportedSummary,
			Remedy: "Reinstall the server with Ubuntu 24.04 from your provider's console, then run oap again. OAP doesn't upgrade an operating system in place."})
	}
	switch f.Arch {
	case "x86_64", "amd64", "aarch64", "arm64":
	default:
		add(Finding{Why: "host.arch.unsupported", Severity: Stop, Observed: f.Arch, Required: "x86_64 or arm64",
			Remedy: "Use a server with an x86_64 or arm64 processor."})
	}
	if f.MemoryMiB < MinMemoryMiB {
		add(Finding{Why: "host.memory.low", Severity: Stop, Observed: fmt.Sprintf("%d MiB", f.MemoryMiB),
			Required: "1 GB or more", Remedy: "Resize the server to at least 1 GB of memory."})
	}
	if f.MemoryMiB < SwapBelowMiB && f.SwapMiB == 0 {
		add(Finding{Why: "host.swap.missing", Severity: NeedsFix, Observed: fmt.Sprintf("%d MiB memory, no swap", f.MemoryMiB),
			Required: "swap on servers under 2 GB", Remedy: "Add a 1 GiB swap file so a memory spike slows the server instead of stopping apps.",
			Fix: &Fix{ID: "swap-file", Tier: Routine, Summary: "Create /swapfile (1 GiB), enable it and add it to /etc/fstab", Undoable: true}})
	}
	if f.DiskFreeMiB < MinDiskFreeMiB {
		add(Finding{Why: "host.disk.low", Severity: Stop, Observed: fmt.Sprintf("%.1f GiB free", float64(f.DiskFreeMiB)/1024),
			Required: "5 GiB free", Remedy: "Free disk space or resize the disk, then run oap again."})
	}

	switch {
	case f.DockerDaemon == "" && f.DockerVersion == "":
		add(Finding{Why: "host.docker.missing", Severity: NeedsFix, Observed: "Docker isn't running", Required: fmt.Sprintf("Docker Engine %d or later", MinDockerMajor),
			Remedy: "Install Docker Engine from Docker's official package repository.",
			Fix:    &Fix{ID: "install-docker", Tier: Sensitive, Summary: "Install Docker Engine from Docker's apt repository and start it"}})
	case f.DockerPackage == "":
		obs := "Docker runs from " + orUnknown(f.DockerDaemon) + ", which no package manages"
		if len(f.DockerDropins) > 0 {
			obs += "; systemd drop-ins: " + strings.Join(f.DockerDropins, ", ")
		}
		add(Finding{Why: "host.docker.unmanaged", Severity: NeedsFix, Observed: obs, Required: "Docker Engine installed from a package, so it gets security updates",
			Remedy: "Replace it with Docker Engine from Docker's official package repository. Its containers restart.",
			Fix:    &Fix{ID: "install-docker", Tier: Sensitive, Summary: "Install Docker Engine from Docker's apt repository in place of the unmanaged one; running containers restart"}})
	case dockerMajor(f.DockerVersion) < MinDockerMajor:
		add(Finding{Why: "host.docker.old", Severity: NeedsFix, Observed: "Docker " + orUnknown(f.DockerVersion), Required: fmt.Sprintf("Docker Engine %d or later", MinDockerMajor),
			Remedy: "Upgrade Docker Engine. Its containers restart.",
			Fix:    &Fix{ID: "upgrade-docker", Tier: Sensitive, Summary: "Upgrade Docker Engine from Docker's apt repository; running containers restart"}})
	}

	oapRunning := false
	for _, c := range f.Containers {
		if c.OAP && c.State == "running" {
			oapRunning = true
		}
	}
	for _, port := range []int{80, 443} {
		for _, l := range f.Listeners {
			if l.Port != port || (l.Process == "docker-proxy" && oapRunning) {
				continue
			}
			add(Finding{Why: "host.port.busy", Severity: Stop, Observed: fmt.Sprintf("port %d is held by %s (%s)", port, orUnknown(l.Process), l.Addr),
				Required: "ports 80 and 443 free for OAP's Caddy",
				Remedy:   fmt.Sprintf("Decide what happens to %s: stop it, move it, or run it behind OAP. OAP doesn't stop software it didn't install.", orUnknown(l.Process))})
			break
		}
	}

	switch f.TimeSynced {
	case "no":
		add(Finding{Why: "host.time.unsynced", Severity: NeedsFix, Observed: "the clock isn't synchronized", Required: "a synchronized clock (certificates and tokens depend on it)",
			Remedy: "Turn on network time synchronization.",
			Fix:    &Fix{ID: "time-sync", Tier: Routine, Summary: "Enable systemd-timesyncd", Undoable: true}})
	case "":
		add(Finding{Why: "host.time.unknown", Severity: Info, Observed: "couldn't tell whether the clock is synchronized",
			Remedy: "Nothing to do now. Certificates and tokens fail if the clock drifts."})
	}

	var oap, other []string
	for _, c := range f.Containers {
		if c.OAP {
			oap = append(oap, c.Name)
		} else {
			other = append(other, fmt.Sprintf("container %s (%s)", c.Name, c.State))
		}
	}
	if len(oap) > 0 {
		sort.Strings(oap)
		add(Finding{Why: "host.oap.present", Severity: Info, Observed: fmt.Sprintf("%d OAP containers from an earlier install: %s", len(oap), strings.Join(oap, ", ")),
			Remedy: "Remove the earlier install with oap host uninstall, or keep it running alongside."})
	}
	for _, l := range f.Listeners {
		switch {
		case l.Port == 22 && l.Process == "sshd":
		case l.Process == "systemd-resolve" || l.Process == "systemd-resolved":
		case l.Port == 80 || l.Port == 443:
		case l.Process == "docker-proxy":
		default:
			other = append(other, fmt.Sprintf("%s on %s", orUnknown(l.Process), l.Addr))
		}
	}
	if len(other) > 0 {
		add(Finding{Why: "host.foreign.present", Severity: Info, Observed: strings.Join(other, "; "),
			Remedy: "Nothing to do. OAP leaves software it didn't install alone."})
	}

	if f.SSHPasswordLogin == "yes" {
		obs := "SSH accepts passwords"
		if f.SSHRootLogin == "yes" {
			obs += ", including for root"
		}
		add(Finding{Why: "host.ssh.password_login", Severity: Advice, Observed: obs,
			Remedy: "At hand-off, OAP offers to turn off password login once your own key works."})
	}
	return out
}

// Blocking reports whether any finding blocks onboarding, and whether any
// of those needs something outside OAP (a stop).
func Blocking(fs []Finding) (blocked, stopped bool) {
	for _, f := range fs {
		switch f.Severity {
		case Stop:
			blocked, stopped = true, true
		case NeedsFix:
			blocked = true
		}
	}
	return blocked, stopped
}

// Summary describes the host in one line.
func Summary(f Facts) string {
	name := f.OSName
	if name == "" {
		name = strings.TrimSpace(f.OSID + " " + f.OSVersion)
	}
	cpus := "1 CPU"
	if f.CPUs != 1 {
		cpus = fmt.Sprintf("%d CPUs", f.CPUs)
	}
	return fmt.Sprintf("%s, %s, %s, %d MiB memory, %.1f GiB disk free", orUnknown(name), orUnknown(f.Arch), cpus, f.MemoryMiB, float64(f.DiskFreeMiB)/1024)
}

func supported(id, version string) bool {
	for _, s := range Supported {
		if s == id+" "+version {
			return true
		}
	}
	return false
}

func dockerMajor(v string) int {
	major, _, _ := strings.Cut(v, ".")
	return atoi(major, 0)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
