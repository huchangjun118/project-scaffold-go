# Template model

A template is a directory containing a `template.json` manifest and the
source files it references. Source files are
[`text/template`](https://pkg.go.dev/text/template) templates; their output
is written into the workspace.

## Manifest

```json
{
  "specVersion": "1",
  "name": "my-template",
  "description": "optional",
  "variables": [ ... ],
  "files": [ ... ]
}
```

| Field         | Required | Description |
|---------------|----------|-------------|
| `specVersion` | yes      | Template format version. The engine rejects versions newer than it supports. |
| `name`        | no       | Human readable template name. |
| `description` | no       | Human readable template description. |
| `variables`   | no       | Variable model (see below). |
| `files`       | yes      | File generation rules. At least one file is required. |

Unknown fields are rejected (with `ErrUnknownField`) rather than silently
ignored.

### Deprecated fields

| Deprecated field | Maps to | Semantics |
|------------------|---------|-----------|
| `version`        | `specVersion` | Used only when `specVersion` is absent; otherwise ignored with a warning. |
| `vars`           | `variables`   | Used only when `variables` is absent; otherwise ignored with a warning. |

Using a deprecated field produces a warning on the loaded template and is
never a silent no-op.

## Variables

```json
{
  "name": "projectName",
  "type": "string",
  "required": true,
  "default": null,
  "pattern": "^[a-z][a-z0-9-]*$",
  "description": "Go module name"
}
```

| Field         | Applies to        | Description |
|---------------|-------------------|-------------|
| `name`        | all               | Variable identifier used in templates. |
| `type`        | all               | `string`, `int`, `bool`, `enum` or `list`. |
| `required`    | all               | Caller must supply a value (no default allowed). |
| `default`     | all               | Value used when the caller does not supply one. |
| `description` | all               | Human readable description. |
| `pattern`     | `string`          | Regular expression the value must match. |
| `min` / `max` | `int`             | Inclusive bounds. |
| `values`      | `enum`            | Allowed values. |
| `itemType`    | `list`            | Element type: `string` (default) or `int`. |

Validation is all-or-nothing per pass: every missing or invalid variable is
reported in a single `ValidationError`, each problem naming the variable and
the reason. Supplying a variable that is not declared is also reported
(rather than ignored).

### Zero values

An optional variable without a default gets a zero value: `""` for
`string`/`enum`, `0` for `int`, `false` for `bool`, `[]` for `list`.

## Files

```json
{
  "id": "main",
  "source": "files/main.go.tmpl",
  "target": "cmd/{{.projectName}}/main.go",
  "condition": "{{.includeLicense}}",
  "each": "features",
  "dependsOn": ["go.mod"],
  "conflict": "merge"
}
```

| Field       | Required | Description |
|-------------|----------|-------------|
| `source`    | yes      | Path to the source template, relative to the template directory. |
| `target`    | yes      | Output path relative to the workspace; rendered as a template, so file and directory names may vary with variables. |
| `id`        | no       | Stable identifier other files can reference. |
| `condition` | no       | Template expression; the file is generated only when it renders truthy. |
| `each`      | no       | Name of a `list` variable; one file is generated per element. |
| `dependsOn` | no       | File ids (or source paths) that must be generated first. |
| `conflict`  | no       | Per-file conflict strategy: `skip`, `overwrite` or `merge`. Overrides the global strategy. |

### Conditions

A condition is rendered with the variable set. It is truthy unless the
rendered output (trimmed) is empty or one of `false`, `0`, `no`, `off`
(case-insensitive). When false, the file is skipped and the plan records
`skip` with reason `condition evaluated false`.

### Repetition (`each`)

When `each` names a list variable, the file is rendered once per element.
The render context is the variable set plus `item` (the element) and
`index` (the zero-based position). The target template can use
`{{.item}}` and `{{.index}}` to produce distinct paths.

### Dependencies

Files are generated in dependency order. A dependency is declared by:

- listing an id or source path in `dependsOn`; or
- calling `{{ref "id"}}` inside a source template.

The `ref` function returns the rendered target path of the referenced file.
A reference to an id that does not exist is an error
(`ErrMissingDependency`). A reference cycle is rejected with
`ErrCyclicDependency` and a readable path such as
`id="a" (files/a.tmpl) -> id="c" (files/c.tmpl) -> id="a" (files/a.tmpl)`.

### Path safety

Rendered targets must stay inside the workspace. Absolute paths and paths
containing `..` components are rejected.
