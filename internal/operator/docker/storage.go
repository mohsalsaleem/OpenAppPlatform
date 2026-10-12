package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type volume struct {
	Name    string            `json:"Name"`
	Driver  string            `json:"Driver"`
	Labels  map[string]string `json:"Labels"`
	Options map[string]string `json:"Options"`
}

func (c *Client) volumeName(s operator.Spec, name string) string {
	hash := sha256.Sum256([]byte(c.target.ID + "\x00" + s.Ownership + "\x00" + name))
	return "oap-" + hex.EncodeToString(hash[:])
}

func (c *Client) volumeMatches(v volume, s operator.Spec, name string) bool {
	return v.Name == c.volumeName(s, name) && v.Driver == "local" && len(v.Options) == 0 && v.Labels[prefix+"target"] == c.target.ID && v.Labels[prefix+"owner"] == s.Ownership && v.Labels[prefix+"volume"] == name
}

// Every lookup is ownership checked; a retry after an uncertain create reuses
// the same labeled volume. There is deliberately no delete operation.
func (c *Client) mounts(ctx context.Context, s operator.Spec) ([]map[string]any, error) {
	missing := []string{}
	for _, m := range s.Component.Volumes {
		var v volume
		err := c.request(ctx, "GET", "/volumes/"+url.PathEscape(c.volumeName(s, m.Name)), nil, &v)
		if isStatus(err, 404) {
			for _, ref := range []string{s.Name, s.Name + "-next", s.Name + "-previous"} {
				existing, inspectErr := c.inspectContainer(ctx, ref)
				if inspectErr != nil && !isStatus(inspectErr, 404) {
					return nil, inspectErr
				}
				if inspectErr == nil && c.owned(existing) && existing.Config.Labels[prefix+"owner"] == s.Ownership && len(existing.Mounts) > 0 {
					return nil, errors.New("referenced persistent volume is missing; restore through the operator")
				}
			}
			missing = append(missing, m.Name)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !c.volumeMatches(v, s, m.Name) {
			return nil, errors.New("named volume ownership or local driver differs; operator review required")
		}
	}
	for _, name := range missing {
		var v volume
		if err := c.request(ctx, "POST", "/volumes/create", map[string]any{"Name": c.volumeName(s, name), "Driver": "local", "Labels": map[string]string{prefix + "target": c.target.ID, prefix + "owner": s.Ownership, prefix + "volume": name}}, &v); err != nil {
			return nil, err
		}
		if !c.volumeMatches(v, s, name) {
			return nil, errors.New("created volume ownership cannot be verified")
		}
	}
	out := []map[string]any{}
	for _, m := range s.Component.Volumes {
		out = append(out, map[string]any{"Type": "volume", "Source": c.volumeName(s, m.Name), "Target": m.MountPath, "ReadOnly": false})
	}
	return out, nil
}

func (c *Client) checkLimits(v container, s operator.Spec) error {
	if l := s.Component.Limits; l != nil && (v.HostConfig.Memory != int64(l.MemoryMiB)<<20 || v.HostConfig.NanoCPUs != int64(l.CPUMillis)*1000000) {
		return errors.New("native resource limits differ from the prepared definition")
	}
	return nil
}
func (c *Client) checkMounts(v container, s operator.Spec) error {
	if len(v.Mounts) != len(s.Component.Volumes) {
		return errors.New("native volume mounts differ from the definition")
	}
	seen := map[string]bool{}
	for _, raw := range v.Mounts {
		var m struct {
			Type, Name, Destination string
			RW                      bool
		}
		if json.Unmarshal(raw, &m) != nil {
			return errors.New("cannot verify native mounts")
		}
		matched := false
		for _, w := range s.Component.Volumes {
			if m.Type == "volume" && m.Name == c.volumeName(s, w.Name) && m.Destination == w.MountPath && m.RW && !seen[w.Name] {
				seen[w.Name] = true
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("native volume mounts differ from the definition")
		}
	}
	return nil
}
