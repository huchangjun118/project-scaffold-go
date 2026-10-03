package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const basicTemplate = "testdata/basic"
const mergeTemplate = "testdata/merge"

// ---------- helpers ----------

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// snapshot returns a sorted list of relative file paths and their contents.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot walk: %v", err)
	}
	return out
}

func assertNoLeftovers(t *testing.T, ws string) {
	t.Helper()
	err := filepath.Walk(ws, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if strings.HasSuffix(name, backupSuffix) || strings.HasPrefix(name, stagingPrefix) {
			return fmt.Errorf("leftover transaction artifact: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// ---------- variable validation ----------

func TestVariableValidation_AllErrorsReported(t *testing.T) {
	tmpl, err := LoadTemplate(basicTemplate)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	values := map[string]any{
		"name":   "BadName123", // fails pattern
		"count":  100,          // exceeds max
		"color":  "purple",     // not in enum
		"items":  []any{1, "ok"}, // item 0 has wrong type
		"flag":   "yes",        // wrong type
		"extra":  1,            // undeclared variable
	}
	_, err = validateVariables(tmpl.Manifest.Variables, values)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	got := map[string]bool{}
	for _, p := range ve.Problems {
		got[p.Variable] = true
	}
	for _, want := range []string{"name", "count", "color", "items", "flag", "extra"} {
		if !got[want] {
			t.Errorf("expected problem for variable %q, got problems: %v", want, ve.Problems)
		}
	}
}

func TestVariableValidation_MissingRequired(t *testing.T) {
	tmpl, err := LoadTemplate(basicTemplate)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	_, err = validateVariables(tmpl.Manifest.Variables, map[string]any{})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	found := false
	for _, p := range ve.Problems {
		if p.Variable == "name" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing-required problem for name, got %v", ve.Problems)
	}
}

func TestVariableValidation_DefaultsAndCoercion(t *testing.T) {
	tmpl, err := LoadTemplate(basicTemplate)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	out, err := validateVariables(tmpl.Manifest.Variables, map[string]any{"name": "demo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["count"].(int64) != 1 {
		t.Errorf("count default = %v, want 1", out["count"])
	}
	if out["color"] != "red" {
		t.Errorf("color default = %v, want red", out["color"])
	}
	if out["flag"] != true {
		t.Errorf("flag default = %v, want true", out["flag"])
	}
	if len(out["items"].([]any)) != 0 {
		t.Errorf("items default = %v, want empty list", out["items"])
	}
}

// ---------- dependency graph ----------

func TestDependencyCycle(t *testing.T) {
	tmpl, err := LoadTemplate("testdata/cycle")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	_, err = resolveDependencies(tmpl.Manifest.Files, func(source string) ([]byte, error) {
		return os.ReadFile(filepath.Join(tmpl.Dir, source))
	})
	if !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("expected ErrCyclicDependency, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"files/a.tmpl", "files/b.tmpl", "files/c.tmpl"} {
		if !strings.Contains(msg, want) {
			t.Errorf("cycle error %q does not contain %q", msg, want)
		}
	}
	t.Logf("cycle error: %s", msg)
}

func TestMissingDependency(t *testing.T) {
	tmpl, err := LoadTemplate("testdata/missing-dep")
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	_, err = resolveDependencies(tmpl.Manifest.Files, func(source string) ([]byte, error) {
		return os.ReadFile(filepath.Join(tmpl.Dir, source))
	})
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("expected ErrMissingDependency, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing dependency, got %v", err)
	}
}

func TestDependencyOrdering(t *testing.T) {
	tmpl, err := LoadTemplate(basicTemplate)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	order, err := resolveDependencies(tmpl.Manifest.Files, func(source string) ([]byte, error) {
		return os.ReadFile(filepath.Join(tmpl.Dir, source))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// a must come before b (b depends on a).
	pos := map[string]int{}
	for i, f := range order {
		pos[f.ID] = i
	}
	if pos["a"] >= pos["b"] {
		t.Errorf("a should be ordered before b, got positions %d and %d", pos["a"], pos["b"])
	}
}

// ---------- conflict strategies ----------

func TestConflictStrategies(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		ws := t.TempDir()
		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		writeFile(t, filepath.Join(ws, "out", "demo.txt"), "user content")

		plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "skip"})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.Entries[0].Op != "skip" {
			t.Errorf("op = %s, want skip", plan.Entries[0].Op)
		}
		_, err = Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "skip"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if got := readFile(t, filepath.Join(ws, "out", "demo.txt")); got != "user content" {
			t.Errorf("file changed under skip strategy: %q", got)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		ws := t.TempDir()
		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		writeFile(t, filepath.Join(ws, "out", "demo.txt"), "user content")

		_, err = Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if got := readFile(t, filepath.Join(ws, "out", "demo.txt")); strings.Contains(got, "user content") {
			t.Errorf("file was not overwritten: %q", got)
		}
		// idempotent: another run plans skip
		plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		for _, e := range plan.Entries {
			if e.Op != "skip" {
				t.Errorf("expected idempotent skip, got %s for %s", e.Op, e.Target)
			}
		}
	})

	t.Run("merge append", func(t *testing.T) {
		ws := t.TempDir()
		_, err := Generate(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		writeFile(t, filepath.Join(ws, "config.ini"), "user = kept\n")

		plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.Entries[0].Op != "merge" {
			t.Errorf("op = %s, want merge", plan.Entries[0].Op)
		}
		if !strings.Contains(string(plan.Entries[0].Bytes), "user = kept") {
			t.Errorf("merged content missing user line: %q", plan.Entries[0].Bytes)
		}
		if !strings.Contains(string(plan.Entries[0].Bytes), "port = 8080") {
			t.Errorf("merged content missing template line: %q", plan.Entries[0].Bytes)
		}
	})

	t.Run("merge no-op is idempotent", func(t *testing.T) {
		ws := t.TempDir()
		_, err := Generate(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.Entries[0].Op != "skip" {
			t.Errorf("op = %s, want skip (idempotent)", plan.Entries[0].Op)
		}
	})

	t.Run("merge conflict leaves original untouched", func(t *testing.T) {
		ws := t.TempDir()
		_, err := Generate(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		original := "port = 9090\n"
		writeFile(t, filepath.Join(ws, "config.ini"), original)

		_, err = Generate(Options{Workspace: ws, TemplateDir: mergeTemplate, Conflict: "merge"})
		if !errors.Is(err, ErrMergeConflict) {
			t.Fatalf("expected ErrMergeConflict, got %v", err)
		}
		if got := readFile(t, filepath.Join(ws, "config.ini")); got != original {
			t.Errorf("original file was modified after merge failure: %q", got)
		}
		assertNoLeftovers(t, ws)
	})
}

// ---------- transaction rollback ----------

func TestTransactionRollback_NewFiles(t *testing.T) {
	ws := t.TempDir()
	calls := 0
	beforeApplyHook = func(target string) error {
		calls++
		if strings.HasSuffix(target, "b.txt") {
			return fmt.Errorf("simulated write failure")
		}
		return nil
	}
	defer func() { beforeApplyHook = nil }()

	_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
		Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
	if err == nil {
		t.Fatal("expected simulated failure")
	}
	if calls < 2 {
		t.Fatalf("hook called %d times, want at least 2", calls)
	}
	// Workspace must be exactly as before: empty.
	if files := snapshot(t, ws); len(files) != 0 {
		t.Errorf("workspace not rolled back, leftover files: %v", files)
	}
	assertNoLeftovers(t, ws)
}

func TestTransactionRollback_ExistingFiles(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "out", "b.txt"), "original")

	beforeApplyHook = func(target string) error {
		if strings.HasSuffix(target, "b.txt") {
			return fmt.Errorf("simulated write failure")
		}
		return nil
	}
	defer func() { beforeApplyHook = nil }()

	_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
		Variables: map[string]any{"name": "demo"}, Conflict: "overwrite"})
	if err == nil {
		t.Fatal("expected simulated failure")
	}
	// Pre-existing file must be restored byte-for-byte; new file removed.
	if got := readFile(t, filepath.Join(ws, "out", "b.txt")); got != "original" {
		t.Errorf("b.txt not restored: %q", got)
	}
	if fileExists(filepath.Join(ws, "out", "demo.txt")) {
		t.Errorf("demo.txt should have been removed on rollback")
	}
	assertNoLeftovers(t, ws)
}

// ---------- crash recovery ----------

func TestCrashRecovery(t *testing.T) {
	t.Run("backup without target restores file", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, "orphan.txt.scaffold-bak"), "restored content")

		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate after recovery: %v", err)
		}
		if got := readFile(t, filepath.Join(ws, "orphan.txt")); got != "restored content" {
			t.Errorf("orphan.txt = %q, want restored content", got)
		}
		assertNoLeftovers(t, ws)
	})

	t.Run("backup with target commits new file", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, "g.txt"), "new content")
		writeFile(t, filepath.Join(ws, "g.txt.scaffold-bak"), "old content")

		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate after recovery: %v", err)
		}
		if got := readFile(t, filepath.Join(ws, "g.txt")); got != "new content" {
			t.Errorf("g.txt = %q, want new content", got)
		}
		assertNoLeftovers(t, ws)
	})

	t.Run("staging directory removed", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, ".scaffold-staging-deadbeef", "junk"), "x")

		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		if err != nil {
			t.Fatalf("generate after recovery: %v", err)
		}
		assertNoLeftovers(t, ws)
	})
}

