package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

// refPattern matches {{ref "some-id"}} calls in template sources, which
// implicitly reference another file by id.
var refPattern = regexp.MustCompile(`ref\s+"([^"]+)"`)

// resolveDependencies orders file specs by dependency (dependsOn / ref
// references found in source content) and reports cycles with a readable
// path. readSource returns the raw content of a template source file.
func resolveDependencies(files []FileSpec, readSource func(source string) ([]byte, error)) ([]FileSpec, error) {
	index := map[string]int{} // id or source -> position in files
	for i, f := range files {
		if f.ID != "" {
			index[f.ID] = i
		} else {
			index[f.Source] = i
		}
	}

	// Build adjacency: node -> dependency indices.
	deps := make([][]int, len(files))
	for i, f := range files {
		seen := map[int]bool{}
		addDep := func(ref string) error {
			j, ok := index[ref]
			if !ok {
				return fmt.Errorf("%w: file %q references %q which does not exist",
					ErrMissingDependency, nodeLabel(f), ref)
			}
			if j == i {
				return fmt.Errorf("%w: file %q references itself",
					ErrCyclicDependency, nodeLabel(f))
			}
			if !seen[j] {
				seen[j] = true
				deps[i] = append(deps[i], j)
			}
			return nil
		}
		for _, d := range f.DependsOn {
			if err := addDep(d); err != nil {
				return nil, err
			}
		}
		if readSource != nil {
			content, err := readSource(f.Source)
			if err != nil {
				return nil, fmt.Errorf("scaffold: cannot read source %q for dependency analysis: %w", f.Source, err)
			}
			for _, m := range refPattern.FindAllStringSubmatch(string(content), -1) {
				if err := addDep(m[1]); err != nil {
					return nil, err
				}
			}
		}
	}

	// DFS topological sort with cycle detection.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(files))
	stack := []int{}
	var order []FileSpec

	var visit func(i int) error
	visit = func(i int) error {
		color[i] = gray
		stack = append(stack, i)
		for _, j := range deps[i] {
			switch color[j] {
			case gray:
				return fmt.Errorf("%w: %s", ErrCyclicDependency, formatCycle(files, stack, j))
			case white:
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = black
		order = append(order, files[i])
		return nil
	}

	for i := range files {
		if color[i] == white {
			if err := visit(i); err != nil {
				return nil, err
			}
		}
	}
	return order, nil
}

// nodeLabel returns a human readable label for a file spec.
func nodeLabel(f FileSpec) string {
	if f.ID != "" {
		return fmt.Sprintf("id=%q (%s)", f.ID, f.Source)
	}
	return f.Source
}

// formatCycle renders the cycle path as "a -> b -> c -> a".
func formatCycle(files []FileSpec, stack []int, cycleStart int) string {
	start := 0
	for start < len(stack) && stack[start] != cycleStart {
		start++
	}
	path := make([]string, 0, len(stack)-start+1)
	for _, idx := range stack[start:] {
		path = append(path, nodeLabel(files[idx]))
	}
	path = append(path, nodeLabel(files[cycleStart]))
	return strings.Join(path, " -> ")
}
