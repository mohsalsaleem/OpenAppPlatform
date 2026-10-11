// Package routing owns bounded native route configuration, independently of workloads.
package routing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type Binding struct {
	ExpectedAddresses []string `json:"expectedAddresses,omitempty"`
	ApplicationID     string   `json:"applicationId"`
	Component         string   `json:"component"`
	TargetID          string   `json:"targetId"`
	Name              string   `json:"name"`
	Domain            string   `json:"domain"`
	HealthPath        string   `json:"healthPath"`
	Directory         string   `json:"directory"`
}

var namePattern = regexp.MustCompile(`^oap-[a-z0-9-]{1,48}$`)
var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var healthPattern = regexp.MustCompile(`^/[A-Za-z0-9/_-]*$`)
var appPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func Parse(raw []byte) ([]Binding, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var bindings []Binding
	if err := d.Decode(&bindings); err != nil {
		return nil, errors.New("invalid native route bindings")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("provide one native route binding array")
	}
	if len(bindings) > 32 {
		return nil, errors.New("at most 32 explicit native routes")
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		if _, e := expectedAddresses(b.ExpectedAddresses); e != nil {
			return nil, e
		}
		if !appPattern.MatchString(b.ApplicationID) || !slugPattern.MatchString(b.Component) || !slugPattern.MatchString(b.TargetID) || !namePattern.MatchString(b.Name) || !validDomain(b.Domain) || !healthPattern.MatchString(b.HealthPath) || !filepath.IsAbs(b.Directory) || filepath.Clean(b.Directory) != b.Directory {
			return nil, errors.New("invalid native route authority")
		}
		for _, key := range []string{"file:" + filepath.Join(b.Directory, b.Name+".yaml"), "domain:" + b.Domain, "component:" + b.ApplicationID + ":" + b.Component} {
			if seen[key] {
				return nil, errors.New("duplicate native route authority")
			}
			seen[key] = true
		}
	}
	return bindings, nil
}
func validDomain(value string) bool {
	return len(value) <= 253 && domainPattern.MatchString(value) && strings.Contains(value, ".") && !strings.Contains(value, "..")
}
func Origin(value, domain string) bool {
	u, e := url.Parse(value)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == "" && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && !strings.ContainsAny(value, " \t\r\n") && !strings.EqualFold(u.Hostname(), domain)
}
func Render(b Binding, upstreams []string) ([]byte, error) {
	if !namePattern.MatchString(b.Name) || !validDomain(b.Domain) || !healthPattern.MatchString(b.HealthPath) || len(upstreams) < 1 || len(upstreams) > 4 {
		return nil, errors.New("invalid native route")
	}
	servers := []map[string]string{}
	seen := map[string]bool{}
	for _, endpoint := range upstreams {
		if !Origin(endpoint, b.Domain) || seen[endpoint] {
			return nil, errors.New("invalid or duplicate backend origin")
		}
		seen[endpoint] = true
		servers = append(servers, map[string]string{"url": strings.TrimRight(endpoint, "/")})
	}
	rule := fmt.Sprintf("Host(`%s`)", b.Domain)
	config := map[string]any{"http": map[string]any{
		"middlewares": map[string]any{b.Name + "-redirect": map[string]any{"redirectScheme": map[string]string{"scheme": "https"}}},
		"routers":     map[string]any{b.Name + "-http": map[string]any{"rule": rule, "entryPoints": []string{"http"}, "middlewares": []string{b.Name + "-redirect"}, "service": b.Name}, b.Name + "-https": map[string]any{"rule": rule, "entryPoints": []string{"https"}, "service": b.Name, "tls": map[string]string{"certResolver": "letsencrypt"}}},
		"services":    map[string]any{b.Name: map[string]any{"loadBalancer": map[string]any{"passHostHeader": false, "healthCheck": map[string]string{"path": b.HealthPath, "interval": "2s", "timeout": "1s"}, "servers": servers}}},
	}}
	return json.MarshalIndent(config, "", "  ")
}
func Backends(b Binding, data []byte, allowed map[string]bool) ([]string, error) {
	var config struct {
		HTTP struct {
			Services map[string]struct {
				LoadBalancer struct {
					Servers []struct {
						URL string `json:"url"`
					} `json:"servers"`
				} `json:"loadBalancer"`
			} `json:"services"`
		} `json:"http"`
	}
	if json.Unmarshal(data, &config) != nil {
		return nil, errors.New("native route is not an OAP renderer document")
	}
	urls := []string{}
	for _, server := range config.HTTP.Services[b.Name].LoadBalancer.Servers {
		if !allowed[server.URL] {
			return nil, errors.New("native route contains a foreign backend")
		}
		urls = append(urls, server.URL)
	}
	expected, e := Render(b, urls)
	if e != nil {
		return nil, e
	}
	var actual, wanted any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(expected, &wanted) != nil {
		return nil, errors.New("invalid native route")
	}
	left, _ := json.Marshal(actual)
	right, _ := json.Marshal(wanted)
	if !bytes.Equal(left, right) {
		return nil, errors.New("custom native configuration is preserved; route publication is blocked")
	}
	return urls, nil
}
