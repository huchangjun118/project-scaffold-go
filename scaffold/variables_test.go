package scaffold

import (
	"errors"
	"strings"
	"testing"
)

func tmplWithVars(vars map[string]Variable) *Template {
	return &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Variables:   vars,
		Files:       []FileRule{{Path: "a.txt", Content: "x"}},
	}
}

func TestResolveVariablesAppliesDefaults(t *testing.T) {
	tmpl := tmplWithVars(map[string]Variable{
		"name": {Type: VarString, Default: "demo"},
		"port": {Type: VarInt, Default: float64(8080)},
	})
	vars, err := ResolveVariables(tmpl, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["name"] != "demo" || vars["port"] != float64(8080) {
		t.Fatalf("defaults not applied: %v", vars)
	}
}

func TestResolveVariablesAggregatesAllErrors(t *testing.T) {
	tmpl := tmplWithVars(map[string]Variable{
		"name":    {Type: VarString, Required: true, Pattern: "[a-z]+"},
		"port":    {Type: VarInt, Min: ptrInt64(1), Max: ptrInt64(65535)},
		"enabled": {Type: VarBool},
		"mode":    {Type: VarString, Enum: []any{"dev", "prod"}},
	})
	_, err := ResolveVariables(tmpl, map[string]any{
		"name":    "BAD-NAME",
		"port":    float64(70000),
		"enabled": "yes",
		"mode":    "staging",
		"unknown": "x",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(verr.Errors) != 5 {
		t.Fatalf("expected 5 aggregated errors, got %d: %v", len(verr.Errors), verr)
	}
	// 每个错误都要能指到具体变量。
	want := map[string]bool{"name": false, "port": false, "enabled": false, "mode": false, "unknown": false}
	for _, ve := range verr.Errors {
		if _, ok := want[ve.Name]; !ok {
			t.Fatalf("unexpected variable in error: %q", ve.Name)
		}
		want[ve.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing error for variable %q", name)
		}
	}
}

func TestResolveVariablesMissingRequired(t *testing.T) {
	tmpl := tmplWithVars(map[string]Variable{
		"name": {Type: VarString, Required: true},
	})
	_, err := ResolveVariables(tmpl, nil)
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Fatalf("expected error pointing at variable name, got %v", err)
	}
}

func TestResolveVariablesWrongType(t *testing.T) {
	tmpl := tmplWithVars(map[string]Variable{
		"tags": {Type: VarList},
	})
	_, err := ResolveVariables(tmpl, map[string]any{"tags": "not-a-list"})
	if err == nil || !strings.Contains(err.Error(), "expected list") {
		t.Fatalf("expected list type error, got %v", err)
	}
}

func ptrInt64(v int64) *int64 { return &v }
