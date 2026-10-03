# Configuration

## CLI

```sh
scaffold -template DIR -target DIR [-vars JSON] [-conflict STRATEGY] [-plan]
```

| Flag        | Required | Default | Description |
|-------------|----------|---------|-------------|
| `-template` | yes      | —       | Template directory (containing `template.json`). |
| `-target`   | yes      | —       | Target workspace directory (created if missing). |
| `-vars`     | no       | `{}`    | Variable values as a JSON object, or `@file.json` to read from a file. |
| `-conflict` | no       | `merge` | Default conflict strategy: `skip`, `overwrite` or `merge`. |
| `-plan`     | no       | `false` | Print the plan only; write nothing. |

The CLI prints the planned operations and, on success,
`generation complete`. On failure it exits with status 1 and a readable
error.

## Library

```go
type Options struct {
    Workspace   string         // target directory
    TemplateDir string         // template directory
    Variables   map[string]any // caller-supplied variable values
    Conflict    string         // skip, overwrite or merge
}
```

| Function | Description |
|----------|-------------|
| `scaffold.PlanGeneration(opts)` | Plan only; performs reads only. |
| `scaffold.Generate(opts)` | Plan then execute transactionally. |
| `scaffold.LoadTemplate(dir)` | Load and validate a template (used internally). |

Both functions return a `*Plan` describing (or, on failure, partially
describing) the operations. Errors are wrapped in `*GenerationError` with
a `Phase` of `plan`, `execute` or `recovery`.

## Error types

| Error                        | Meaning |
|------------------------------|---------|
| `ErrUnsupportedSpecVersion`  | Template spec version is newer than the engine supports. |
| `ErrDeprecatedField`         | A deprecated field was used without a mapping (currently mapped with a warning instead). |
| `ErrUnknownField`            | The manifest contains a field the engine does not know. |
| `ErrCyclicDependency`        | File dependencies form a cycle. |
| `ErrMissingDependency`       | A file references an id that does not exist. |
| `ErrMergeConflict`           | A merge could not reconcile the existing file with the generated content. |
| `ErrWorkspaceLocked`         | Another generation is running on the workspace. |
| `ErrPermission`              | The workspace cannot be written to. |
| `ErrInsufficientSpace`       | Not enough free disk space. |
| `*ValidationError`           | One or more variable problems; each names the variable and reason. |

## Reserved workspace names

The engine uses these names in the workspace root; templates should not
generate files with these names:

- `.scaffold.lock` — workspace lock file (removed on clean release);
- `.scaffold-staging-*` — staging directories (removed after apply);
- `*.scaffold-bak` — per-file backups during apply (removed after commit).
