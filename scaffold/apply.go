package scaffold

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// journalDirName 是工作区内事务日志目录名。
const journalDirName = ".scaffold.journal"

// journalEntry 记录单个已执行操作的回滚信息。
type journalEntry struct {
	Path   string `json:"path"`             // 相对工作区路径
	Action string `json:"action"`           // "created" 或 "replaced"
	Backup string `json:"backup,omitempty"` // 日志目录内备份文件名
}

// journal 是事务日志，用于失败回滚与崩溃恢复。
type journal struct {
	Entries     []journalEntry `json:"entries"`
	DirsCreated []string       `json:"dirsCreated"`
	RootCreated bool           `json:"rootCreated"`
}

// applyPlan 以事务方式执行计划：任一文件写入失败则全部回滚；
// 进程中断后可通过日志恢复，不得留下半成品文件或临时残留。
func applyPlan(workspace string, plan *Plan) error {
	// 先恢复上一次可能被中断的事务，保证工作区干净。
	if err := recoverIfNeeded(workspace); err != nil {
		return err
	}

	// 全部跳过：不做任何写入，保证幂等执行无副作用。
	writes := make([]*PlannedFile, 0, len(plan.Files))
	for _, pf := range plan.Files {
		if pf.Op != OpSkip {
			writes = append(writes, pf)
		}
	}
	if len(writes) == 0 {
		return nil
	}

	// 预检：权限与磁盘空间，任何一项不满足都在写入前失败。
	if err := preflight(workspace, writes); err != nil {
		return err
	}

	j := &journal{}
	jdir := filepath.Join(workspace, journalDirName)
	bdir := filepath.Join(jdir, "backup")

	rollback := func(err error) error {
		if rerr := rollbackJournal(workspace, jdir, j); rerr != nil {
			return fmt.Errorf("%w (additionally, rollback failed: %v)", err, rerr)
		}
		return err
	}

	// 工作区根目录本身可能由本次事务创建。
	rootCreated := false
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return &WorkspaceError{Op: "create workspace", Path: workspace, Err: err}
		}
		rootCreated = true
		j.RootCreated = true
	}

	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return rollback(&WorkspaceError{Op: "create journal dir", Path: bdir, Err: err})
	}
	if err := writeJournal(jdir, j); err != nil {
		return rollback(err)
	}

	for i, pf := range writes {
		abs := filepath.Join(workspace, filepath.FromSlash(pf.Path))

		// 记录并创建缺失的父目录。
		missing := missingDirs(workspace, filepath.Dir(abs))
		for _, d := range missing {
			if err := os.Mkdir(d, 0o755); err != nil {
				return rollback(&WorkspaceError{Op: "create directory", Path: d, Err: err})
			}
			rel, _ := filepath.Rel(workspace, d)
			j.DirsCreated = append(j.DirsCreated, rel)
		}

		entry := journalEntry{Path: pf.Path}
		if _, err := os.Stat(abs); err == nil {
			// 覆盖/合并前先备份原文件。
			backupName := fmt.Sprintf("%d", i)
			if err := copyFile(abs, filepath.Join(bdir, backupName)); err != nil {
				return rollback(&WorkspaceError{Op: "backup existing file", Path: abs, Err: err})
			}
			entry.Action = "replaced"
			entry.Backup = backupName
		} else {
			entry.Action = "created"
		}
		j.Entries = append(j.Entries, entry)
		if err := writeJournal(jdir, j); err != nil {
			return rollback(err)
		}

		// 先写临时文件并 fsync，再原子 rename 到目标位置。
		if err := writeFileAtomic(abs, pf.Content); err != nil {
			return rollback(&WorkspaceError{Op: "write file", Path: abs, Err: err})
		}
	}

	// 全部成功：清理日志目录；若本次创建了工作区根则保留根目录。
	if err := os.RemoveAll(jdir); err != nil {
		return &WorkspaceError{Op: "clean up journal", Path: jdir, Err: err}
	}
	_ = rootCreated
	return nil
}

