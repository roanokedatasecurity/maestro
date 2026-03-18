# Maestro Cron Design — Scheduled Work Infrastructure

**Status:** Draft — under review
**Authors:** Kirt Debique, Damon
**Created:** 2026-03-18

---

## Problem

Monitor is the Player whose work is never "done." Every other Player has a clear unit
of work: it receives an Assignment, executes, signals Done, goes Idle. Monitor is a
daemon — it runs continuously, polling channels and synthesizing signal for the
Conductor.

Without dedicated infrastructure, Monitor implements its polling loop inside its
terminal process. That loop is alive only as long as the Maestro session is. Three failure modes follow:

- **Time-triggered work misses its window.** The 9:20 PM RSA synthesis, the weekly
  Sprint Demo DM to Siva, the RSA morning brief — if no session is active when the
  window hits, they don't fire. The workaround (OS-level crons calling into Claude)
  is invisible to Maestro's coordination model and survives only by convention.

- **Monitor is alive only while the session is.** Between sessions, nothing is
  watching. Actionable Slack messages, DMs, and PR events accumulate unobserved
  until the next time the Conductor opens a session and Monitor respawns.

- **Polling is the wrong abstraction for a coordinator.** Monitor shouldn't be
  *doing* the polling — it should be *deciding what to poll*, reading the outputs,
  and synthesizing signal. The execution of the poll itself is infrastructure's job.

---

## Core Insight

**Monitor is a coordinator, not a loop.**

The right model: Monitor registers schedules and reads results. Maestro runs the
schedules. This separates concerns cleanly:

- **Monitor's job:** decide what to monitor, register schedules, read output, decide
  what warrants the Conductor's attention, surface signal via Blocked.
- **Maestro's job:** persist registrations, run scripts on schedule, store output,
  deliver CronFired events to the owning Player.

This generalizes beyond Monitor. A PR Manager can register a cron to check CI every
10 minutes. An ODX Researcher can register a cron to watch a spec URL for changes.
Any Player can register scheduled work; Maestro owns execution.

---

## Design

### Scripts as Committed Artifacts

Maestro runs scripts — it does not know what they do. A cron registration is a
schedule plus a path to an executable script. The script is responsible for its own
logic (polling a Slack channel, checking a PR, fetching a URL) and writes its output
to stdout. Maestro captures stdout to the assigned scratchpad.

**Scripts live in `scripts/` in the repo** (or any accessible path). They become
committed artifacts — durable, auditable, versioned. A Monitor player writes scripts
for its domain, or a Utility Coder writes them on Monitor's behalf. Maestro stays
dumb about integrations.

This is the same principle as the broader architecture: *Maestro stays infra; the
agent stays smart.*

### CronFired as a Lifecycle Message

When a cron fires, Maestro enqueues a `CronFired` Lifecycle message to the owning
Player's queue. The Player reads the message (which includes the scratchpad path),
reads the output, and decides whether to escalate. If the output is signal, the
Player sends a `Blocked` to the Conductor. If it's noise, the Player absorbs it
silently.

The Conductor never sees raw cron output — only signal the Player decides to surface.

### Session Boundary Behavior

**Phase 1 (single process):** The Maestro scheduler goroutine runs while the app is
active. Crons fire within active sessions. At session start, Maestro checks
`last_fired_at` vs `next_fire_at` for all registered crons and generates synthetic
`CronFired` events for any windows missed while the process was down. This covers
most catch-up cases without requiring a background daemon.

**Phase 2 (detach/reattach):** Cron registrations persist in SQLite and survive
restart. The scheduler runs continuously as part of the backend service — firing
scripts and accumulating `CronFired` events in the notification queue regardless of
whether an app session is active. When the Conductor reconnects, it sees the full
backlog. This is the complete solution to between-session monitoring.

---

## Store Schema Addition

One table added to the MAESTRO-1 store schema. Worth including in sprint 1 — cheap
to add while the schema is fresh; retrofitting later requires a migration.

```sql
CREATE TABLE IF NOT EXISTS cron_jobs (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    script_path     TEXT NOT NULL,
    schedule        TEXT NOT NULL,   -- cron expression ("*/5 * * * *") or interval ("5m")
    scratchpad_path TEXT NOT NULL,   -- Maestro-assigned, same pattern as Job scratchpads
    owner_player_id TEXT,            -- player that registered this cron (null = Conductor)
    last_fired_at   DATETIME,
    next_fire_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
```

