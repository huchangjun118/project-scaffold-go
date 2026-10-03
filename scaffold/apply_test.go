package scaffold

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyRollbackOnWriteFailure 第二个文件写入失败时，
// 第一个已写入的文件必须被回滚，且不留临时残留。
func TestApplyRollbackOnWriteFailure(t *testing.T) {
	ws := t.TempDir()
	// 目标路径 "zdir" 已存在为目录，rename 必然失败。
	if err := os.Mkdir(filepath.Join(ws, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "a.txt", Content: "created then rolled back"},
			{Path: "sub/nested.txt", Content: "nested"},
			{Path: "zdir", Content: "collides with directory"},
		},
	}
	_, err := New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err == nil {
		t.Fatal("expected write failure")
	}
	// 已写入的文件必须回滚。
	if _, serr := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(serr) {
		t.Fatal("a.txt should have been rolled back")
	}
	if _, serr := os.Stat(filepath.Join(ws, "sub")); !os.IsNotExist(serr) {
		t.Fatal("created directory sub/ should have been rolled back")
	}
	// 不得有临时残留。
	entries, _ := os.ReadDir(ws)
	for _, e := range entries {
		if e.Name() != "zdir" {
			t.Fatalf("unexpected residue in workspace: %s", e.Name())
		}
	}
}

// TestApplyRollbackRestoresOverwrittenFile 覆盖失败时原文件内容必须恢复。
func TestApplyRollbackRestoresOverwrittenFile(t *testing.T) {
	ws := t.TempDir()
	original := []byte("precious user data\n")
	os.WriteFile(filepath.Join(ws, "keep.txt"), original, 0o644)
	os.Mkdir(filepath.Join(ws, "zdir"), 0o755)
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files: []FileRule{
			{Path: "keep.txt", Content: "overwritten"},
			{Path: "zdir", Content: "fails"},
		},
	}
	_, err := New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err == nil {
		t.Fatal("expected failure")
	}
	got, _ := os.ReadFile(filepath.Join(ws, "keep.txt"))
	if string(got) != string(original) {
		t.Fatalf("original file not restored: %q", got)
	}
}

// TestRecoveryAfterInterruption 模拟进程中断：留下事务日志与半成品状态，
// 再次执行必须先回滚到执行前状态，再干净地完成生成。
func TestRecoveryAfterInterruption(t *testing.T) {
	ws := t.TempDir()
	original := []byte("original content\n")
	os.WriteFile(filepath.Join(ws, "exist.txt"), original, 0o644)

	// 构造崩溃现场：日志目录 + 备份 + 半成品文件。
	jdir := filepath.Join(ws, journalDirName)
	os.MkdirAll(filepath.Join(jdir, "backup"), 0o700)
	os.WriteFile(filepath.Join(jdir, "backup", "0"), original, 0o644)
	j := journal{
		Entries: []journalEntry{
			{Path: "exist.txt", Action: "replaced", Backup: "0"},
			{Path: "half.txt", Action: "created"},
		},
		DirsCreated: []string{"halfdir"},
	}
	data, _ := json.Marshal(&j)
	os.WriteFile(filepath.Join(jdir, "journal.json"), data, 0o644)
	os.WriteFile(filepath.Join(ws, "exist.txt"), []byte("corrupted partial write"), 0o644)
	os.WriteFile(filepath.Join(ws, "half.txt"), []byte("half"), 0o644)
	os.Mkdir(filepath.Join(ws, "halfdir"), 0o755)

	// 再次执行：恢复 + 正常生成。
	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files:       []FileRule{{Path: "new.txt", Content: "new file\n"}},
	}
	if _, err := New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("apply after interruption failed: %v", err)
	}
	// 崩溃前的半成品被清理。
	if _, err := os.Stat(filepath.Join(ws, "half.txt")); !os.IsNotExist(err) {
		t.Fatal("half-written file should have been rolled back")
	}
	if _, err := os.Stat(filepath.Join(ws, "halfdir")); !os.IsNotExist(err) {
		t.Fatal("half-created dir should have been rolled back")
	}
	// 被覆盖的文件恢复原内容。
	got, _ := os.ReadFile(filepath.Join(ws, "exist.txt"))
	if string(got) != string(original) {
		t.Fatalf("existing file not recovered: %q", got)
	}
	// 新模板正常落地，日志目录被清理。
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); err != nil {
		t.Fatal("new template file missing")
	}
	if _, err := os.Stat(jdir); !os.IsNotExist(err) {
		t.Fatal("journal dir should be cleaned up")
	}
}

// TestReadOnlyWorkspace 目标目录只读时必须立即失败且工作区不变。
func TestReadOnlyWorkspace(t *testing.T) {
	ws := t.TempDir()
	os.Chmod(ws, 0o555)
	defer os.Chmod(ws, 0o755)

	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files:       []FileRule{{Path: "a.txt", Content: "x"}},
	}
	_, err := New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err == nil {
		t.Fatal("expected read-only error")
	}
	var werr *WorkspaceError
	if !errors.As(err, &werr) {
		t.Fatalf("expected *WorkspaceError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error should mention read-only: %q", err.Error())
	}
	entries, _ := os.ReadDir(ws)
	if len(entries) != 0 {
		t.Fatalf("workspace should be unchanged, found %d entries", len(entries))
	}
}

// TestReadOnlyParentDir 父目录不可写（子目录已存在为只读）时立即失败。
func TestReadOnlyParentDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "proj")
	os.Mkdir(sub, 0o755)
	os.Mkdir(filepath.Join(sub, "locked"), 0o555)
	defer os.Chmod(filepath.Join(sub, "locked"), 0o755)

	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files:       []FileRule{{Path: "locked/deep/a.txt", Content: "x"}},
	}
	_, err := New().Apply(tmpl, Options{Workspace: sub, OnConflict: ConflictOverwrite})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only error, got %v", err)
	}
	// locked 目录内不得有任何写入。
	entries, _ := os.ReadDir(filepath.Join(sub, "locked"))
	if len(entries) != 0 {
		t.Fatalf("read-only dir should be untouched, found %d entries", len(entries))
	}
}

// TestInsufficientDiskSpace 磁盘空间不足时立即失败且不产生写入。
func TestInsufficientDiskSpace(t *testing.T) {
	ws := t.TempDir()
	orig := availableBytes
	availableBytes = func(string) (uint64, error) { return 16, nil }
	defer func() { availableBytes = orig }()

	tmpl := &Template{
		SpecVersion: "1.0",
		Name:        "t",
		Files:       []FileRule{{Path: "big.txt", Content: strings.Repeat("x", 1<<20)}},
	}
	_, err := New().Apply(tmpl, Options{Workspace: ws, OnConflict: ConflictOverwrite})
	if err == nil || !strings.Contains(err.Error(), "insufficient disk space") {
		t.Fatalf("expected disk space error, got %v", err)
	}
	entries, _ := os.ReadDir(ws)
	if len(entries) != 0 {
		t.Fatalf("workspace should be unchanged, found %d entries", len(entries))
	}
}
