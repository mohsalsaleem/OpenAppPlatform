package runtimeinputs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

// RegistryBinding grants pull credentials to one component and registry host.
// It never grants login authority over a server's shared Docker configuration.
type RegistryBinding struct {
	Reference     string `json:"reference"`
	TargetID      string `json:"targetId"`
	ApplicationID string `json:"applicationId"`
	Component     string `json:"component"`
	Registry      string `json:"registry"`
	UsernameEnv   string `json:"usernameEnv"`
	PasswordEnv   string `json:"passwordEnv"`
}
type registryEntry struct {
	binding RegistryBinding
	auth    domain.RegistryAuth
}

func registryHost(image string) string {
	first, _, slash := strings.Cut(image, "/")
	if slash && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "docker.io"
}
func validRegistry(host string) bool {
	u, err := url.Parse("https://" + host)
	return err == nil && u.Host == host && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && strings.ToLower(host) == host && !strings.ContainsAny(host, " \r\n\t")
}
func AddRegistries(inputs *Inputs, raw []byte, lookup func(string) string) (*Inputs, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("registry bindings exceed limit")
	}
	var bindings []RegistryBinding
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&bindings) != nil || d.Decode(new(any)) != io.EOF || len(bindings) > 128 {
		return nil, errors.New("invalid registry bindings")
	}
	if inputs == nil {
		inputs = &Inputs{}
	}
	out := &Inputs{entries: append([]entry(nil), inputs.entries...), registries: append([]registryEntry(nil), inputs.registries...)}
	for _, b := range bindings {
		if !slug.MatchString(b.Reference) || !slug.MatchString(b.TargetID) || !slug.MatchString(b.Component) || len(b.ApplicationID) != 32 || strings.Trim(b.ApplicationID, "0123456789abcdef") != "" || !validRegistry(b.Registry) || !identifier.MatchString(b.UsernameEnv) || !identifier.MatchString(b.PasswordEnv) || !strings.HasPrefix(b.UsernameEnv, "OAP_SECRET_") || !strings.HasPrefix(b.PasswordEnv, "OAP_SECRET_") {
			return nil, errors.New("registry bindings require exact scope, registry host and dedicated OAP_SECRET_ variables")
		}
		for _, existing := range out.registries {
			a := existing.binding
			if a.Reference == b.Reference && a.TargetID == b.TargetID && a.ApplicationID == b.ApplicationID && a.Component == b.Component {
				return nil, errors.New("duplicate registry binding")
			}
		}
		username, password := lookup(b.UsernameEnv), lookup(b.PasswordEnv)
		if len(username) > 256 || len(password) > 4096 || (password != "" && len(password) < 8) || strings.ContainsAny(username, "\x00\r\n") || strings.ContainsAny(password, "\x00\r\n") {
			return nil, errors.New("invalid registry credential value")
		}
		out.registries = append(out.registries, registryEntry{b, domain.RegistryAuth{Username: username, Password: password, ServerAddress: b.Registry}})
	}
	return out, nil
}
func (i *Inputs) resolveRegistry(target, app string, c domain.Component) (domain.Component, error) {
	c.RegistryAuth = nil
	if c.RegistryCredentialRef == "" {
		return c, nil
	}
	if i != nil {
		for _, e := range i.registries {
			b := e.binding
			if b.Reference == c.RegistryCredentialRef && b.TargetID == target && b.ApplicationID == app && b.Component == c.Name && b.Registry == registryHost(c.Image) && e.auth.Username != "" && len(e.auth.Password) >= 8 {
				auth := e.auth
				c.RegistryAuth = &auth
				return c, nil
			}
		}
	}
	return domain.Component{}, errors.New("registry credential reference is missing or not authorized for this component and image registry")
}
func registryRedactions(auth domain.RegistryAuth) []string {
	if auth.Password == "" {
		return nil
	}
	raw, _ := json.Marshal(auth)
	return []string{auth.Password, string(raw), base64.URLEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))}
}