---

## IPC API

```
POST   /cron           — register a cron job
DELETE /cron/:id       — deregister a cron job
GET    /cron           — list all registered cron jobs
```

### POST /cron

```json
{
  "name":        "slack-nha-poll",
  "script_path": "scripts/poll-slack-channel.sh",
  "args":        ["C0AKLKWH2SX"],
  "schedule":    "*/5 * * * *"
}
```

Response: `{ "id": "...", "scratchpad_path": "/tmp/maestro-scratch/cron-<id>.md" }`

Maestro assigns the scratchpad path. The script receives the scratchpad path as `$MAESTRO_SCRATCHPAD` and writes its output there.

### GET /cron

```json
[
  {
    "id":             "...",
    "name":           "slack-nha-poll",
    "script_path":    "scripts/poll-slack-channel.sh",
    "schedule":       "*/5 * * * *",
    "scratchpad_path": "/tmp/maestro-scratch/cron-<id>.md",
    "owner_player_id": "...",
    "last_fired_at":  "2026-03-18T14:30:00Z",
    "next_fire_at":   "2026-03-18T14:35:00Z"
  }
]
```

---

## CronFired Lifecycle Message

When a cron fires, Maestro enqueues to the owning Player's queue:

```go
Message{
    Type:    Lifecycle,
    From:    "maestro",
    To:      <owner_player_id>,
    Payload: `{"event":"CronFired","cron_id":"...","name":"...","scratchpad":"..."}`,
    Priority: Normal,
}
```

The Player reads the scratchpad from the `CronFired` payload. No naming agreement
needed between Maestro and the Player — Maestro assigned the path at registration.

---

## Monitor Player — Topology Under This Model

With the cron API, Monitor's session-start behavior changes from:

> *Loop forever: poll Slack, poll DMs, poll #kirt-todo, sleep 300s, repeat.*

to:

> *At spawn: register cron schedules for each monitoring domain.*
> *Event loop: read CronFired messages, read scratchpads, decide what to surface.*

This makes Monitor's monitoring topology **declarative and inspectable**. The
Conductor (or Kirt) can call `GET /cron` and see exactly what is being monitored,
at what cadence, with what last-fired timestamp. Monitor becomes auditable as an
artifact rather than opaque as a loop.

---

## Design Decisions (Locked)

### 1. Scripts as committed artifacts, not Maestro-shipped adapters

Maestro does not ship built-in adapters for Slack, GitHub, or any other integration.
The cron API executes scripts. Scripts are authored once, committed to the repo, and
become durable artifacts. This keeps Maestro integration-agnostic.

### 2. Maestro assigns scratchpad paths at registration

Consistent with the Job scratchpad model (locked in ipc-design.md §Design Decision 6):
Maestro assigns all scratchpad paths. No naming agreements between Conductor and Players.
Cron scratchpads follow the same convention as Job scratchpads.

### 3. Monitor decides what to surface — Maestro does not filter

Maestro fires scripts and delivers `CronFired` events. It does not inspect output for
signal. Monitor is responsible for reading scratchpad output and deciding whether to
escalate. This preserves the "Maestro stays infra, agents stay smart" principle.

### 4. `cron_jobs` table added in sprint 1

Schema is defined once and carries forward. The table is cheap to add now; adding it
later requires a migration. The execution engine ships in sprint 2, but the schema
is locked in sprint 1.

---

## Open Questions

1. **Schedule format.** Cron expression (`*/5 * * * *`) vs. interval shorthand (`5m`,
   `1h`). Both are useful; interval shorthand is simpler for common cases. Support both?

2. **Script failure handling.** If a script exits non-zero, does Maestro still enqueue
   a `CronFired` event (with stderr captured), or does it enqueue a `CronError` Lifecycle
   event instead? The Player should know the difference.

3. **Cron ownership.** If the owning Player is dead and a cron fires, where does the
   `CronFired` event route? Options: Conductor directly, dead-letter queue, or silently
   to scratchpad only (no event, Player re-subscribes on respawn).

4. **Script args.** The `POST /cron` payload above includes an `args` array. Should args
   be positional (argv), env vars injected by Maestro, or a JSON blob the script receives
   on stdin? Env vars feel most consistent with how `$MAESTRO_JOB_ID` and
   `$MAESTRO_SCRATCHPAD` are injected into Assignment payloads.
