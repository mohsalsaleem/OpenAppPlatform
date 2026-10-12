package coolify

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

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
