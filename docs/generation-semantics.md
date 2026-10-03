# Generation semantics

## Two phases

### Plan

`PlanGeneration` performs all validation and rendering and produces a
`Plan` listing every file operation. It performs reads only:

- the workspace is never modified;
- no lock is taken;
- the plan lists, for every file, one of:
  - `create` — the file does not exist and will be written;
  - `overwrite` — the file exists and will be replaced;
  - `merge` — the file exists and will be merged;
  - `skip` — the file will not be written (condition false, already
    identical, or strategy `skip`).

`Plan.TotalBytes()` and `Plan.Counts()` summarize the plan.

### Execute

`Generate` runs the plan transactionally:

1. **Lock** the workspace (exclusive, non-blocking). A second concurrent
   generation fails immediately with `ErrWorkspaceLocked`.
2. **Recover** leftovers from a previously interrupted run (see below).
3. **Load** the template and **plan** it.
4. **Preflight**: verify every target directory is reachable and writable
   (a path component that exists but is not a directory fails fast), and
   that the workspace has enough free disk space.
5. **Stage** every file into a temporary directory inside the workspace.
6. **Apply** each file in dependency order: back up an existing file, then
   atomically rename the staged file into place.
7. **Commit** by removing backups and the staging directory.

Any failure during apply triggers a **rollback**: files created during the
run are removed, backed-up files are restored byte-for-byte, and newly
created directories are removed. The workspace is left exactly as it was
before the run.

## Conflict strategies

When a target file already exists:

| Strategy    | Behavior |
|-------------|----------|
| `skip`      | Leave the existing file untouched. |
| `overwrite` | Replace the existing file. If the content is identical, the file is planned as `skip` (idempotent). |
| `merge`     | Union merge (see below). If the result is identical to the existing file, it is planned as `skip`. |

The global strategy is set via `Options.Conflict` (or the `-conflict`
CLI flag). A file may override it with its `conflict` field.

### Merge semantics

Merge is a line-based union merge for config-like files:

- identical content is a no-op;
- lines present in the generated content but missing from the existing
  file are appended;
- key/value lines (`KEY=value` or `key: value`) that disagree on the same
  key are a **merge conflict** — the generation fails with
  `ErrMergeConflict` and the original file is left untouched.

This makes merge safe for files like `.gitignore`, `.env` or `*.ini` where
user values must not be silently overwritten.

### Idempotency

Running the same generation twice with the same variables produces the
same result: the second run plans every file as `skip` (identical content
or merge no-op) and writes nothing.

## Crash recovery

A generation can be interrupted (process killed, power loss) at any point.
Recovery runs automatically at the start of the next `Generate`:

- a leftover `*.scaffold-bak` backup with no corresponding target means the
  new file never landed — the backup is restored;
- a leftover backup with a new target in place means the new file landed
  but the backup was not cleaned up — the backup is removed;
- leftover `.scaffold-staging-*` directories are removed.

The kernel releases the workspace `flock` automatically when a process
dies, so a crashed process never leaves the workspace permanently locked.

## Permission and resource errors

| Condition | Error | Behavior |
|-----------|-------|----------|
| Target directory is not writable | `ErrPermission` | Fail before any write. |
| A path component is a regular file (ENOTDIR) | readable error | Fail before any write. |
| Not enough free disk space | `ErrInsufficientSpace` | Fail before any write. |
| A file cannot be written mid-apply | wrapped error | Roll back the whole transaction. |

After any of these failures the workspace content is identical to what it
was before the run.

## Version compatibility

- The engine supports template spec version `1`.
- A template declaring a newer version is rejected with
  `ErrUnsupportedSpecVersion` and a readable message naming the declared
  and supported versions.
- Deprecated fields are mapped deterministically (see
  [template model](template-model.md#deprecated-fields)) and produce a
  warning; they are never silently ignored.
