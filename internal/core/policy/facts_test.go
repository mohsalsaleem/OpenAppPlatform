package policy

import "testing"

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		files                  []string
		migrations, lock, docs bool
	}{
		{[]string{"docs/install_linux.md"}, false, false, true},
		{[]string{"README.md", "docs/img/arch.png", "LICENSE"}, false, false, true},
		{[]string{"README.md", "main.go"}, false, false, false},
		{[]string{"go.mod", "go.sum"}, false, true, false},
		{[]string{"web/package-lock.json"}, false, true, false},
		{[]string{"db/migrate/20261020_add_notes.rb", "app/models/note.rb"}, true, false, false},
		{[]string{"prisma/migrations/2026_init/migration.sql"}, true, false, false},
		{[]string{"alembic/versions/ab12_add.py"}, true, false, false},
		{[]string{"src/main/resources/db/V2__add_column.sql"}, true, false, false},
		{[]string{"internal/config/migration/multi_account.go"}, false, false, false},
		{[]string{"migrations.go"}, false, false, false},
		{nil, false, false, false},
	} {
		m, l, d := Classify(c.files)
		if m != c.migrations || l != c.lock || d != c.docs {
			t.Errorf("%v: migrations %v lockfile %v docs-only %v; want %v %v %v", c.files, m, l, d, c.migrations, c.lock, c.docs)
		}
	}
}
