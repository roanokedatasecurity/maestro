# Maestro Boot Sequence

**Status:** Draft
**Authors:** Kirt Debique, Damon
**Created:** 2026-03-18
**Depends on:** `ux-direction.md`, `ipc-design.md`

---

## Overview

Maestro's boot sequence has two distinct paths — **first run** (empty DB, no
config) and **resume** (config and DB exist). Both paths converge at the same
Conductor-ready state; they differ in how they get there.

The `damon` wrapper script from conn v1 is fully replaced by this sequence.
Boot prompt construction, memory injection, recovery state, and session context
are all owned by Maestro's Go backend — derived from structured data, not
hardcoded strings in a bash wrapper.

---

## Artifacts

Two persistent artifacts drive the boot sequence:

### `~/.maestro/config.toml` — Collaborator identity (portable)

Human-editable, version-controllable, portable across machines. Declares who
the collaborator is and where their context lives. Survives a DB reset. Travels
to a new machine as-is.

```toml
[collaborator]
name         = "Damon"
model        = "claude-sonnet-4-6"
memory_path  = "~/.claude/projects/-Users-kirtdebique-oleria/memory/"
working_dir  = "~/oleria"

[session]
auto_spawn   = ["utility-coder", "researcher", "slack-monitor"]
# boot_prompt_path = "~/.maestro/boot.md"   # optional override; Maestro generates
                                             # from memory + DB state if absent

[api]
# ANTHROPIC_API_KEY read from environment — never written to this file
```

### `~/.maestro/maestro.db` — Session state (durable, local)

SQLite database. Owns job history, message queue, player registry,
notifications, and approvals. See `ipc-design.md` for full schema.

Session state portability is an open design question — see Open Issues below.

---

## First Run

Triggered when `~/.maestro/config.toml` does not exist.

### Phase 1 — Setup Wizard (React, 3 screens)

Maestro renders a first-run wizard before the Conductor tab appears. Goal:
collect the minimum viable profile in ~90 seconds.

**Screen 1 — Identity**
- Collaborator name (text input) — e.g. "Damon"
- Model selector (dropdown) — Sonnet 4.6 / Opus 4.6 / Haiku 4.5
- Defaults: name empty (required), model = Sonnet 4.6

**Screen 2 — Memory**
- Maestro scans for existing `~/.claude/projects/*/memory/` directories and
  presents them as selectable options with detected MEMORY.md previews
- "Create new at `~/.maestro/{name}/memory/`" always available
- This is the most important field: the selected path is where MEMORY.md and
  all memory files live — the collaborator's accumulated context

**Screen 3 — Session behavior**
- Working directory (text input, defaults to cwd at launch)
- Auto-spawn players (checkboxes: Researcher / Coder / Monitor / none)
- Boot behavior: "Generate from memory" (default) or "Use custom boot prompt"
  (reveals file path input)
- "Launch" button

**On Launch:**
1. Write `~/.maestro/config.toml` from wizard inputs
2. Create `~/.maestro/maestro.db`, apply schema migrations
3. Register Conductor player in `players` table (`IsConductor = true`)
4. Transition to boot Phase 2

---

### Phase 2 — Backend Initialization (`OnStartup`, Go)

Runs before the WebView renders anything. By the time React paints, the
backend has a complete picture.

```
1. Open ~/.maestro/maestro.db
2. Apply pending schema migrations (idempotent)
3. Load config.toml → collaborator profile
4. Read MEMORY.md from memory_path → store as boot context string
5. Register Conductor player (or verify existing, mark alive)
6. Compute recovery state:
     - dead_letter_count   (first run: 0)
     - pending_approvals   (first run: 0)
     - unread_notifications (first run: 0)
     - backgrounded_jobs   (first run: 0)
7. Build boot context struct → pass to React via Wails binding
```

### Phase 3 — UI Ready (`OnDomReady`, React → Go → PTY)

React receives the boot context, renders the Conductor tab with xterm.js, and
signals the Go backend to inject the boot prompt into the Conductor PTY.

**Boot prompt (first run, generated):**

```
You are {name}.

{MEMORY.md contents — injected verbatim}

Working directory: {working_dir}
Today's date: {date}
Session state: fresh start — no prior jobs, notifications, or history.

Begin your session-start behavior.
```

