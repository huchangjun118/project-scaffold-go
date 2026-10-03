package scaffold

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// backupSuffix marks a file backed up during a transaction. A leftover
// backup means a previous run was interrupted; recovery restores or commits
// it.
const backupSuffix = ".scaffold-bak"

// stagingPrefix marks the directory where generated content is staged before
// the atomic apply phase.
const stagingPrefix = ".scaffold-staging-"

// Options controls a generation run.
type Options struct {
	// Workspace is the target directory (created if missing).
	Workspace string
	// TemplateDir is the directory containing template.json and files.
	TemplateDir string
	// Variables are caller-supplied values keyed by variable name.
	Variables map[string]any
	// Conflict is the default strategy for existing files:
	// skip, overwrite or merge.
	Conflict string
}

// Generate plans and executes a template generation into the workspace.
// The plan phase performs no writes; the execute phase is transactional:
// any failure rolls the workspace back to its pre-generation state.
func Generate(opts Options) (*Plan, error) {
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("scaffold: cannot resolve workspace path %s: %w", opts.Workspace, err)
	}

	lock, err := acquireLock(workspace)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	// Recover leftovers from a previously interrupted run before doing
	// anything else.
	if err := recoverWorkspace(workspace); err != nil {
		return nil, &GenerationError{Phase: "recovery", Err: err}
	}

	tmpl, err := LoadTemplate(opts.TemplateDir)
	if err != nil {
		return nil, &GenerationError{Phase: "plan", Err: err}
	}

	plan, err := planGeneration(workspace, tmpl, opts.Variables, opts.Conflict)
	if err != nil {
		return nil, &GenerationError{Phase: "plan", Err: err}
	}

	if err := executePlan(plan); err != nil {
		return plan, &GenerationError{Phase: "execute", Err: err}
	}
	return plan, nil
}

// executePlan applies a plan transactionally.
func executePlan(plan *Plan) error {
	if err := preflight(plan); err != nil {
		return err
	}

	staging, err := stageFiles(plan)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	applied, createdDirs, err := applyFiles(plan, staging)
	if err != nil {
		rollback(plan.Workspace, applied, createdDirs)
		return err
	}

	// Commit: remove backups of overwritten/merged files.
	for _, op := range applied {
		if op.backup != "" {
			if err := os.Remove(op.backup); err != nil && !os.IsNotExist(err) {
				// Backup removal failure is not fatal for the workspace
				// state (the new file is in place); report it.
				return fmt.Errorf("scaffold: cannot remove backup %s: %w", op.backup, err)
			}
		}
	}
	return nil
}

// preflight verifies writability and free disk space before any write.
func preflight(plan *Plan) error {
	probed := map[string]bool{}
	for _, e := range plan.Entries {
		if e.Op == "skip" {
			continue
		}
		if err := checkPathBlocked(plan.Workspace, e.Target); err != nil {
			return err
		}
		parent := filepath.Dir(filepath.Join(plan.Workspace, filepath.FromSlash(e.Target)))
		ancestor := existingAncestor(parent)
		if !probed[ancestor] {
			if err := probeWritable(ancestor); err != nil {
				return err
			}
			probed[ancestor] = true
		}
	}
	if err := diskSpaceCheck(plan.Workspace, plan.TotalBytes()); err != nil {
		return err
	}
	return nil
}

// stageFiles writes every planned file into a staging directory inside the
// workspace (same filesystem, so the final rename is atomic).
func stageFiles(plan *Plan) (string, error) {
	staging := filepath.Join(plan.Workspace, stagingPrefix+randomSuffix())
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", fmt.Errorf("%w: cannot create staging directory %s: %v", ErrPermission, staging, err)
	}
	for _, e := range plan.Entries {
		if e.Op == "skip" {
			continue
		}
		dst := filepath.Join(staging, filepath.FromSlash(e.Target))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("%w: cannot create directory for %s: %v", ErrPermission, e.Target, err)
		}
		if err := os.WriteFile(dst, e.Bytes, 0o644); err != nil {
			return "", fmt.Errorf("scaffold: cannot stage %s: %w", e.Target, err)
		}
	}
	return staging, nil
}

// appliedOp records one applied file for rollback.
type appliedOp struct {
	target string // absolute target path
	backup string // absolute backup path ("" if the file was newly created)
}

// applyFiles moves staged files into the workspace, backing up existing
// files first. On error it returns what was applied so the caller can roll
// back.
func applyFiles(plan *Plan, staging string) ([]appliedOp, []string, error) {
	var applied []appliedOp
	var createdDirs []string

	for _, e := range plan.Entries {
		if e.Op == "skip" {
			continue
		}
		target := filepath.Join(plan.Workspace, filepath.FromSlash(e.Target))
		staged := filepath.Join(staging, filepath.FromSlash(e.Target))

		created, err := ensureParentDirs(target)
		if err != nil {
			return applied, createdDirs, err
		}
		createdDirs = append(createdDirs, created...)

		op := appliedOp{target: target}
		if _, err := os.Lstat(target); err == nil {
			backup := target + backupSuffix
			if err := os.Rename(target, backup); err != nil {
				return applied, createdDirs, fmt.Errorf("scaffold: cannot back up %s: %w", e.Target, err)
			}
			op.backup = backup
		} else if !os.IsNotExist(err) {
			return applied, createdDirs, fmt.Errorf("scaffold: cannot stat %s: %w", e.Target, err)
		}

		if beforeApplyHook != nil {
			if err := beforeApplyHook(target); err != nil {
				if op.backup != "" {
					_ = os.Rename(op.backup, target)
				}
				return applied, createdDirs, err
			}
		}
		if err := os.Rename(staged, target); err != nil {
			// Restore the backup before reporting failure.
			if op.backup != "" {
				_ = os.Rename(op.backup, target)
			}
			return applied, createdDirs, fmt.Errorf("scaffold: cannot write %s: %w", e.Target, err)
		}
		applied = append(applied, op)
	}
	return applied, createdDirs, nil
}

