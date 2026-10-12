package coolify

import "github.com/mohsalsaleem/OpenAppPlatform/internal/operator"

// Deliberately excludes passwords, environment values, connection URLs and storage.
type databaseResource struct {
	UUID   string `json:"uuid"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

func (c *Client) databaseResource(d databaseResource) operator.Resource {
	return operator.Resource{ID: d.UUID, Name: d.Name, Status: d.Status, Image: d.Image, ArtifactKind: "database", ProjectID: c.target.ProjectID, Environment: c.target.Environment}
}