**Auto-spawn:** After the boot prompt is injected, Maestro spawns the players
declared in `config.toml [session] auto_spawn`, in order. Each spawned Player
gets its own tab and a registration message routed through the Bus.

---

## Resume

Triggered when `~/.maestro/config.toml` exists and `~/.maestro/maestro.db`
exists. Wizard is skipped entirely.

### Phase 1 — Backend Initialization (`OnStartup`, Go)

Same as first run Phase 2, but recovery state is non-trivial:

```
1. Open ~/.maestro/maestro.db
2. Apply pending schema migrations
3. Load config.toml
4. Read MEMORY.md from memory_path
5. Register new Conductor player (fresh Player row each session — prior
   session's Conductor transitions to Dead; history is preserved)
6. Compute recovery state:
     - dead_letter_count    — Jobs in DeadLetter state from prior sessions
     - pending_approvals    — Approvals with no decision yet
     - unread_notifications — Notifications with read_at IS NULL
     - backgrounded_jobs    — Jobs in Backgrounded state
     - last_session_at      — timestamp of prior Conductor's last_seen_at
7. Build boot context struct
```

### Phase 2 — UI Ready (`OnDomReady`)

**Boot prompt (resume, generated):**

```
You are {name}.

{MEMORY.md contents — injected verbatim}

Working directory: {working_dir}
Today's date: {date}
Session state: resuming — last session {duration} ago.
  {dead_letter_count} dead-letter jobs pending review.
  {pending_approvals} approvals awaiting decision.
  {unread_notifications} unread notifications.
  {backgrounded_jobs} backgrounded jobs.

Begin your session-start behavior.
```

The collaborator wakes up with full memory context and an accurate recovery
picture before saying a word. No checkpoint files, no wrapper script, no
reconstructing state from compaction summaries.

**Auto-spawn:** Same as first run — players declared in `auto_spawn` are
spawned after boot prompt injection. Prior session's Players are shown in tabs
as Dead; they are not re-spawned automatically (the collaborator decides
whether to resume them via new Assignments).

---

## Shutdown (`OnShutdown`, Go)

```
1. Mark all Running players as Dead
2. Transition all InProgress jobs to Backgrounded (not DeadLetter — clean
   shutdown is not a crash; jobs are resumable)
3. Write session summary to notifications table:
     "Session ended. {N} jobs completed, {M} backgrounded, {K} dead-lettered."
4. Close DB connection cleanly
```

This ensures the next boot's recovery state accurately reflects a clean exit
vs. a crash (crash = no OnShutdown = jobs remain InProgress → treated as
potential dead-letter candidates on next boot).

---

## Custom Boot Prompt

If `boot_prompt_path` is set in config.toml, Maestro reads that file and uses
it verbatim instead of generating the boot prompt. MEMORY.md injection and
recovery state are prepended regardless — the custom prompt is appended after.

This is an escape hatch for collaborators with highly specific session-start
rituals that don't fit the generated format.

---

## Migration: conn → Maestro

### What actually needs to migrate

conn v1 has no durable runtime state — that is the problem Maestro solves.
There is no job history, no message queue, no notification log to carry forward.
The only meaningful artifacts that exist today are:

| Artifact | Location | Migration action |
|----------|----------|-----------------|
| Memory files (MEMORY.md, topic files) | `~/.claude/projects/*/memory/` | Point config.toml at existing path — no move required |
| Task files (okd-tasks.md, conn-tasks.md, etc.) | Repo + memory dir | Already text files; no migration |
| Wrapper script (`damon`) | `~/bin/damon` or similar | Replaced by Maestro — decommission after first clean Maestro session |
| Checkpoint sentinels | `/tmp/damon-checkpoint`, `/tmp/damon-restart-context.md` | Ephemeral — discard |
| conn runtime state (kids, PTY sessions) | In-memory only | Nothing to migrate; intentionally ephemeral |

The collaborator's accumulated intelligence — everything that makes Damon
*Damon* — lives entirely in the memory files. Those are already text, already
at a known path, and Maestro reads them directly. Migration is mostly a
configuration act, not a data movement act.

### `maestro migrate` — CLI command

Rather than asking the user to manually fill in the wizard, Maestro ships a
`migrate` subcommand that automates discovery:

```
maestro migrate
```

**What it does:**

