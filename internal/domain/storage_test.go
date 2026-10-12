package domain

import "testing"

func TestStorageAndLimitsContract(t *testing.T) {
	base := Component{Name: "web", Instances: 1, Volumes: []VolumeMount{{Name: "uploads", MountPath: "/data/uploads"}}, Limits: &ResourceLimits{MemoryMiB: 256, CPUMillis: 500}}
	if err := base.ValidateStorageAndLimits(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Component){
		func(c *Component) { c.Instances = 2 }, func(c *Component) { c.ResourceID = "native" }, func(c *Component) { c.Management = "observe" }, func(c *Component) { c.Kind = "database" },
		func(c *Component) { c.Limits = &ResourceLimits{MemoryMiB: 63, CPUMillis: 500} }, func(c *Component) { c.Limits = &ResourceLimits{MemoryMiB: 256, CPUMillis: 64001} },
		func(c *Component) { c.Volumes = []VolumeMount{{Name: "data", MountPath: "/"}} },
		func(c *Component) { c.Volumes = []VolumeMount{{Name: "data", MountPath: "/var/run/docker.sock"}} },
		func(c *Component) { c.Volumes = []VolumeMount{{Name: "data", MountPath: "/data/../run"}} },
		func(c *Component) {
			c.Volumes = []VolumeMount{{Name: "data", MountPath: "/data"}, {Name: "more", MountPath: "/data/uploads"}}
		},
		func(c *Component) {
			c.Volumes = []VolumeMount{{Name: "data", MountPath: "/data"}, {Name: "data", MountPath: "/uploads"}}
		},
	} {
		c := base
		change(&c)
		if c.ValidateStorageAndLimits() == nil {
			t.Fatalf("unsafe storage accepted: %+v", c)
		}
	}
}
