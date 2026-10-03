package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadTemplateOK(t *testing.T) {
	dir := writeManifest(t, `{
	  "specVersion": "1.2",
	  "name": "go-service",
	  "variables": {"name": {"type": "string", "required": true}},
	  "files": [{"path": "{{.name}}/go.mod", "content": "module {{.name}}"}]
	}`)
	tmpl, err := LoadTemplate(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmpl.Name != "go-service" || tmpl.Dir != dir {
		t.Fatalf("unexpected template: %+v", tmpl)
	}
}

func TestLoadTemplateUnsupportedSpecVersion(t *testing.T) {
	dir := writeManifest(t, `{
	  "specVersion": "2.0",
	  "name": "t",
	  "files": [{"path": "a.txt", "content": "x"}]
	}`)
	_, err := LoadTemplate(dir)
	if err == nil {
		t.Fatal("expected spec version error")
	}
	var sverr *SpecVersionError
	if !errors.As(err, &sverr) {
		t.Fatalf("expected *SpecVersionError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "2.0") || !strings.Contains(err.Error(), "1.x") {
		t.Fatalf("error should mention template and supported versions: %q", err.Error())
	}
}

func TestLoadTemplateDeprecatedParamsField(t *testing.T) {
	dir := writeManifest(t, `{
	  "specVersion": "1.0",
	  "name": "t",
	  "params": {"name": {"type": "string"}},
	  "files": [{"path": "a.txt", "content": "x"}]
	}`)
	_, err := LoadTemplate(dir)
	var derr *DeprecatedFieldError
	if !errors.As(err, &derr) {
		t.Fatalf("expected *DeprecatedFieldError, got %T: %v", err, err)
	}
	if derr.Field != "params" || derr.Replacement != "variables" {
		t.Fatalf("unexpected deprecated field error: %v", derr)
	}
}

func TestLoadTemplateDeprecatedCopyField(t *testing.T) {
	dir := writeManifest(t, `{
	  "specVersion": "1.0",
	  "name": "t",
	  "files": [{"path": "a.txt", "source": "a.tmpl", "copy": true}]
	}`)
	_, err := LoadTemplate(dir)
	var derr *DeprecatedFieldError
	if !errors.As(err, &derr) {
		t.Fatalf("expected *DeprecatedFieldError, got %T: %v", err, err)
	}
	if !strings.Contains(derr.Field, "copy") {
		t.Fatalf("unexpected deprecated field: %q", derr.Field)
	}
}

func TestLoadTemplateUnknownFieldRejected(t *testing.T) {
	dir := writeManifest(t, `{
	  "specVersion": "1.0",
	  "name": "t",
	  "bogus": 1,
	  "files": [{"path": "a.txt", "content": "x"}]
	}`)
	_, err := LoadTemplate(dir)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadTemplateMissingManifest(t *testing.T) {
	_, err := LoadTemplate(t.TempDir())
	var werr *WorkspaceError
	if !errors.As(err, &werr) {
		t.Fatalf("expected *WorkspaceError, got %T: %v", err, err)
	}
}