// ---------- concurrent generation ----------

func TestConcurrentGeneration(t *testing.T) {
	ws := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	beforeApplyHook = func(target string) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}
	defer func() { beforeApplyHook = nil }()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		<-started
		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		errCh <- err
	}()

	<-started
	time.Sleep(200 * time.Millisecond) // let the second goroutine attempt the lock
	close(release)
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != 1 {
		t.Fatalf("expected exactly one non-nil error, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrWorkspaceLocked) {
		t.Fatalf("expected ErrWorkspaceLocked, got %v", errs[0])
	}
	assertNoLeftovers(t, ws)
	if !fileExists(filepath.Join(ws, "out", "demo.txt")) {
		t.Errorf("workspace was not generated")
	}
}

// ---------- permission / resource errors ----------

func TestPermissionErrors(t *testing.T) {
	t.Run("path blocked by file (ENOTDIR)", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, "blocker"), "x")
		tpl := t.TempDir()
		writeFile(t, filepath.Join(tpl, "template.json"), `{
			"specVersion": "1",
			"files": [{"source": "f.tmpl", "target": "blocker/sub/f.txt"}]
		}`)
		writeFile(t, filepath.Join(tpl, "f.tmpl"), "content")

		before := snapshot(t, ws)
		_, err := Generate(Options{Workspace: ws, TemplateDir: tpl, Conflict: "merge"})
		if err == nil {
			t.Fatal("expected error for blocked path")
		}
		if !errors.Is(err, ErrPermission) && !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("expected permission/not-a-directory error, got %v", err)
		}
		if after := snapshot(t, ws); !equalSnapshots(before, after) {
			t.Errorf("workspace changed after failed generation: before=%v after=%v", before, after)
		}
	})

	t.Run("insufficient disk space", func(t *testing.T) {
		ws := t.TempDir()
		orig := diskSpaceCheck
		diskSpaceCheck = func(workspace string, needed int64) error {
			return fmt.Errorf("%w: simulated shortage (need %d bytes)", ErrInsufficientSpace, needed)
		}
		defer func() { diskSpaceCheck = orig }()

		before := snapshot(t, ws)
		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		if !errors.Is(err, ErrInsufficientSpace) {
			t.Fatalf("expected ErrInsufficientSpace, got %v", err)
		}
		if after := snapshot(t, ws); !equalSnapshots(before, after) {
			t.Errorf("workspace changed after failed generation: before=%v after=%v", before, after)
		}
	})

	t.Run("read-only workspace", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission checks are bypassed")
		}
		ws := t.TempDir()
		if err := os.Chmod(ws, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		defer os.Chmod(ws, 0o755)

		_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
			Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
		if !errors.Is(err, ErrPermission) {
			t.Fatalf("expected ErrPermission, got %v", err)
		}
	})
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ---------- version compatibility ----------

