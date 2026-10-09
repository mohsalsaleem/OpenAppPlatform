// Package operators is the composition boundary for concrete operator adapters.
package operators

import (
	"bytes"
	"errors"
	"os"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

func New(t domain.Target) (operator.Adapter, error) {
	if e := t.Validate(); e != nil {
		return nil, e
	}
	switch t.Operator {
	case "coolify":
		if len(t.Settings) > 0 && !bytes.Equal(bytes.TrimSpace(t.Settings), []byte("{}")) && !bytes.Equal(bytes.TrimSpace(t.Settings), []byte("null")) {
			return nil, errors.New("Coolify settings are not supported; reference credentials through tokenEnv")
		}
		if t.ProjectID == "" || t.ServerID == "" {
			return nil, errors.New("Coolify projectId and serverId are required")
		}
		return coolify.New(t, os.Getenv(t.TokenEnv))
	case "docker":
		return docker.New(t)
	default:
		return nil, errors.New("unsupported operator")
	}
}
