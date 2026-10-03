package scaffold

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedSpecVersion is returned when the template declares a spec
// version outside the range supported by this engine.
var ErrUnsupportedSpecVersion = errors.New("scaffold: unsupported template spec version")

// ErrDeprecatedField is returned when a manifest uses a deprecated field with
// no deterministic mapping enabled.
var ErrDeprecatedField = errors.New("scaffold: deprecated manifest field")

// ErrUnknownField is returned when the manifest contains a field the engine
// does not know (fields are never silently ignored).
var ErrUnknownField = errors.New("scaffold: unknown manifest field")

// ErrCyclicDependency is returned when file dependency references form a
// cycle.
var ErrCyclicDependency = errors.New("scaffold: cyclic file dependency")

// ErrMissingDependency is returned when a file references an id that does not
// exist.
var ErrMissingDependency = errors.New("scaffold: missing file dependency")

// ErrMergeConflict is returned when a merge strategy cannot reconcile the
// existing file with the generated content. The existing file is left
// untouched.
var ErrMergeConflict = errors.New("scaffold: merge conflict")

// ErrWorkspaceLocked is returned when another generation is already running
// on the target workspace.
var ErrWorkspaceLocked = errors.New("scaffold: workspace is locked by another generation")

// ErrPermission is returned when the workspace cannot be written to.
var ErrPermission = errors.New("scaffold: workspace permission denied")

// ErrInsufficientSpace is returned when the workspace does not have enough
// free disk space for the generated files.
var ErrInsufficientSpace = errors.New("scaffold: insufficient disk space")

// ValidationError aggregates every variable validation problem found in a
// single pass. It points at the specific variable (and reason) so callers can
// fix all of them at once.
type ValidationError struct {
	Problems []VariableProblem
}

// VariableProblem describes one missing or invalid variable.
type VariableProblem struct {
	Variable string // variable name
	Reason   string // human readable reason
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("scaffold: template variable validation failed:\n")
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "  - %s: %s\n", p.Variable, p.Reason)
	}
	return b.String()
}

// Has reports whether e contains any problems.
func (e *ValidationError) Has() bool { return len(e.Problems) > 0 }

// GenerationError wraps a failure that occurred during plan or execute,
// carrying the phase for readable reporting.
type GenerationError struct {
	Phase string // "plan" or "execute"
	Err   error
}

func (e *GenerationError) Error() string {
	return fmt.Sprintf("scaffold %s phase failed: %v", e.Phase, e.Err)
}

func (e *GenerationError) Unwrap() error { return e.Err }
