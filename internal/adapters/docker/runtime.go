// Package docker implements the Runtime port on the Docker Engine API, over
// its unix socket. Every container, volume and network it creates carries
// OAP labels, and it only ever observes, adopts or removes resources whose
// labels name its installation: anything else on the host is left alone,
// even when it has a name OAP would use.
package docker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// Labels OAP puts on every resource it creates.
const (
	LabelPrefix      = "io.openappplatform."
	LabelInstall     = LabelPrefix + "install"
	LabelApplication = LabelPrefix + "application"
	LabelComponent   = LabelPrefix + "component"
	LabelOrdinal     = LabelPrefix + "ordinal"
	LabelRelease     = LabelPrefix + "release"
	LabelRole        = LabelPrefix + "role"
	LabelImage       = LabelPrefix + "image"
	LabelVolume      = LabelPrefix + "volume"
	LabelSnapshotKey = LabelPrefix + "snapshot-key"
)

// Resource roles.
const (
	RoleInstance = "instance"
	RoleJob      = "job"
	RoleVolume   = "volume"
	RoleSnapshot = "snapshot"
	RoleNetwork  = "network"
	RoleHelper   = "helper"
)

// Runtime runs OAP instances and jobs as Docker containers.
type Runtime struct {
	// Install names the OAP installation. Only resources labelled with it
	// are observed, adopted or removed.
	Install string
	// Secrets resolves instance and job secret references; nil means none
	// may be used.
	Secrets ports.Secrets
	// HelperImage copies volumes for snapshots; it needs sh, test, cp and touch.
	HelperImage string
	// StopTimeout is how long an instance gets to exit after SIGTERM.
	StopTimeout time.Duration
	// MemInfo is the host's meminfo file, read for available memory, which
	// the Engine API doesn't report. oapd runs on the Docker host (it reaches
	// the engine on a unix socket), and a container's /proc/meminfo shows the
	// host's memory. Empty, or unreadable, leaves available memory unknown.
	MemInfo string

	c *client
}

// New returns a runtime for an installation, talking to the Docker Engine on
// socket (usually /var/run/docker.sock).
func New(socket, install string) (*Runtime, error) {
	if !validName.MatchString(install) {
		return nil, fmt.Errorf("docker: installation id %q must match %s", install, validName)
	}
	return &Runtime{Install: install, HelperImage: "busybox:1.37", StopTimeout: 10 * time.Second, MemInfo: "/proc/meminfo",
		c: newClient(socket)}, nil
}

var (
	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	unsafe    = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
)

func clean(s string) string { return unsafe.ReplaceAllString(s, "-") }

// ContainerName is the container name of an instance. On the installation's
// network it is also the instance's host name, which the edge dials.
func ContainerName(install string, r ops.InstanceRef) string {
	return clean(fmt.Sprintf("oap-%s-%s-%s-%d-%s", install, r.Application, r.Component, r.Ordinal, r.Release))
}

// NetworkName is the network every instance of an installation joins.
func NetworkName(install string) string { return "oap-" + install }

func jobName(install string, r ops.InstanceRef) string {
	return clean(fmt.Sprintf("oap-%s-job-%s-%s-%s", install, r.Application, r.Component, r.Release))
}

func volumeName(install, app, component, volume string) string {
	return clean(fmt.Sprintf("oap-%s-%s-%s-%s", install, app, component, volume))
}

func (r *Runtime) labels(role string) map[string]string {
	return map[string]string{LabelInstall: r.Install, LabelRole: role}
}

func refLabels(m map[string]string, ref ops.InstanceRef) map[string]string {
	m[LabelApplication], m[LabelComponent] = ref.Application, ref.Component
	m[LabelOrdinal], m[LabelRelease] = strconv.Itoa(ref.Ordinal), ref.Release
	return m
}

// owned reports whether labels mark a resource of this installation with
// the given role.
func (r *Runtime) owned(labels map[string]string, role string) bool {
	return labels[LabelInstall] == r.Install && labels[LabelRole] == role
}

