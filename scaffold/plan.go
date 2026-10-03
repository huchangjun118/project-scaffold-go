package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Op 是计划中对单个文件的操作。
type Op string

const (
	OpCreate    Op = "create"
	OpOverwrite Op = "overwrite"
	OpSkip      Op = "skip"
	OpMerge     Op = "merge"
)

// PlannedFile 描述计划阶段对单个目标文件的决定。
type PlannedFile struct {
	Path    string // 相对工作区的路径
	Op      Op
	Content []byte // 将要写入的最终内容（merge 时为合并结果）
}

// Plan 是生成前的完整计划，不触碰工作区。
type Plan struct {
	Files []*PlannedFile
}

// ConflictStrategy 决定目标文件已存在时的行为。
type ConflictStrategy string

const (
	ConflictSkip      ConflictStrategy = "skip"
	ConflictOverwrite ConflictStrategy = "overwrite"
	ConflictMerge     ConflictStrategy = "merge"
)

// buildPlan 由渲染结果与工作区现状计算计划，不做任何写入。
func buildPlan(workspace string, files []*renderedFile, strategy ConflictStrategy) (*Plan, error) {
	switch strategy {
	case ConflictSkip, ConflictOverwrite, ConflictMerge:
	default:
		return nil, fmt.Errorf("unknown conflict strategy %q", string(strategy))
	}

	plan := &Plan{}
	for _, f := range files {
		abs := filepath.Join(workspace, filepath.FromSlash(f.path))
		existing, err := os.ReadFile(abs)
		switch {
		case os.IsNotExist(err):
			plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpCreate, Content: f.content})
		case err != nil:
			return nil, &WorkspaceError{Op: "read existing file", Path: abs, Err: err}
		default:
			if bytes.Equal(existing, f.content) {
				// 内容一致：跳过，保证重复执行幂等。
				plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpSkip})
				continue
			}
			switch strategy {
			case ConflictSkip:
				plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpSkip})
			case ConflictOverwrite:
				plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpOverwrite, Content: f.content})
			case ConflictMerge:
				merged, err := mergeContent(f.merge, existing, f.content)
				if err != nil {
					return nil, fmt.Errorf("cannot merge %q: %w", f.path, err)
				}
				if bytes.Equal(merged, existing) {
					plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpSkip})
				} else {
					plan.Files = append(plan.Files, &PlannedFile{Path: f.path, Op: OpMerge, Content: merged})
				}
			}
		}
	}
	return plan, nil
}
