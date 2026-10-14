package docker

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

var jobReleaseID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// JobResult is native evidence only. The durable controller must journal its
// dispatch before calling RunJob and enforce the release's observation timeout.
// A timeout cannot establish whether a migration committed application data.
type JobResult struct {
	State      string `json:"state"`
	ResourceID string `json:"resourceId"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

func validateJobSpec(s operator.Spec) error {
	parts := strings.Split(s.Ownership, ":")
	if !jobReleaseID.MatchString(s.JobReleaseID) || len(parts) != 3 || parts[0] != "OpenAppPlatform" || !jobReleaseID.MatchString(parts[1]) || parts[2] != s.Component.Name || s.Component.Name == "" {
		return errors.New("job requires application ownership and a release identity")
	}
	if s.Name != "oap-job-"+s.JobReleaseID+"-"+s.Component.Name {
		return errors.New("job reference must belong to its release")
	}
	c := s.Component
	if c.Kind != "worker" || c.Instances != 1 || c.Port != 0 || c.HostPort != 0 || c.ResourceID != "" || c.Management != "" || c.HealthCheck != nil || c.Readiness != nil {
		return errors.New("job requires one owned image process without ports or health probes")
	}
	return nil
}

// EnsureJob prepares one image's default command on the application network.
// Each release receives a distinct reference; replaying the same release keeps
// its completed container as evidence instead of creating another execution.
// Public manifest/planning support is deliberately not enabled by this primitive.
func (c *Client) EnsureJob(ctx context.Context, s operator.Spec, release string) (operator.Resource, error) {
	if s.Component.Kind == "job" {
		s.Component.Kind = "worker"
		s.Component.Readiness = nil
	}
	s.JobReleaseID = release
	s.Name = "oap-job-" + release + "-" + s.Component.Name
	return c.Ensure(ctx, s)
}

func (c *Client) checkJob(v container, ref, release string) error {
	if !jobReleaseID.MatchString(release) || !c.owned(v) || v.Config.Labels[prefix+"reference"] != ref || v.Config.Labels[prefix+"job-release"] != release || !strings.HasPrefix(ref, "oap-job-"+release+"-") {
		return errors.New("job identity differs from the release")
	}
	if v.HostConfig.RestartPolicy.Name != "no" || v.Config.Healthcheck == nil || len(v.Config.Healthcheck.Test) != 1 || v.Config.Healthcheck.Test[0] != "NONE" {
		return errors.New("job restart or health configuration changed")
	}
	return nil
}

// RunJob starts a prepared process. A controller must serialize calls. An
// uncertain response is observed by exact ID/reference, never blindly retried.
func (c *Client) RunJob(ctx context.Context, ref, release string) (string, error) {
	v, err := c.inspectContainer(ctx, ref+"-next")
	if isStatus(err, 404) {
		v, err = c.inspectContainer(ctx, ref)
	}
	if err != nil {
		return "", err
	}
	if err := c.checkJob(v, ref, release); err != nil {
		return "", err
	}
	if v.State.Status != "created" {
		// Running, failed and completed processes must never be started again.
		return v.ID, nil
	}
	return c.Deploy(ctx, ref)
}

func (c *Client) ObserveJob(ctx context.Context, ref, release, containerID string) (JobResult, error) {
	v, err := c.inspectContainer(ctx, ref)
	if err != nil {
		return JobResult{}, err
	}
	if err := c.checkJob(v, ref, release); err != nil {
		return JobResult{}, err
	}
	if containerID == "" || v.ID != containerID {
		return JobResult{}, errors.New("job container changed while observing the release")
	}
	result := JobResult{State: "running", ResourceID: v.ID}
	switch v.State.Status {
	case "exited", "dead":
		code := v.State.ExitCode
		result.ExitCode = &code
		result.State = "failed"
		if v.State.Status == "exited" && code == 0 {
			result.State = "succeeded"
		}
	case "created":
		result.State = "pending"
	case "running":
	default:
		return JobResult{}, errors.New("unexpected job process state: " + strconv.Quote(v.State.Status))
	}
	return result, nil
}
