package scaffold

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderPathAndContentTemplates(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "{{.projectName}}/README.md", Content: "# {{.projectName}}"},
		},
	}
	files, err := renderFiles(tmpl, map[string]any{"projectName": "demo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 || files[0].path != "demo/README.md" {
		t.Fatalf("unexpected files: %+v", files)
	}
	if string(files[0].content) != "# demo" {
		t.Fatalf("unexpected content: %q", files[0].content)
	}
}

func TestRenderWhenConditional(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "a.txt", Content: "a"},
			{Path: "b.txt", Content: "b", When: `useB`},
			{Path: "c.txt", Content: "c", When: `mode == "prod" && useB`},
			{Path: "d.txt", Content: "d", When: `!useB || mode != "dev"`},
		},
	}
	vars := map[string]any{"useB": true, "mode": "dev"}
	files, err := renderFiles(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.path)
	}
	want := []string{"a.txt", "b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestRenderRepeat(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{
				Path:    "services/{{.svc.name}}/main.go",
				Content: "package {{.svc.name}} // port {{.svc.port}}",
				Repeat:  &Repeat{Var: "services", As: "svc"},
			},
		},
	}
	vars := map[string]any{
		"services": []any{
			map[string]any{"name": "api", "port": "8080"},
			map[string]any{"name": "web", "port": "3000"},
		},
	}
	files, err := renderFiles(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].path != "services/api/main.go" || files[1].path != "services/web/main.go" {
		t.Fatalf("unexpected paths: %v, %v", files[0].path, files[1].path)
	}
	if string(files[1].content) != "package web // port 3000" {
		t.Fatalf("unexpected content: %q", files[1].content)
	}
}

func TestRenderDependencyOrder(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "c.txt", Content: "c", DependsOn: []string{"b.txt"}},
			{Path: "a.txt", Content: "a"},
			{Path: "b.txt", Content: "b", DependsOn: []string{"a.txt"}},
		},
	}
	files, err := renderFiles(tmpl, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := []string{files[0].path, files[1].path, files[2].path}
	want := []string{"a.txt", "b.txt", "c.txt"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want order %v, got %v", want, got)
		}
	}
}

func TestRenderCycleDetected(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "a.txt", Content: "a", DependsOn: []string{"c.txt"}},
			{Path: "b.txt", Content: "b", DependsOn: []string{"a.txt"}},
			{Path: "c.txt", Content: "c", DependsOn: []string{"b.txt"}},
		},
	}
	_, err := renderFiles(tmpl, map[string]any{})
	if err == nil {
		t.Fatal("expected cycle error")
	}
	var cerr *CycleError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	// 报错要可读：包含环上的文件路径。
	msg := cerr.Error()
	if !strings.Contains(msg, "cyclic dependency") || !strings.Contains(msg, "a.txt") {
		t.Fatalf("cycle error not readable: %q", msg)
	}
}

func TestRenderUnknownDependency(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "a.txt", Content: "a", DependsOn: []string{"ghost.txt"}},
		},
	}
	_, err := renderFiles(tmpl, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "ghost.txt") {
		t.Fatalf("expected unknown dependency error, got %v", err)
	}
}

func TestRenderPathEscapeRejected(t *testing.T) {
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "../{{.name}}.txt", Content: "x"},
		},
	}
	_, err := renderFiles(tmpl, map[string]any{"name": "evil"})
	if err == nil || !strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("expected escape error, got %v", err)
	}
}
