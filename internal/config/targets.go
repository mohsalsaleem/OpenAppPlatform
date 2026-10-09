package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"io"
)

func Targets(data []byte) ([]domain.Target, error) {
	var raw []struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Operator    string          `json:"operator"`
		URL         string          `json:"url"`
		TokenEnv    string          `json:"tokenEnv"`
		ProjectID   string          `json:"projectId"`
		ServerID    string          `json:"serverId"`
		Environment string          `json:"environment"`
		Settings    json.RawMessage `json:"settings"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(&raw); e != nil {
		return nil, e
	}
	var more any
	if e := d.Decode(&more); e != io.EOF {
		return nil, errors.New("target file must contain one JSON array")
	}
	out := []domain.Target{}
	seen := map[string]bool{}
	for _, r := range raw {
		t := domain.Target{ID: r.ID, Name: r.Name, Operator: r.Operator, URL: r.URL, TokenEnv: r.TokenEnv, ProjectID: r.ProjectID, ServerID: r.ServerID, Environment: r.Environment, Settings: r.Settings}
		if e := t.Validate(); e != nil {
			return nil, e
		}
		if seen[t.ID] {
			return nil, errors.New("duplicate target ID")
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out, nil
}
