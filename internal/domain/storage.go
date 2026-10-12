package domain

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

// VolumeMount names component-local retained data, never an existing host path.
type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

// ResourceLimits uses whole MiB and millicores (1000 = one CPU).
type ResourceLimits struct {
	MemoryMiB int `json:"memoryMiB"`
	CPUMillis int `json:"cpuMillis"`
}

var mountPath = regexp.MustCompile(`^/[a-zA-Z0-9._/-]+$`)

func (c Component) ValidateStorageAndLimits() error {
	if (len(c.Volumes) > 0 || c.Limits != nil) && (c.ResourceID != "" || c.Management == "observe" || c.Kind == "database") {
		return errors.New("storage and limit overrides require OAP-owned image components")
	}
	if c.Limits != nil && (c.Limits.MemoryMiB < 64 || c.Limits.MemoryMiB > 65536 || c.Limits.CPUMillis < 50 || c.Limits.CPUMillis > 64000) {
		return errors.New("limits require memoryMiB between 64 and 65536 and cpuMillis between 50 and 64000")
	}
	if len(c.Volumes) > 4 {
		return errors.New("at most four named volume mounts per component")
	}
	if len(c.Volumes) > 0 && c.Instances > 1 {
		return errors.New("local persistent volumes require one instance")
	}
	names := map[string]bool{}
	for i, v := range c.Volumes {
		if !slug.MatchString(v.Name) || names[v.Name] {
			return errors.New("volume names must be unique lowercase slugs")
		}
		names[v.Name] = true
		if len(v.MountPath) > 256 || !mountPath.MatchString(v.MountPath) || path.Clean(v.MountPath) != v.MountPath || v.MountPath == "/" {
			return errors.New("volume mountPath must be a clean absolute container directory")
		}
		for _, blocked := range []string{"/proc", "/sys", "/dev", "/etc", "/run", "/var/run"} {
			if v.MountPath == blocked || strings.HasPrefix(v.MountPath, blocked+"/") {
				return errors.New("volume mountPath cannot cover system directories")
			}
		}
		for _, other := range c.Volumes[:i] {
			if v.MountPath == other.MountPath || strings.HasPrefix(v.MountPath, other.MountPath+"/") || strings.HasPrefix(other.MountPath, v.MountPath+"/") {
				return errors.New("volume mount paths cannot overlap")
			}
		}
	}
	return nil
}
