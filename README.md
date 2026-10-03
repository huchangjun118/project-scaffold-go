# project-scaffold-go

A project scaffold generation engine for editors and CLIs. It renders a
template on disk into a target workspace, with a two-phase
plan-then-execute flow, transactional application, conflict strategies,
variable validation, dependency ordering and crash recovery.

## Features

- **Variable model** with types (`string`, `int`, `bool`, `enum`, `list`),
  defaults and validation rules (required, pattern, min/max, enum values,
  item types). All missing/invalid variables are reported in one pass, each
  pointed at the offending variable.
- **Dynamic file names** via `text/template` rendering of target paths.
- **Conditional inclusion** with `condition` and **repeated snippets** with
  `each` (one file per list element, with `{{.item}}` / `{{.index}}`).
- **Dependency ordering** between files via `dependsOn` or `{{ref "id"}}`
  references; cyclic dependencies are rejected with a readable path.
- **Two-phase generation**: `PlanGeneration` lists every create / overwrite
  / skip / merge operation without touching the workspace; `Generate`
  executes transactionally.
- **Conflict strategies**: `skip`, `overwrite`, `merge`. Merge is a union
  merge with key/value conflict detection; a merge failure never modifies
  the original file. Repeated runs are idempotent.
- **Transactional application**: all files are staged first, then applied
  with backups; any failure rolls the workspace back. Leftovers from an
  interrupted process are recovered on the next run.
- **Permission and resource handling**: read-only targets, blocked paths
  (a file where a directory is needed) and insufficient disk space fail
  fast with readable errors; the workspace is unchanged after failure.
- **Version compatibility**: templates declaring a spec version newer than
  the engine supports are rejected. Deprecated fields are mapped with
  deterministic semantics and a warning, never silently ignored.
- **Concurrent generation** is guarded by a workspace lock.

## Quick start

```sh
# Print the plan only (writes nothing):
go run ./cmd/scaffold -template templates/go-service -target ./out \
  -vars '{"projectName":"demo","port":9090}' -plan

# Generate:
go run ./cmd/scaffold -template templates/go-service -target ./out \
  -vars '{"projectName":"demo","port":9090}' -conflict merge
```

As a library:

```go
plan, err := scaffold.PlanGeneration(scaffold.Options{
    Workspace:   "./out",
    TemplateDir: "templates/go-service",
    Variables:   map[string]any{"projectName": "demo"},
    Conflict:    "merge",
})

plan, err = scaffold.Generate(scaffold.Options{
    Workspace:   "./out",
    TemplateDir: "templates/go-service",
    Variables:   map[string]any{"projectName": "demo"},
    Conflict:    "merge",
})
```

## Documentation

- [Template model](docs/template-model.md) — `template.json` format,
  variables, files, conditions, repetition and dependencies.
- [Generation semantics](docs/generation-semantics.md) — plan/execute
  phases, conflict strategies, transactional application and recovery.
- [Configuration](docs/configuration.md) — CLI flags and `Options`.

## Layout

```
scaffold/        library package (engine)
cmd/scaffold/    CLI
templates/       example templates
docs/            documentation
```
