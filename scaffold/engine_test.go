package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentApplySameTemplate 多个 goroutine 对同一工作区执行同一模板：
// 全部成功，最终内容一致，无日志残留。
func TestConcurrentApplySameTemplate(t *testing.T) {
	ws := t.TempDir()
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "a.txt", Content: "aaa\n"},
			{Path: "dir/b.txt", Content: "bbb\n"},
			{Path: "config.json", Content: `{"x": 1}`, Merge: "json"},
		},
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictMerge})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}
	for path, want := range map[string]string{
		"a.txt":       "aaa\n",
		"dir/b.txt":   "bbb\n",
		"config.json": `{"x": 1}`,
	} {
		got, err := os.ReadFile(filepath.Join(ws, path))
		if err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
		if path != "config.json" && string(got) != want {
			t.Fatalf("%s: want %q, got %q", path, want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, journalDirName)); !os.IsNotExist(err) {
		t.Fatal("journal residue after concurrent apply")
	}
}

// TestConcurrentApplyDisjointTemplates 不同模板写入不相交的文件，
// 并发执行后两组文件都必须完整存在。
func TestConcurrentApplyDisjointTemplates(t *testing.T) {
	ws := t.TempDir()
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		tmpl := &Template{
			SpecVersion: "1.0",
			Name:        fmt.Sprintf("t%d", i),
			Files: []FileRule{
				{Path: fmt.Sprintf("mod%d/file.txt", i), Content: fmt.Sprintf("content-%d\n", i)},
			},
		}
		wg.Add(1)
		go func(i int, tmpl *Template) {
			defer wg.Done()
			_, errs[i] = New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictSkip})
		}(i, tmpl)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		got, err := os.ReadFile(filepath.Join(ws, fmt.Sprintf("mod%d/file.txt", i)))
		if err != nil {
			t.Fatalf("missing file from template %d: %v", i, err)
		}
		if string(got) != fmt.Sprintf("content-%d\n", i) {
			t.Fatalf("template %d file corrupted: %q", i, got)
		}
	}
}

// TestEngineEndToEnd 从磁盘模板目录完整走一遍 Plan + Apply。
func TestEngineEndToEnd(t *testing.T) {
	tdir := t.TempDir()
	manifest := `{
	  "specVersion": "1.0",
	  "name": "go-service",
	  "variables": {
	    "name": {"type": "string", "required": true, "pattern": "[a-z][a-z0-9-]*"},
	    "withTests": {"type": "bool", "default": true}
	  },
	  "files": [
	    {"path": "{{.name}}/go.mod", "content": "module {{.name}}\n"},
	    {"path": "{{.name}}/main.go", "source": "main.go.tmpl", "dependsOn": ["{{.name}}/go.mod"]},
	    {"path": "{{.name}}/main_test.go", "content": "package main\n", "when": "withTests"}
	  ]
	}`
	os.WriteFile(filepath.Join(tdir, ManifestName), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(tdir, "main.go.tmpl"), []byte("package main // {{.name}}\n"), 0o644)

	tmpl, err := LoadTemplate(tdir)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	ws := filepath.Join(t.TempDir(), "ws")
	plan, err := New().Apply(tmpl, Options{
		Workspace:  ws,
		Vars:       map[string]any{"name": "my-svc"},
		OnConflict: ConflictSkip,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(plan.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(plan.Files))
	}
	got, err := os.ReadFile(filepath.Join(ws, "my-svc", "main.go"))
	if err != nil || string(got) != "package main // my-svc\n" {
		t.Fatalf("main.go: %q, %v", got, err)
	}
}