func TestUnsupportedSpecVersion(t *testing.T) {
	_, err := LoadTemplate("testdata/v2")
	if !errors.Is(err, ErrUnsupportedSpecVersion) {
		t.Fatalf("expected ErrUnsupportedSpecVersion, got %v", err)
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("error should mention the declared version, got %v", err)
	}
}

func TestDeprecatedFieldsMapped(t *testing.T) {
	tmpl, err := LoadTemplate("testdata/deprecated")
	if err != nil {
		t.Fatalf("deprecated fields should be mapped, not rejected: %v", err)
	}
	if len(tmpl.Warnings) == 0 {
		t.Errorf("expected deprecation warnings, got none")
	}
	joined := strings.Join(tmpl.Warnings, "; ")
	for _, field := range []string{"version", "vars"} {
		if !strings.Contains(joined, field) {
			t.Errorf("warning should mention deprecated field %q, got %q", field, joined)
		}
	}
	// The mapped values must actually work.
	plan, err := PlanGeneration(Options{Workspace: t.TempDir(), TemplateDir: "testdata/deprecated",
		Variables: map[string]any{}, Conflict: "merge"})
	if err != nil {
		t.Fatalf("plan with deprecated fields: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(plan.Entries))
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := LoadTemplate("testdata/unknown-field")
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("expected ErrUnknownField, got %v", err)
	}
}

// ---------- plan phase writes nothing ----------

func TestPlanWritesNothing(t *testing.T) {
	ws := t.TempDir()
	before := snapshot(t, ws)
	plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: basicTemplate,
		Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Entries) == 0 {
		t.Fatal("plan should have entries")
	}
	if after := snapshot(t, ws); !equalSnapshots(before, after) {
		t.Errorf("plan phase wrote to the workspace: before=%v after=%v", before, after)
	}
}

