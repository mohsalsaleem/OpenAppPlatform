package coolify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func applyStorageSafety(data map[string]any, component domain.Component) {
	if len(component.Volumes) > 0 {
		data["is_consistent_container_name_enabled"] = true
	}
}

func (c *Client) checkStorageAndLimits(ctx context.Context, id string, component domain.Component) error {
	if len(component.Volumes) == 0 && component.Limits == nil {
		return nil
	}
	var current struct {
		Memory   string `json:"limits_memory"`
		CPUs     string `json:"limits_cpus"`
		Settings struct {
			Consistent bool `json:"is_consistent_container_name_enabled"`
		} `json:"settings"`
	}
	if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(id), nil, &current); err != nil {
		return errors.New("native storage and limits cannot be verified")
	}
	if l := component.Limits; l != nil {
		cpu, err := strconv.ParseFloat(current.CPUs, 64)
		if err != nil || cpu != float64(l.CPUMillis)/1000 || current.Memory != strconv.Itoa(l.MemoryMiB)+"m" {
			return errors.New("native resource limits differ from the definition")
		}
	}
	if len(component.Volumes) == 0 {
		return nil
	}
	if !current.Settings.Consistent {
		return errors.New("persistent components require Coolify consistent container names to prevent overlapping writers")
	}
	var mounts storages
	if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(id)+"/storages", nil, &mounts); err != nil {
		return err
	}
	if len(mounts.Files) > 0 || len(mounts.Persistent) != len(component.Volumes) {
		return errors.New("native persistent mounts differ from the definition")
	}
	seen := map[string]bool{}
	for _, v := range mounts.Persistent {
		matched := false
		for _, m := range component.Volumes {
			if v.Name == id+"-oap-"+m.Name && v.MountPath == m.MountPath && (v.HostPath == nil || *v.HostPath == "") && !seen[m.Name] {
				seen[m.Name] = true
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("native persistent mounts differ from the definition")
		}
	}
	return nil
}

func applyLimits(data map[string]any, component domain.Component) {
	if component.Limits != nil {
		data["limits_memory"] = fmt.Sprintf("%dm", component.Limits.MemoryMiB)
		data["limits_cpus"] = fmt.Sprintf("%.3f", float64(component.Limits.CPUMillis)/1000)
	}
}

type storages struct {
	Persistent []struct {
		Name      string  `json:"name"`
		MountPath string  `json:"mount_path"`
		HostPath  *string `json:"host_path"`
	} `json:"persistent_storages"`
	Files []struct{} `json:"file_storages"`
}

// Coolify prefixes persistent names with its immutable application UUID.
// Exact matching makes a repeated preparation safe without patching, deleting,
// or taking over unrelated native mounts.
func (c *Client) syncStorage(ctx context.Context, id string, s operator.Spec) error {
	if len(s.Component.Volumes) == 0 {
		return nil
	}
	endpoint := "/applications/" + url.PathEscape(id) + "/storages"
	var current storages
	if err := c.request(ctx, "GET", endpoint, nil, &current); err != nil {
		return errors.New("Coolify storage API unavailable; named volumes require native storage support")
	}
	if len(current.Files) > 0 {
		return errors.New("native file mounts require operator review before adding OAP volumes")
	}
	present := map[string]bool{}
	for _, v := range current.Persistent {
		matched := false
		for _, wanted := range s.Component.Volumes {
			name := id + "-oap-" + wanted.Name
			if v.Name == name && v.MountPath == wanted.MountPath && (v.HostPath == nil || *v.HostPath == "") && !present[wanted.Name] {
				present[wanted.Name] = true
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("native volume configuration differs; operator review required")
		}
	}
	for _, wanted := range s.Component.Volumes {
		if present[wanted.Name] {
			continue
		}
		if err := c.request(ctx, "POST", endpoint, map[string]any{"type": "persistent", "name": "oap-" + wanted.Name, "mount_path": wanted.MountPath}, nil); err != nil {
			return err
		}
	}
	// Re-read after writes. Ambiguous or unexpected native rows hold preparation.
	if err := c.request(ctx, "GET", endpoint, nil, &current); err != nil {
		return err
	}
	if len(current.Persistent) != len(s.Component.Volumes) || len(current.Files) != 0 {
		return errors.New("native volume creation could not be uniquely verified")
	}
	seen := map[string]bool{}
	for _, v := range current.Persistent {
		matched := false
		for _, w := range s.Component.Volumes {
			if v.Name == id+"-oap-"+w.Name && v.MountPath == w.MountPath && (v.HostPath == nil || *v.HostPath == "") && !seen[w.Name] {
				seen[w.Name] = true
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("native volume creation differs from the requested mounts")
		}
	}
	return nil
}