// preflight 在写入前检查权限与磁盘空间。
func preflight(workspace string, writes []*PlannedFile) error {
	// 检查工作区根（或已存在的最近祖先）可写。
	rootToCheck := workspace
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		rootToCheck = deepestExistingAncestor(workspace)
		if rootToCheck == "" {
			return &WorkspaceError{Op: "check workspace", Path: workspace,
				Err: fmt.Errorf("no existing ancestor directory found")}
		}
	}
	if err := checkDirWritable(rootToCheck); err != nil {
		return err
	}

	// 检查每个目标文件已存在的最近祖先目录可写。
	checked := map[string]bool{rootToCheck: true}
	for _, pf := range writes {
		abs := filepath.Join(workspace, filepath.FromSlash(pf.Path))
		ancestor := deepestExistingAncestor(filepath.Dir(abs))
		if ancestor == "" || checked[ancestor] {
			continue
		}
		checked[ancestor] = true
		if err := checkDirWritable(ancestor); err != nil {
			return err
		}
	}

	// 检查磁盘空间是否足够。
	var need uint64
	for _, pf := range writes {
		need += uint64(len(pf.Content)) + 4096
	}
	avail, err := availableBytes(rootToCheck)
	if err != nil {
		return &WorkspaceError{Op: "check disk space", Path: rootToCheck, Err: err}
	}
	if avail < need {
		return &WorkspaceError{Op: "check disk space", Path: workspace,
			Err: fmt.Errorf("insufficient disk space: need %d bytes, only %d available", need, avail)}
	}
	return nil
}

// checkDirWritable 检查目录存在且带有写权限位。
func checkDirWritable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return &WorkspaceError{Op: "stat directory", Path: dir, Err: err}
	}
	if !info.IsDir() {
		return &WorkspaceError{Op: "check directory", Path: dir,
			Err: fmt.Errorf("not a directory")}
	}
	if info.Mode().Perm()&0o222 == 0 {
		return &WorkspaceError{Op: "check directory writable", Path: dir,
			Err: fmt.Errorf("directory is read-only")}
	}
	return nil
}

// deepestExistingAncestor 返回路径本身或最近的已存在祖先。
func deepestExistingAncestor(p string) string {
	cur := p
	for {
		if _, err := os.Stat(cur); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// availableBytes 返回路径所在文件系统的可用字节数。
// 声明为变量以便测试替换。
var availableBytes = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// missingDirs 列出从 workspace 到 dir 之间尚不存在的目录（自顶向下）。
func missingDirs(workspace, dir string) []string {
	var missing []string
	cur := dir
	for {
		rel, err := filepath.Rel(workspace, cur)
		if err != nil || rel == "." || rel == ".." || len(rel) > 2 && rel[:3] == "../" {
			break
		}
		if _, err := os.Stat(cur); err == nil {
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// 自顶向下排序。
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// writeJournal 把事务日志落盘并 fsync。
func writeJournal(jdir string, j *journal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(jdir, "journal.json"), data)
}

// writeFileAtomic 先写临时文件并 fsync，再原子 rename。
func writeFileAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".scaffold-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// copyFile 复制文件内容（用于备份）。
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data)
}

// recoverIfNeeded 检测并回滚上一次被中断的事务。
func recoverIfNeeded(workspace string) error {
	jdir := filepath.Join(workspace, journalDirName)
	data, err := os.ReadFile(filepath.Join(jdir, "journal.json"))
	if os.IsNotExist(err) {
		// 可能日志目录刚创建就崩溃了。
		if _, serr := os.Stat(jdir); serr == nil {
			return os.RemoveAll(jdir)
		}
		return nil
	}
	if err != nil {
		return &WorkspaceError{Op: "read journal", Path: jdir, Err: err}
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return &WorkspaceError{Op: "parse journal", Path: jdir, Err: err}
	}
	return rollbackJournal(workspace, jdir, &j)
}

// rollbackJournal 按日志逆序回滚：删除新建文件、恢复被覆盖文件、
// 清理新建目录与日志目录。
func rollbackJournal(workspace, jdir string, j *journal) error {
	var errs []error
	for i := len(j.Entries) - 1; i >= 0; i-- {
		e := j.Entries[i]
		abs := filepath.Join(workspace, filepath.FromSlash(e.Path))
		switch e.Action {
		case "created":
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		case "replaced":
			backup := filepath.Join(jdir, "backup", e.Backup)
			data, err := os.ReadFile(backup)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if err := writeFileAtomic(abs, data); err != nil {
				errs = append(errs, err)
			}
		}
	}
	// 逆序删除新建目录（子目录先删）。
	dirs := append([]string{}, j.DirsCreated...)
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		abs := filepath.Join(workspace, filepath.FromSlash(d))
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if err := os.RemoveAll(jdir); err != nil {
		errs = append(errs, err)
	}
	if j.RootCreated {
		if err := os.Remove(workspace); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("rollback incomplete: %v", errs)
	}
	return nil
}
