package scaffold

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// kvPattern matches key/value lines such as "KEY=value" or "key: value",
// which are common in config files (.env, properties, yaml-like).
var kvPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_.\-]*)\s*[=:]\s*(.*?)\s*$`)

// mergeContent reconciles existing workspace content with newly generated
// content under the merge strategy. It returns the merged bytes. On failure
// it returns ErrMergeConflict and the caller must leave the original file
// untouched.
//
// Semantics:
//   - identical content is a no-op (idempotent);
//   - key/value lines that disagree on the same key are a conflict;
//   - otherwise lines present in the generated content but missing from the
//     existing content are appended (union merge).
func mergeContent(existing, generated []byte) ([]byte, error) {
	if bytes.Equal(existing, generated) {
		return generated, nil
	}

	existingLines := splitLines(existing)
	generatedLines := splitLines(generated)

	// Index key/value pairs already present in the workspace file.
	existingKV := map[string]string{}
	for _, line := range existingLines {
		if k, v, ok := parseKV(line); ok {
			if prev, dup := existingKV[k]; dup && prev != v {
				return nil, fmt.Errorf(
					"%w: key %q appears with conflicting values in the existing file (%q vs %q)",
					ErrMergeConflict, k, prev, v)
			}
			existingKV[k] = v
		}
	}

	// A generated key/value line that disagrees with the workspace file is
	// a hard conflict: we never silently overwrite user values.
	for _, line := range generatedLines {
		if k, v, ok := parseKV(line); ok {
			if prev, exists := existingKV[k]; exists && prev != v {
				return nil, fmt.Errorf(
					"%w: key %q has value %q in the workspace but the template wants %q",
					ErrMergeConflict, k, prev, v)
			}
		}
	}

	// Append generated lines that are missing from the workspace file.
	existingSet := make(map[string]bool, len(existingLines))
	for _, line := range existingLines {
		existingSet[line] = true
	}
	var additions []string
	for _, line := range generatedLines {
		if !existingSet[line] {
			existingSet[line] = true
			additions = append(additions, line)
		}
	}
	if len(additions) == 0 {
		// Nothing to add: the workspace file already satisfies the
		// template, so leave it byte-for-byte unchanged.
		return existing, nil
	}

	var b strings.Builder
	b.Write(existing)
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	for _, a := range additions {
		b.WriteString(a)
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// parseKV extracts a key/value pair from a line.
func parseKV(line string) (key, value string, ok bool) {
	m := kvPattern.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// splitLines splits file content into lines without trailing empty noise.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	s := string(data)
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
