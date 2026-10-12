package domain

import "testing"

func TestDatabaseIsAnObservedDependencyWithoutLifecycle(t *testing.T) {
	db := Component{Name: "database", Kind: "database", Management: "observe", ResourceID: "native-db", DatabaseEndpoint: "postgresql://database:5432/application", Instances: 1}
	m := Manifest{Name: "example", Environment: "staging", TargetID: "target", Components: []Component{db, {Name: "api", Image: "example:1", Port: 8080, DependsOn: []string{"database"}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	steps := InitialSteps(m)
	if len(steps) != 1 || steps[0].Component != "api" {
		t.Fatal("database entered the release")
	}
	for _, bad := range []Component{{Name: "database", Kind: "database", Image: "postgres:17", DatabaseEndpoint: db.DatabaseEndpoint}, {Name: "database", Kind: "database", Management: "observe", ResourceID: "db", DatabaseEndpoint: "postgresql://user:secret@database/app"}, {Name: "database", Kind: "database", Management: "observe", ResourceID: "db", DatabaseEndpoint: db.DatabaseEndpoint, Instances: 2}} {
		m.Components[0] = bad
		if m.Validate() == nil {
			t.Fatal("unsafe database accepted")
		}
	}
}
