package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpecificationExportCannotSilentlyEraseLiterals(t *testing.T) {
	m := Manifest{Name: "portable", Environment: "staging", TargetID: "docker", Components: []Component{{Name: "api", Kind: "web", Port: 8080, Image: "example/api:1", Instances: 1, Env: map[string]string{"PASSWORD": "private-literal"}, SecretRefs: map[string]string{"TOKEN": "private-ref"}}}}
	spec := ExportSpecification(m)
	raw, _ := json.Marshal(spec)
	if strings.Contains(string(raw), "private-literal") || m.Components[0].Env["PASSWORD"] != "private-literal" {
		t.Fatal("export leaked or mutated source")
	}
	if spec.Spec.Components[0].SecretRefs["TOKEN"] != "private-ref" {
		t.Fatal("export lost secret reference")
	}
	if spec.Validate() == nil {
		t.Fatal("redacted export accepted as complete input")
	}
	spec.Spec.Components[0].Env = map[string]string{"PASSWORD": "replacement"}
	spec.RedactedFields = nil
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	spec.APIVersion = "unknown/v2"
	if spec.Validate() == nil {
		t.Fatal("unknown version accepted")
	}
}