```
1. Scan ~/.claude/projects/*/memory/ for directories containing MEMORY.md
2. Present discovered collaborators with a preview of their name + description
3. User selects (or confirms auto-detected) collaborator memory path
4. Detect working directory from memory content (looks for repo paths in MEMORY.md)
5. Prompt for model selection (default: Sonnet 4.6)
6. Prompt for auto-spawn player preferences
7. Write ~/.maestro/config.toml
8. Initialize ~/.maestro/maestro.db with a migration seed record:
     notifications table: "Migrated from conn v1 on {date}. Memory path: {path}."
9. Print: "Ready. Run `maestro` to launch."
```

This replaces the first-run wizard entirely for users coming from conn. Users
starting fresh (no prior Claude Code memory) use the wizard; users migrating
use `maestro migrate`.

### Portability setup at migration time

`maestro migrate` is the natural moment to initialize the git-crypt portability
repo defined in OKD-7. After writing config.toml, it can optionally:

```
1. Initialize ~/.maestro/ as a git repo (or use an existing one)
2. Configure git-crypt for the repo
3. Add config.toml and the memory_path directory to the encrypted repo
4. Commit initial state: "Initialize Maestro — migrated from conn v1"
```

If the user declines (or git-crypt isn't installed), migration continues
without portability setup. A warning is printed: "Portability not configured —
run `maestro setup-portability` to enable cross-machine sync."

### First post-migration boot

On the first Maestro launch after migration, the DB contains the migration seed
notification. The generated boot prompt includes a migration acknowledgment:

```
You are {name}.

{MEMORY.md contents}

Working directory: {working_dir}
Today's date: {date}
Session state: first Maestro session — migrated from conn v1 on {date}.
  Your memory has been carried forward intact.
  conn's runtime state (in-flight sessions, PTY state) was ephemeral and not
  migrated — this is expected; conn had no durable session persistence.

Begin your session-start behavior.
```

This gives the collaborator explicit awareness that they are now in Maestro,
what was and wasn't carried forward, and why. They don't need to wonder whether
something was lost — the boot prompt tells them directly.

After the first clean Maestro session, the `damon` wrapper script (or
equivalent) can be decommissioned. conn v1 continues to function independently
— it is memorialized and self-hosting, not replaced.

---

## Open Issues

### OI-1 — Session State Portability

**Question:** How does `~/.maestro/maestro.db` travel to a new machine?

Config and memory files have a clear portability story: they are text files
that belong in the git-crypt'd repo defined in OKD-7. The SQLite DB is binary
and requires a separate approach.

**Option A: Git the DB directly**
Commit `maestro.db` to the same git-crypt'd repo as config and memory. Binary
blob — no meaningful diffs, but git stores it cleanly and git-crypt encrypts
it. On a new machine: clone repo, launch Maestro, full history is present.

- Pro: single artifact, single repo, no extra tooling
- Pro: git-crypt handles encryption transparently
- Con: binary blobs grow the repo over time; no human-readable history
- Con: git history for the DB is meaningless (no diffs)
- Mitigation: periodic `git gc`, treat DB as a single-writer file

**Option B: Structured export on shutdown**
On `OnShutdown`, Maestro exports a `session-history.json` alongside the DB —
a human-readable summary of the last N sessions (job summaries, notification
counts, approval records). This file is committed to git. On a new machine:
if no DB exists, Maestro bootstraps a fresh DB from the export.

- Pro: human-readable, diffable, meaningful git history
- Pro: DB stays local (large history doesn't travel)
- Con: export/import round-trip; raw message queue is not portable
- Con: a crash without clean shutdown means no export for that session

**Option C: Litestream (continuous replication)**
Litestream replicates SQLite writes continuously to S3/R2/GCS. On a new
machine: restore from object storage, launch Maestro with full history.

- Pro: continuous, no manual sync, no crash gap
- Con: requires external cloud storage dependency
- Con: adds operational surface (bucket, credentials, retention policy)

**Lean:** Option A for now (simplest, consistent with OKD-7 story), with
Option B's export as a complementary human-readable artifact. Option C is the
right long-term answer if Maestro becomes multi-machine active rather than
single-machine with occasional migration.

**Decision:** Open — revisit when OKD-7 portable memory architecture is
implemented. The DB portability story should ship in the same PR as memory
portability.