// rollback restores the workspace to its pre-generation state.
func rollback(workspace string, applied []appliedOp, createdDirs []string) {
	for i := len(applied) - 1; i >= 0; i-- {
		op := applied[i]
		if op.backup != "" {
			if _, err := os.Lstat(op.target); err == nil {
				_ = os.Remove(op.target)
			}
			_ = os.Rename(op.backup, op.target)
		} else {
			_ = os.Remove(op.target)
		}
	}
	for i := len(createdDirs) - 1; i >= 0; i-- {
		_ = os.Remove(createdDirs[i]) // rmdir: fails silently if non-empty
	}
}

// recoverWorkspace rolls back or commits backups left by an interrupted
// run, and removes leftover staging/probe directories.
func recoverWorkspace(workspace string) error {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scaffold: cannot read workspace %s for recovery: %w", workspace, err)
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, backupSuffix):
			backup := filepath.Join(workspace, name)
			target := filepath.Join(workspace, strings.TrimSuffix(name, backupSuffix))
			if _, err := os.Lstat(target); err == nil {
				// New file is in place: commit by removing the backup.
				if err := os.Remove(backup); err != nil {
					return fmt.Errorf("scaffold: cannot clean leftover backup %s: %w", backup, err)
				}
			} else {
				// New file never landed: roll back by restoring the backup.
				if err := os.Rename(backup, target); err != nil {
					return fmt.Errorf("scaffold: cannot restore %s from backup: %w", target, err)
				}
			}
		case strings.HasPrefix(name, stagingPrefix):
			if err := os.RemoveAll(filepath.Join(workspace, name)); err != nil {
				return fmt.Errorf("scaffold: cannot clean leftover staging directory %s: %w", name, err)
			}
		}
	}
	return nil
}

// ensureParentDirs creates parent directories of target, returning the
// absolute paths that were newly created (deepest first).
func ensureParentDirs(target string) ([]string, error) {
	parent := filepath.Dir(target)
	var created []string
	dir := parent
	for {
		info, err := os.Lstat(dir)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("scaffold: %s is not a directory", dir)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		created = append([]string{dir}, created...)
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			break
		}
		dir = parentDir
	}
	if len(created) > 0 {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return nil, err
		}
	}
	return created, nil
}

// existingAncestor returns the nearest existing ancestor directory of dir.
func existingAncestor(dir string) string {
	for {
		info, err := os.Lstat(dir)
		if err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// checkPathBlocked fails when a path component of target exists but is not
// a directory (e.g. a regular file where a directory is needed).
func checkPathBlocked(workspace, target string) error {
	parts := strings.Split(filepath.ToSlash(target), "/")
	// Only parent components may be blocked; the final component is the
	// target file itself (an existing file is handled by backup/overwrite).
	parents := parts[:len(parts)-1]
	cur := workspace
	for _, p := range parents {
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if err == nil && !info.IsDir() {
			return fmt.Errorf("scaffold: cannot write %s: %s is not a directory", target, cur)
		}
	}
	return nil
}

// probeWritable verifies a directory is writable by creating and removing
// a temporary file.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".scaffold-probe-*")
	if err != nil {
		return fmt.Errorf("%w: directory %s is not writable: %v", ErrPermission, dir, err)
	}
	name := f.Name()
	if _, err := f.Write([]byte(".")); err != nil {
		f.Close()
		os.Remove(name)
		return fmt.Errorf("%w: cannot write to %s: %v", ErrPermission, dir, err)
	}
	f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("scaffold: cannot clean probe file in %s: %w", dir, err)
	}
	return nil
}

// diskSpaceCheck verifies the workspace has enough free space. It is a
// package variable so tests can simulate resource exhaustion.
var diskSpaceCheck = func(workspace string, needed int64) error {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(workspace, &fs); err != nil {
		return fmt.Errorf("scaffold: cannot determine free disk space for %s: %w", workspace, err)
	}
	avail := int64(fs.Bavail) * int64(fs.Bsize)
	if avail < needed {
		return fmt.Errorf("%w: need %d bytes but only %d bytes available in %s",
			ErrInsufficientSpace, needed, avail, workspace)
	}
	return nil
}

// randomSuffix returns a short random hex string for staging directories.
func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// Plan runs only the plan phase: it performs all validation, dependency
// resolution and rendering, and lists the operations that Generate would
// perform. It performs reads only and never writes to the workspace.
func PlanGeneration(opts Options) (*Plan, error) {
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("scaffold: cannot resolve workspace path %s: %w", opts.Workspace, err)
	}
	tmpl, err := LoadTemplate(opts.TemplateDir)
	if err != nil {
		return nil, &GenerationError{Phase: "plan", Err: err}
	}
	plan, err := planGeneration(workspace, tmpl, opts.Variables, opts.Conflict)
	if err != nil {
		return nil, &GenerationError{Phase: "plan", Err: err}
	}
	return plan, nil
}

// beforeApplyHook, when set, is invoked immediately before each staged file
// is moved into the workspace. Tests may set it to simulate a write failure
// after earlier files have already been applied.
var beforeApplyHook func(target string) error
