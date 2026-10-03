package scaffold

import (
	"fmt"
	"os"
	"syscall"
)

// Options 控制一次生成。
type Options struct {
	// Workspace 是目标工作区目录。
	Workspace string
	// Vars 是用户提供的变量值。
	Vars map[string]any
	// OnConflict 决定已存在文件的处理策略。
	OnConflict ConflictStrategy
}

// Engine 是脚手架生成引擎。
type Engine struct{}

// New 创建引擎。
func New() *Engine { return &Engine{} }

// Plan 计算生成计划，不对工作区产生任何写入。
func (e *Engine) Plan(t *Template, opts Options) (*Plan, error) {
	vars, err := ResolveVariables(t, opts.Vars)
	if err != nil {
		return nil, err
	}
	files, err := renderFiles(t, vars)
	if err != nil {
		return nil, err
	}
	return buildPlan(opts.Workspace, files, opts.OnConflict)
}

// Apply 计划 + 事务执行，把工作区推进到目标状态。
// 同一工作区的并发 Apply 通过目录文件锁串行化。
func (e *Engine) Apply(t *Template, opts Options) (*Plan, error) {
	if opts.Workspace == "" {
		return nil, fmt.Errorf("workspace must not be empty")
	}
	// 变量解析与渲染不触碰工作区，可在锁外完成。
	vars, err := ResolveVariables(t, opts.Vars)
	if err != nil {
		return nil, err
	}
	files, err := renderFiles(t, vars)
	if err != nil {
		return nil, err
	}

	unlock, err := lockWorkspace(opts.Workspace)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// 计划必须在持锁后计算，保证读到的是最新工作区状态。
	plan, err := buildPlan(opts.Workspace, files, opts.OnConflict)
	if err != nil {
		return nil, err
	}
	if err := applyPlan(opts.Workspace, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// lockWorkspace 对工作区目录加排他文件锁，返回解锁函数。
// 锁随进程退出由内核自动释放，不产生残留文件。
func lockWorkspace(workspace string) (func(), error) {
	// 工作区不存在时先创建（属于执行阶段允许的写入）。
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, &WorkspaceError{Op: "create workspace", Path: workspace, Err: err}
	}
	fd, err := syscall.Open(workspace, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, &WorkspaceError{Op: "open workspace", Path: workspace, Err: err}
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		syscall.Close(fd)
		return nil, &WorkspaceError{Op: "lock workspace", Path: workspace, Err: err}
	}
	return func() {
		syscall.Flock(fd, syscall.LOCK_UN)
		syscall.Close(fd)
	}, nil
}
