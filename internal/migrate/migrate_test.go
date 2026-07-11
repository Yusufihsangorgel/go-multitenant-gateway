package migrate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func file(body string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(body)}
}

func TestLoadFromSortsNumericallyNotLexically(t *testing.T) {
	// Lexical order would put "10" before "2"; the runner must not.
	fsys := fstest.MapFS{
		"10_add_index.sql":   file("CREATE INDEX ..."),
		"2_create_notes.sql": file("CREATE TABLE notes ..."),
		"1_bootstrap.sql":    file("CREATE TABLE bootstrap ..."),
	}
	migs, err := LoadFrom(fsys)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(migs) != 3 {
		t.Fatalf("got %d migrations, want 3", len(migs))
	}
	wantOrder := []int{1, 2, 10}
	wantNames := []string{"bootstrap", "create_notes", "add_index"}
	for i, m := range migs {
		if m.Version != wantOrder[i] {
			t.Errorf("position %d: version %d, want %d", i, m.Version, wantOrder[i])
		}
		if m.Name != wantNames[i] {
			t.Errorf("position %d: name %q, want %q", i, m.Name, wantNames[i])
		}
	}
	if migs[1].SQL != "CREATE TABLE notes ..." {
		t.Errorf("SQL body not carried through: %q", migs[1].SQL)
	}
}

func TestLoadFromRejectsDuplicateVersions(t *testing.T) {
	// 0001 and 1 parse to the same version; that is a conflict, not an order.
	fsys := fstest.MapFS{
		"0001_first.sql": file("SELECT 1"),
		"1_second.sql":   file("SELECT 2"),
	}
	if _, err := LoadFrom(fsys); err == nil {
		t.Fatal("want duplicate version error, got nil")
	} else if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error should name the duplicate: %v", err)
	}
}

func TestLoadFromRejectsMalformedFilenames(t *testing.T) {
	cases := []string{
		"notes.sql",       // no version prefix
		"abc_notes.sql",   // non-numeric version
		"0_zero.sql",      // versions start at 1
		"-1_negative.sql", // negative version
		"0001_.sql",       // empty name
	}
	for _, name := range cases {
		fsys := fstest.MapFS{name: file("SELECT 1")}
		if _, err := LoadFrom(fsys); err == nil {
			t.Errorf("filename %q: want error, got nil", name)
		}
	}
}

func TestLoadFromIgnoresNonSQLFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_create_notes.sql": file("CREATE TABLE notes ..."),
		"README.md":             file("not a migration"),
		"0002_backup.sql.bak":   file("not a migration either"),
	}
	migs, err := LoadFrom(fsys)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(migs) != 1 || migs[0].Version != 1 {
		t.Fatalf("got %+v, want exactly the one .sql migration", migs)
	}
}

func TestLoadReturnsEmbeddedSetInOrder(t *testing.T) {
	migs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("embedded migration set is empty")
	}
	if migs[0].Version != 1 || migs[0].Name != "create_notes" {
		t.Fatalf("first embedded migration = %d (%s), want 1 (create_notes)", migs[0].Version, migs[0].Name)
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].Version <= migs[i-1].Version {
			t.Fatalf("embedded set out of order at position %d: %d after %d", i, migs[i].Version, migs[i-1].Version)
		}
	}
}