// refOf returns the instance a container belongs to, if every label is there.
func refOf(labels map[string]string) (ops.InstanceRef, bool) {
	n, err := strconv.Atoi(labels[LabelOrdinal])
	ref := ops.InstanceRef{Application: labels[LabelApplication], Component: labels[LabelComponent], Ordinal: n, Release: labels[LabelRelease]}
	return ref, err == nil && n > 0 && ref.Application != "" && ref.Component != "" && ref.Release != ""
}

type container struct {
	ID      string `json:"Id"`
	Names   []string
	Labels  map[string]string
	State   string
	Status  string
	Created int64
}

// containers lists this installation's containers with a role, in any state,
// whose labels name a complete instance ref.
func (r *Runtime) containers(ctx context.Context, role string, ref *ops.InstanceRef) ([]container, error) {
	filter := []string{LabelInstall + "=" + r.Install, LabelRole + "=" + role}
	if ref != nil {
		for k, v := range refLabels(map[string]string{}, *ref) {
			filter = append(filter, k+"="+v)
		}
	}
	q := labelFilter(filter...)
	q.Set("all", "1")
	var list []container
	if err := r.c.do(ctx, http.MethodGet, "/containers/json", q, nil, &list); err != nil {
		return nil, err
	}
	out := list[:0]
	for _, c := range list {
		if got, ok := refOf(c.Labels); ok && r.owned(c.Labels, role) && (ref == nil || got == *ref) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Runtime) remove(ctx context.Context, id string) error {
	err := r.c.do(ctx, http.MethodDelete, "/containers/"+id, url.Values{"force": {"1"}, "v": {"0"}}, nil, nil)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// Start runs an instance, or returns the one already running for its ref.
func (r *Runtime) Start(ctx context.Context, s ops.InstanceSpec) (string, error) {
	existing, err := r.containers(ctx, RoleInstance, &s.Ref)
	if err != nil {
		return "", err
	}
	for _, c := range existing {
		if c.State == "running" {
			return c.ID, nil
		}
	}
	for _, c := range existing {
		if err := r.remove(ctx, c.ID); err != nil {
			return "", err
		}
	}
	env, err := r.env(ctx, s.Env, s.SecretRefs)
	if err != nil {
		return "", err
	}
	labels := refLabels(r.labels(RoleInstance), s.Ref)
	labels[LabelImage] = s.Image
	host := hostConfig{Memory: int64(s.MemoryMiB) << 20, NanoCPUs: int64(s.CPUMillis) * 1e6,
		RestartPolicy: restartPolicy{Name: "unless-stopped"}}
	for _, v := range sortedKeys(s.Volumes) {
		name := volumeName(r.Install, s.Ref.Application, s.Ref.Component, v)
		vl := r.labels(RoleVolume)
		vl[LabelApplication], vl[LabelComponent], vl[LabelVolume] = s.Ref.Application, s.Ref.Component, v
		if err := r.ensureVolume(ctx, name, vl); err != nil {
			return "", err
		}
		host.Mounts = append(host.Mounts, mount{Type: "volume", Source: name, Target: s.Volumes[v]})
	}
	cfg := containerConfig{Image: s.Image, Env: env, Labels: labels, HostConfig: host,
		// Other components reach this one by its service name (ops.ServiceURL);
		// Docker's DNS answers it with every running instance.
		NetworkingConfig: &networkingConfig{EndpointsConfig: map[string]endpointConfig{
			NetworkName(r.Install): {Aliases: []string{ops.ServiceHost(s.Ref.Application, s.Ref.Component)}}}}}
	if s.Port > 0 {
		port := fmt.Sprintf("%d/tcp", s.Port)
		cfg.ExposedPorts = map[string]struct{}{port: {}}
		if s.HostPort > 0 {
			cfg.HostConfig.PortBindings = map[string][]portBinding{port: {{HostPort: strconv.Itoa(s.HostPort)}}}
		}
	}
	if h := s.Health; h != nil && h.HTTPPath != "" && s.Port > 0 {
		cfg.Healthcheck = &healthcheck{
			Test:        []string{"CMD-SHELL", fmt.Sprintf("wget -q -O /dev/null http://127.0.0.1:%d%s || exit 1", s.Port, h.HTTPPath)},
			Interval:    seconds(h.IntervalSeconds, 5),
			Timeout:     seconds(h.TimeoutSeconds, 3),
			Retries:     max(h.Retries, 1),
			StartPeriod: int64(h.StartPeriod),
		}
	}
	return r.run(ctx, ContainerName(r.Install, s.Ref), cfg, true)
}

func seconds(n, fallback int) int64 {
	if n <= 0 {
		n = fallback
	}
	return int64(time.Duration(n) * time.Second)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// run creates and starts a container on the installation's network.
func (r *Runtime) run(ctx context.Context, name string, cfg containerConfig, network bool) (string, error) {
	if network {
		if err := r.ensureNetwork(ctx); err != nil {
			return "", err
		}
		cfg.HostConfig.NetworkMode = NetworkName(r.Install)
	}
	if err := r.ensureImage(ctx, cfg.Image); err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"Id"`
	}
	err := r.c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &created)
	if isStatus(err, http.StatusConflict) {
		return "", fmt.Errorf("docker: container name %s is held by a container this installation didn't create; it is left alone", name)
	}
	if err != nil {
		return "", err
	}
	err = r.c.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil, nil)
	if err != nil && !isStatus(err, http.StatusNotModified) {
		return "", err
	}
	return created.ID, nil
}

func (r *Runtime) env(ctx context.Context, plain, refs map[string]string) ([]string, error) {
	vals := map[string]string{}
	for k, v := range plain {
		vals[k] = v
	}
	if len(refs) > 0 {
		if r.Secrets == nil {
			return nil, errors.New("docker: secret references given but no secret store configured")
		}
		resolved, err := r.Secrets.Resolve(ctx, refs)
		if err != nil {
			return nil, err
		}
		for k, v := range resolved {
			vals[k] = v
		}
	}
	var out []string
	for _, k := range sortedKeys(vals) {
		out = append(out, k+"="+vals[k])
	}
	return out, nil
}

// ensureImage pulls an image that isn't on the host.
func (r *Runtime) ensureImage(ctx context.Context, image string) error {
	err := r.c.do(ctx, http.MethodGet, "/images/"+image+"/json", nil, nil, nil)
	if !isStatus(err, http.StatusNotFound) {
		return err
	}
	resp, err := r.c.request(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {image}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct{ Error string }
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("docker: pulling %s: %s", image, msg.Error)
		}
	}
}

func (r *Runtime) ensureNetwork(ctx context.Context) error {
	name := NetworkName(r.Install)
	var n struct{ Labels map[string]string }
	err := r.c.do(ctx, http.MethodGet, "/networks/"+name, nil, nil, &n)
	if err == nil {
		if !r.owned(n.Labels, RoleNetwork) {
			return fmt.Errorf("docker: network %s exists without this installation's labels; it is left alone", name)
		}
		return nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return err
	}
	err = r.c.do(ctx, http.MethodPost, "/networks/create", nil,
		map[string]any{"Name": name, "Driver": "bridge", "Labels": r.labels(RoleNetwork)}, nil)
	if isStatus(err, http.StatusConflict) {
		return nil // created concurrently
	}
	return err
}

// ensureVolume creates a volume, or checks that the existing one is ours.
func (r *Runtime) ensureVolume(ctx context.Context, name string, labels map[string]string) error {
	var v struct{ Labels map[string]string }
	err := r.c.do(ctx, http.MethodGet, "/volumes/"+name, nil, nil, &v)
	if err == nil {
		if !r.owned(v.Labels, labels[LabelRole]) {
			return fmt.Errorf("docker: volume %s exists without this installation's labels; it is left alone", name)
		}
		return nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return err
	}
	return r.c.do(ctx, http.MethodPost, "/volumes/create", nil, map[string]any{"Name": name, "Labels": labels}, nil)
}

// Stop stops and removes an instance's containers. Its volumes stay.
func (r *Runtime) Stop(ctx context.Context, ref ops.InstanceRef) error {
	existing, err := r.containers(ctx, RoleInstance, &ref)
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.State == "running" {
			err := r.c.do(ctx, http.MethodPost, "/containers/"+c.ID+"/stop",
				url.Values{"t": {strconv.Itoa(int(r.StopTimeout / time.Second))}}, nil, nil)
			if err != nil && !isStatus(err, http.StatusNotModified) && !isStatus(err, http.StatusNotFound) {
				return err
			}
		}
		if err := r.remove(ctx, c.ID); err != nil {
			return err
		}
	}
	return nil
}

// RunJob starts a job unless a run for its release already exists.
func (r *Runtime) RunJob(ctx context.Context, s ops.JobSpec) error {
	existing, err := r.containers(ctx, RoleJob, &s.Ref)
	if err != nil || len(existing) > 0 {
		return err
	}
	env, err := r.env(ctx, s.Env, s.SecretRefs)
	if err != nil {
		return err
	}
	labels := refLabels(r.labels(RoleJob), s.Ref)
	labels[LabelImage] = s.Image
	_, err = r.run(ctx, jobName(r.Install, s.Ref), containerConfig{Image: s.Image, Env: env, Labels: labels}, true)
	return err
}

// Observe reports the host, running instances and every job run.
func (r *Runtime) Observe(ctx context.Context) (model.Observation, error) {
	var info struct {
		Name     string
		MemTotal int64
		NCPU     int
	}
	if err := r.c.do(ctx, http.MethodGet, "/info", nil, nil, &info); err != nil {
		return model.Observation{}, err
	}
	obs := model.Observation{At: time.Now(), Host: model.HostObservation{Name: info.Name,
		MemoryTotalMiB: int32(info.MemTotal >> 20), MemoryAvailableMiB: r.memAvailable(), CPUMillisTotal: int32(info.NCPU * 1000)}}
	instances, err := r.containers(ctx, RoleInstance, nil)
	if err != nil {
		return model.Observation{}, err
	}
	for _, c := range instances {
		if c.State != "running" {
			continue
		}
		ref, _ := refOf(c.Labels)
		obs.Instances = append(obs.Instances, model.ObservedInstance{ID: c.ID, Application: ref.Application,
			Component: ref.Component, Ordinal: int32(ref.Ordinal), Release: ref.Release, Image: c.Labels[LabelImage],
			Health: healthOf(c.Status)})
	}
	jobs, err := r.containers(ctx, RoleJob, nil)
	if err != nil {
		return model.Observation{}, err
	}
	for _, c := range jobs {
		ref, _ := refOf(c.Labels)
		run := model.JobRun{Application: ref.Application, Component: ref.Component, Release: ref.Release, Status: model.JobRunning}
		if c.State == "exited" || c.State == "dead" {
			var in struct{ State struct{ ExitCode int } }
			if err := r.c.do(ctx, http.MethodGet, "/containers/"+c.ID+"/json", nil, nil, &in); err != nil {
				return model.Observation{}, err
			}
			run.Status = model.JobFailed
			if in.State.ExitCode == 0 && c.State == "exited" {
				run.Status = model.JobSucceeded
			}
		}
		obs.Jobs = append(obs.Jobs, run)
	}
	return obs, nil
}

// memAvailable reads MemAvailable from the host's meminfo, in MiB, or
// returns nil when it can't.
func (r *Runtime) memAvailable() *int32 {
	if r.MemInfo == "" {
		return nil
	}
	b, err := os.ReadFile(r.MemInfo)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "MemAvailable:" && f[2] == "kB" {
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return nil
			}
			mib := int32(kb >> 10)
			return &mib
		}
	}
	return nil
}

// healthOf reads a running container's health from its list status, for
// example "Up 3 minutes (healthy)". A container without a health check is
// healthy while it runs.
func healthOf(status string) model.InstanceHealth {
	switch {
	case strings.Contains(status, "(health: starting)"):
		return model.HealthStarting
	case strings.Contains(status, "(unhealthy)"):
		return model.HealthUnhealthy
	}
	return model.HealthHealthy
}

// Events streams this installation's container events until ctx ends or
// the engine closes the stream.
func (r *Runtime) Events(ctx context.Context) (<-chan ops.RuntimeEvent, error) {
	f, _ := json.Marshal(map[string][]string{"type": {"container"}, "label": {LabelInstall + "=" + r.Install}})
	resp, err := r.c.request(ctx, http.MethodGet, "/events", url.Values{"filters": {string(f)}}, nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan ops.RuntimeEvent, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var m struct {
				Action string
				Actor  struct {
					ID         string
					Attributes map[string]string
				}
				TimeNano int64 `json:"timeNano"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			e, ok := r.event(ctx, m.Action, m.Actor.ID, m.Actor.Attributes)
			if !ok {
				continue
			}
			e.At = time.Unix(0, m.TimeNano)
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (r *Runtime) event(ctx context.Context, action, id string, attrs map[string]string) (ops.RuntimeEvent, bool) {
	ref, ok := refOf(attrs)
	if !ok || attrs[LabelInstall] != r.Install {
		return ops.RuntimeEvent{}, false
	}
	e := ops.RuntimeEvent{Instance: ref, ID: id}
	role := attrs[LabelRole]
	switch {
	case role == RoleInstance && action == "start":
		e.Kind, e.Health = ops.EventStarted, r.startHealth(ctx, id)
	case role == RoleInstance && action == "die":
		e.Kind = ops.EventStopped
	case role == RoleInstance && action == "health_status: healthy":
		e.Kind, e.Health = ops.EventHealth, model.HealthHealthy
	case role == RoleInstance && action == "health_status: unhealthy":
		e.Kind, e.Health = ops.EventHealth, model.HealthUnhealthy
	case role == RoleJob && action == "die":
		e.Kind = ops.EventJobFinished
	default:
		return ops.RuntimeEvent{}, false
	}
	return e, true
}

// startHealth is a just-started instance's health. Without a health check
// (none set by OAP and none in the image) Docker never sends a
// health_status event, so the instance is healthy while it runs, as
// healthOf reads it. If the container can't be inspected it is starting,
// and the next resync settles it.
func (r *Runtime) startHealth(ctx context.Context, id string) model.InstanceHealth {
	var in struct {
		State struct{ Health *struct{ Status string } }
	}
	if err := r.c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &in); err != nil {
		return model.HealthStarting
	}
	if in.State.Health == nil {
		return model.HealthHealthy
	}
	switch in.State.Health.Status {
	case "healthy":
		return model.HealthHealthy
	case "unhealthy":
		return model.HealthUnhealthy
	}
	return model.HealthStarting
}

// SnapshotVolume copies a volume into a new one, named after key so that a
// second call returns the first snapshot. A copy interrupted half way is
// finished by the next call; a finished one is never copied again.
func (r *Runtime) SnapshotVolume(ctx context.Context, key, app, component, volume string) (string, error) {
	src := volumeName(r.Install, app, component, volume)
	var v struct{ Labels map[string]string }
	if err := r.c.do(ctx, http.MethodGet, "/volumes/"+src, nil, nil, &v); err != nil {
		return "", fmt.Errorf("docker: snapshot source %s: %w", src, err)
	}
	if !r.owned(v.Labels, RoleVolume) {
		return "", fmt.Errorf("docker: volume %s exists without this installation's labels; it is left alone", src)
	}
	sum := sha256.Sum256([]byte(key))
	dst := fmt.Sprintf("oap-%s-snap-%x", r.Install, sum[:8])
	labels := r.labels(RoleSnapshot)
	labels[LabelApplication], labels[LabelComponent], labels[LabelVolume], labels[LabelSnapshotKey] = app, component, volume, key
	if err := r.ensureVolume(ctx, dst, labels); err != nil {
		return "", err
	}
	cfg := containerConfig{Image: r.HelperImage, Labels: r.labels(RoleHelper),
		Cmd: []string{"sh", "-c", "test -f /to/.oap-snapshot-complete || { cp -a /from/. /to/ && touch /to/.oap-snapshot-complete; }"},
		HostConfig: hostConfig{Mounts: []mount{
			{Type: "volume", Source: src, Target: "/from", ReadOnly: true},
			{Type: "volume", Source: dst, Target: "/to"}}}}
	id, err := r.run(ctx, clean(fmt.Sprintf("oap-%s-helper-%d", r.Install, time.Now().UnixNano())), cfg, false)
	if err != nil {
		return "", err
	}
	defer r.remove(context.WithoutCancel(ctx), id)
	var res struct{ StatusCode int }
	if err := r.c.do(ctx, http.MethodPost, "/containers/"+id+"/wait", nil, nil, &res); err != nil {
		return "", err
	}
	if res.StatusCode != 0 {
		return "", fmt.Errorf("docker: copying %s to %s exited %d", src, dst, res.StatusCode)
	}
	return dst, nil
}

// DeleteVolume removes a component's volume. A missing volume is already
// deleted; one without this installation's labels is refused.
func (r *Runtime) DeleteVolume(ctx context.Context, app, component, volume string) error {
	name := volumeName(r.Install, app, component, volume)
	var v struct{ Labels map[string]string }
	err := r.c.do(ctx, http.MethodGet, "/volumes/"+name, nil, nil, &v)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !r.owned(v.Labels, RoleVolume) {
		return fmt.Errorf("docker: volume %s exists without this installation's labels; it is left alone", name)
	}
	err = r.c.do(ctx, http.MethodDelete, "/volumes/"+name, nil, nil, nil)
	if isStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// RemoveAll removes every container, volume and network labelled with this
// installation, and nothing else. Tests use it to clean up.
func (r *Runtime) RemoveAll(ctx context.Context) error {
	q := labelFilter(LabelInstall + "=" + r.Install)
	q.Set("all", "1")
	var cs []container
	if err := r.c.do(ctx, http.MethodGet, "/containers/json", q, nil, &cs); err != nil {
		return err
	}
	for _, c := range cs {
		if c.Labels[LabelInstall] == r.Install {
			if err := r.remove(ctx, c.ID); err != nil {
				return err
			}
		}
	}
	var vs struct {
		Volumes []struct {
			Name   string
			Labels map[string]string
		}
	}
	if err := r.c.do(ctx, http.MethodGet, "/volumes", labelFilter(LabelInstall+"="+r.Install), nil, &vs); err != nil {
		return err
	}
	for _, v := range vs.Volumes {
		if v.Labels[LabelInstall] == r.Install {
			if err := r.c.do(ctx, http.MethodDelete, "/volumes/"+v.Name, nil, nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				return err
			}
		}
	}
	var ns []struct {
		ID     string `json:"Id"`
		Labels map[string]string
	}
	if err := r.c.do(ctx, http.MethodGet, "/networks", labelFilter(LabelInstall+"="+r.Install), nil, &ns); err != nil {
		return err
	}
	for _, n := range ns {
		if n.Labels[LabelInstall] == r.Install {
			if err := r.c.do(ctx, http.MethodDelete, "/networks/"+n.ID, nil, nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				return err
			}
		}
	}
	return nil
}

type containerConfig struct {
	Image            string
	Cmd              []string            `json:",omitempty"`
	Env              []string            `json:",omitempty"`
	Labels           map[string]string   `json:",omitempty"`
	ExposedPorts     map[string]struct{} `json:",omitempty"`
	Healthcheck      *healthcheck        `json:",omitempty"`
	HostConfig       hostConfig
	NetworkingConfig *networkingConfig `json:",omitempty"`
}

type networkingConfig struct {
	EndpointsConfig map[string]endpointConfig
}

type endpointConfig struct {
	Aliases []string `json:",omitempty"`
}

type healthcheck struct {
	Test                           []string
	Interval, Timeout, StartPeriod int64
	Retries                        int
}

type hostConfig struct {
	NetworkMode   string                   `json:",omitempty"`
	Memory        int64                    `json:",omitempty"`
	NanoCPUs      int64                    `json:"NanoCpus,omitempty"`
	Mounts        []mount                  `json:",omitempty"`
	PortBindings  map[string][]portBinding `json:",omitempty"`
	RestartPolicy restartPolicy            `json:",omitempty"`
}

type restartPolicy struct {
	Name string `json:",omitempty"`
}

type mount struct {
	Type, Source, Target string
	ReadOnly             bool `json:",omitempty"`
}

type portBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string
}
