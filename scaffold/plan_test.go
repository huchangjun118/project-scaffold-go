package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func simpleTemplate() *Template {
	return &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "config.json", Content: `{"a": 1, "b": 2}`, Merge: "json"},
			{Path: "notes.txt", Content: "line1\nline2\n", Merge: "lines"},
			{Path: "main.go", Content: "package main\n"},
		},
	}
}

func TestPlanAllCreate(t *testing.T) {
	ws := t.TempDir()
	plan, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Files) != 3 {
		t.Fatalf("expected 3 planned files, got %d", len(plan.Files))
	}
	for _, pf := range plan.Files {
		if pf.Op != OpCreate {
			t.Fatalf("expected all create, got %s for %s", pf.Op, pf.Path)
		}
	}
}

func TestPlanDoesNotTouchWorkspace(t *testing.T) {
	ws := t.TempDir()
	if _, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("plan phase must not write to workspace, found %d entries", len(entries))
	}
}

func TestConflictSkip(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "main.go"), []byte("user edited\n"), 0o644)
	plan, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictSkip})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ops := map[string]Op{}
	for _, pf := range plan.Files {
		ops[pf.Path] = pf.Op
	}
	if ops["main.go"] != OpSkip {
		t.Fatalf("expected main.go skipped, got %s", ops["main.go"])
	}
	if ops["config.json"] != OpCreate {
		t.Fatalf("expected config.json created, got %s", ops["config.json"])
	}
}

func TestConflictOverwrite(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "main.go"), []byte("user edited\n"), 0o644)
	plan, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, pf := range plan.Files {
		if pf.Path == "main.go" {
			if pf.Op != OpOverwrite || string(pf.Content) != "package main\n" {
				t.Fatalf("expected overwrite with template content, got %s %q", pf.Op, pf.Content)
			}
		}
	}
}

func TestConflictMergeJSON(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "config.json"), []byte(`{"a": 1, "user": true}`), 0o644)
	plan, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictMerge})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, pf := range plan.Files {
		if pf.Path == "config.json" {
			if pf.Op != OpMerge {
				t.Fatalf("expected merge, got %s", pf.Op)
			}
			got := string(pf.Content)
			if !containsAll(got, `"a": 1`, `"b": 2`, `"user": true`) {
				t.Fatalf("merged content missing keys: %s", got)
			}
		}
	}
}

func TestConflictMergeLines(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("line1\nuser-line\n"), 0o644)
	plan, err := New().Plan(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictMerge})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, pf := range plan.Files {
		if pf.Path == "notes.txt" {
			if pf.Op != OpMerge {
				t.Fatalf("expected merge, got %s", pf.Op)
			}
			want := "line1\nuser-line\nline2\n"
			if string(pf.Content) != want {
				t.Fatalf("want %q, got %q", want, pf.Content)
			}
		}
	}
}

func TestConflictMergeFailureKeepsOriginal(t *testing.T) {
	ws := t.TempDir()
	original := []byte("not json at all")
	os.WriteFile(filepath.Join(ws, "config.json"), []byte(original), 0o644)
	_, err := New().Apply(simpleTemplate(), Options{Workspace: ws, OnConflict: ConflictMerge})
	if err == nil {
		t.Fatal("expected merge failure")
	}
	got, _ := os.ReadFile(filepath.Join(ws, "config.json"))
	if string(got) != string(original) {
		t.Fatalf("original file damaged after merge failure: %q", got)
	}
	// 整个事务应回滚：其他文件也不应落地。
	if _, err := os.Stat(filepath.Join(ws, "main.go")); !os.IsNotExist(err) {
		t.Fatal("transaction should have rolled back main.go")
	}
}

func TestApplyIdempotent(t *testing.T) {
	ws := t.TempDir()
	tmpl := simpleTemplate()
	opts := Options{Workspace: ws, OnConflict: ConflictMerge}

	if _, err := New().Apply(tmpl, opts); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	snapshot := snapshotDir(t, ws)

	// 第二次执行：所有文件内容一致，计划应全部跳过。
	plan, err := New().Apply(tmpl, opts)
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	for _, pf := range plan.Files {
		if pf.Op != OpSkip {
			t.Fatalf("second run should skip %s, got %s", pf.Path, pf.Op)
		}
	}
	if got := snapshotDir(t, ws); got != snapshot {
		t.Fatalf("workspace changed after idempotent re-run:\nbefore:\n%s\nafter:\n%s", snapshot, got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func snapshotDir(t *testing.T, root string) string {
	t.Helper()
	var out string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if info.IsDir() {
			out += "D " + rel + "\n"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out += "F " + rel + "\n" + string(data) + "\n---\n"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
