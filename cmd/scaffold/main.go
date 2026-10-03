// Command scaffold renders a template into a target workspace.
//
// Usage:
//
//	scaffold -template DIR -target DIR [-vars JSON] [-conflict skip|overwrite|merge] [-plan]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	scaffold "github.com/huchangjun118/project-scaffold-go/scaffold"
)

func main() {
	templateDir := flag.String("template", "", "template directory (required)")
	target := flag.String("target", "", "target workspace directory (required)")
	varsJSON := flag.String("vars", "", "variable values as a JSON object or @file.json")
	conflict := flag.String("conflict", "merge", "conflict strategy: skip, overwrite or merge")
	planOnly := flag.Bool("plan", false, "only print the plan, do not write anything")
	flag.Parse()

	if *templateDir == "" || *target == "" {
		flag.Usage()
		os.Exit(2)
	}

	vars := map[string]any{}
	if *varsJSON != "" {
		raw := *varsJSON
		if strings.HasPrefix(raw, "@") {
			data, err := os.ReadFile(strings.TrimPrefix(raw, "@"))
			if err != nil {
				fatal("cannot read vars file: %v", err)
			}
			raw = string(data)
		}
		if err := json.Unmarshal([]byte(raw), &vars); err != nil {
			fatal("cannot parse vars JSON: %v", err)
		}
	}

	opts := scaffold.Options{
		Workspace:   *target,
		TemplateDir: *templateDir,
		Variables:   vars,
		Conflict:    *conflict,
	}

	if *planOnly {
		tmpl, err := scaffold.LoadTemplate(*templateDir)
		if err != nil {
			fatal("%v", err)
		}
		_ = tmpl
		// Plan-only: run the plan phase without executing.
		plan, err := scaffold.PlanGeneration(opts)
		if err != nil {
			fatal("%v", err)
		}
		printPlan(plan)
		return
	}

	plan, err := scaffold.Generate(opts)
	if err != nil {
		printPlan(plan)
		fatal("%v", err)
	}
	printPlan(plan)
	fmt.Println("generation complete")
}

func printPlan(plan *scaffold.Plan) {
	if plan == nil {
		return
	}
	fmt.Printf("Plan for %s:\n", plan.Workspace)
	ops := make([]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		ops = append(ops, e.Op)
		fmt.Printf("  %-9s %s\n", strings.ToUpper(e.Op), e.Target)
	}
	counts := map[string]int{}
	for _, op := range ops {
		counts[op]++
	}
	var parts []string
	for op, n := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", op, n))
	}
	sort.Strings(parts)
	fmt.Printf("  total: %s\n", strings.Join(parts, ", "))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scaffold: %s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}