// ---------- condition and each ----------

func TestConditionAndEach(t *testing.T) {
	ws := t.TempDir()
	_, err := Generate(Options{Workspace: ws, TemplateDir: basicTemplate,
		Variables: map[string]any{"name": "demo", "flag": false, "items": []string{"x", "y"}},
		Conflict: "merge"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if fileExists(filepath.Join(ws, "out", "c.txt")) {
		t.Errorf("conditional file c.txt should not exist when flag=false")
	}
	if !fileExists(filepath.Join(ws, "out", "items", "x.txt")) {
		t.Errorf("items/x.txt missing")
	}
	if !fileExists(filepath.Join(ws, "out", "items", "y.txt")) {
		t.Errorf("items/y.txt missing")
	}
	got := readFile(t, filepath.Join(ws, "out", "items", "x.txt"))
	if !strings.Contains(got, "item=x") || !strings.Contains(got, "index=0") {
		t.Errorf("each context wrong: %q", got)
	}
}

// ---------- plan contents ----------

func TestPlanContents(t *testing.T) {
	ws := t.TempDir()
	plan, err := PlanGeneration(Options{Workspace: ws, TemplateDir: basicTemplate,
		Variables: map[string]any{"name": "demo"}, Conflict: "merge"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var targets []string
	for _, e := range plan.Entries {
		targets = append(targets, e.Target)
	}
	sort.Strings(targets)
	want := []string{"out/b.txt", "out/c.txt", "out/demo.txt"}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("target %d = %q, want %q", i, targets[i], want[i])
		}
	}
	if plan.Counts()["create"] != 3 {
		t.Errorf("create count = %d, want 3", plan.Counts()["create"])
	}
}

// ---------- render errors ----------

func TestMissingKeyFailsFast(t *testing.T) {
	ws := t.TempDir()
	_, err := PlanGeneration(Options{Workspace: ws, TemplateDir: "testdata/bad-key",
		Variables: map[string]any{}, Conflict: "merge"})
	if err == nil {
		t.Fatal("expected error for template referencing undefined variable")
	}
	if !strings.Contains(err.Error(), "undefinedVar") {
		t.Errorf("error should name the undefined variable, got %v", err)
	}
}
