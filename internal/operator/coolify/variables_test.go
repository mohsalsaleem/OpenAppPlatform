package coolify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type memoryJournal struct {
	rows map[string]operator.VariableOwnership
}

func (j *memoryJournal) Reserve(context.Context, string) error { return nil }
func (j *memoryJournal) Variables(context.Context, string) (map[string]operator.VariableOwnership, error) {
	out := map[string]operator.VariableOwnership{}
	for k, v := range j.rows {
		out[k] = v
	}
	return out, nil
}
func (j *memoryJournal) Begin(_ context.Context, _, key, uuid, hash string) error {
	value := j.rows[key]
	if value.UUID != uuid && value.UUID != "" {
		return errors.New("identity mismatch")
	}
	value.UUID = uuid
	value.Intent = hash
	j.rows[key] = value
	return nil
}
func (j *memoryJournal) Commit(_ context.Context, _, key, uuid, hash string) error {
	j.rows[key] = operator.VariableOwnership{UUID: uuid, Hash: hash}
	return nil
}
func (j *memoryJournal) Forget(_ context.Context, _, key, uuid string) error {
	if j.rows[key].UUID == uuid {
		delete(j.rows, key)
	}
	return nil
}
func TestRuntimeVariablesPreserveOperatorOwnershipAndRecoverUpdates(t *testing.T) {
	rows := map[string]variable{"OPERATOR_SECRET": {UUID: "secret-id", Key: "OPERATOR_SECRET", Hidden: true}, "OPERATOR_KEEP": {UUID: "keep-id", Key: "OPERATOR_KEEP", Value: "untouched", Literal: true, Runtime: true}}
	calls := []string{}
	losePatch := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/projects/p/staging":
			json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]string{{"uuid": "owned", "description": "OpenAppPlatform:app:web"}}})
		case r.URL.Path == "/api/v1/applications/owned":
			json.NewEncoder(w).Encode(map[string]string{"uuid": "owned", "description": "OpenAppPlatform:app:web"})
		case r.URL.Path == "/api/v1/applications/owned/envs" && r.Method == "GET":
			out := []variable{}
			for _, row := range rows {
				out = append(out, row)
			}
			json.NewEncoder(w).Encode(out)
		case r.URL.Path == "/api/v1/applications/owned/envs":
			var row variable
			if e := json.NewDecoder(r.Body).Decode(&row); e != nil {
				t.Fatal(e)
			}
			calls = append(calls, r.Method+" "+row.Key)
			if row.Key == "OPERATOR_SECRET" || row.Key == "OPERATOR_KEEP" {
				t.Error("unrelated operator variable was mutated")
			}
			row.UUID = rows[row.Key].UUID
			if row.UUID == "" {
				row.UUID = "created-" + row.Key
			}
			rows[row.Key] = row
			if losePatch && r.Method == "PATCH" {
				losePatch = false
				http.Error(w, "sensitive-value", 503)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"uuid": row.UUID})
		case strings.HasPrefix(r.URL.Path, "/api/v1/applications/owned/envs/") && r.Method == "DELETE":
			uuid := strings.TrimPrefix(r.URL.Path, "/api/v1/applications/owned/envs/")
			for key, row := range rows {
				if row.UUID == uuid {
					calls = append(calls, "DELETE "+key)
					delete(rows, key)
				}
			}
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	journal := &memoryJournal{rows: map[string]operator.VariableOwnership{}}
	spec := operator.Spec{Ownership: "OpenAppPlatform:app:web", Variables: journal, Component: domain.Component{Env: map[string]string{"MESSAGE": "literal-$VALUE", "REMOVE": "old"}}}
	if e := client.syncVariables(context.Background(), "owned", spec); e != nil {
		t.Fatal(e)
	}
	if rows["MESSAGE"].Value != "literal-$VALUE" || len(journal.rows) != 2 {
		t.Fatal("configuration missing")
	}
	count := len(calls)
	if e := client.syncVariables(context.Background(), "owned", spec); e != nil || len(calls) != count {
		t.Fatal("repeat issued duplicate mutations", e)
	}
	spec.Component.Env = map[string]string{"MESSAGE": "updated"}
	losePatch = true
	if e := client.syncVariables(context.Background(), "owned", spec); e == nil || strings.Contains(e.Error(), "sensitive-value") {
		t.Fatal("uncertain patch not protected", e)
	}
	if e := client.syncVariables(context.Background(), "owned", spec); e != nil {
		t.Fatal(e)
	}
	if rows["MESSAGE"].Value != "updated" || journal.rows["MESSAGE"].Intent != "" {
		t.Fatal("patch recovery failed")
	}
	if _, exists := rows["REMOVE"]; exists {
		t.Fatal("removed key retained")
	}
	if _, exists := journal.rows["REMOVE"]; exists {
		t.Fatal("ownership record retained after verified deletion")
	}
	if len(rows) != 3 || rows["OPERATOR_KEEP"].Value != "untouched" || !rows["OPERATOR_SECRET"].Hidden {
		t.Fatal("operator configuration changed")
	}
	count = len(calls)
	spec.Component.Env = map[string]string{"OPERATOR_KEEP": "replacement"}
	if e := client.syncVariables(context.Background(), "owned", spec); e == nil || len(calls) != count {
		t.Fatal("operator key takeover allowed")
	}
}
func TestRuntimeVariableIdentityAndFlagDriftBlocksMutations(t *testing.T) {
	for _, kind := range []string{"identity", "value", "secret", "preview", "uncertain-create"} {
		t.Run(kind, func(t *testing.T) {
			row := variable{UUID: "known", Key: "KEY", Value: "old", Literal: true, Runtime: true}
			state := operator.VariableOwnership{UUID: "known", Hash: variableHash("old")}
			switch kind {
			case "identity":
				row.UUID = "replacement"
			case "value":
				row.Value = "operator-edit"
			case "secret":
				row.Hidden = true
			case "preview":
				row.Preview = true
			case "uncertain-create":
				state = operator.VariableOwnership{Intent: variableHash("old")}
			}
			mutations := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations++
					http.Error(w, "unexpected", 500)
					return
				}
				switch r.URL.Path {
				case "/api/v1/projects/p/staging":
					w.Write([]byte(`{"applications":[{"uuid":"owned"}]}`))
				case "/api/v1/applications/owned":
					w.Write([]byte(`{"uuid":"owned","description":"owner"}`))
				default:
					json.NewEncoder(w).Encode([]variable{row})
				}
			}))
			defer srv.Close()
			client, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
			spec := operator.Spec{Ownership: "owner", Variables: &memoryJournal{rows: map[string]operator.VariableOwnership{"KEY": state}}, Component: domain.Component{Env: map[string]string{"KEY": "next"}}}
			if e := client.syncVariables(context.Background(), "owned", spec); e == nil || mutations != 0 {
				t.Fatal(fmt.Sprintf("%s drift was mutated", kind), e)
			}
		})
	}
}
