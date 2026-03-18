# Maestro IPC Design — Inter-Player Message Bus

**Status:** Draft — under review
**Authors:** Kirt Debique, Damon
**Created:** 2026-03-18

---

## Problem

conn's current IPC is implemented as simulated PTY keystrokes. Every communication
path — Conductor sending work to players, players reporting done/blocked to the
Conductor — calls `k.Type()`, which injects text character-by-character into a PTY.
This is fundamentally fragile:

- **No state gating.** A message sent to a running player types into whatever the PTY
  has focused at that moment, corrupting an ongoing task.
- **No queue.** If a player is busy, the message is lost or corrupted — not deferred.
- **No delivery guarantee.** There is no acknowledgment, no way to know if the message
  was received vs. dropped in PTY noise.
- **Conductor PTY contamination.** Done/blocked signals inject into the Conductor's
  PTY input stream, corrupting whatever Kirt is typing. CONN-67 is a symptom of this.
- **Fragile convention.** Correctness depends entirely on the Conductor never messaging
  a running player — enforced by social contract, not infrastructure.

These are not edge cases. They are load-bearing failures in the coordination model.
Everything built on top of the current IPC inherits these failure modes.

---

## Vocabulary

Four concepts that must not be conflated:

**Message** — an entry in Maestro's queue. Has a type, a producer, a target consumer,
a priority, and a payload. Persistent until delivered. The queue owns durability.

**Assignment** — the Message type that carries work ("do this"). Delivery of an
Assignment to a player creates a Job. Distinct from signal types (Done, Blocked,
Lifecycle) which transition Job state rather than create it.

**Job** — Maestro's record of in-progress work. Born when an Assignment is delivered
to a player. Owns the associated scratchpad path. Lifecycle: in-progress →
complete | dead-letter. Lives in Maestro infrastructure, not in file conventions.

**Task** — a human-AI collaboration work item (CONN-71, OKD-38, etc.). Tracked in
task files or external systems. Entirely outside the IPC layer. A Task may result
in one or more Jobs, but the IPC has no knowledge of Tasks.

---

## Persistence

**This is foundational, not optional.** The queue must provide durability guarantees
that allow producers to fire-and-forget. A player that sends a message is done with
it — the infrastructure owns delivery. This changes the producer model entirely:

- Producers do not poll, retry, or time sends around target player state.
- Producers can always inspect the queue to see if their message is still waiting.
- If a message is no longer in the queue, it has been delivered and a Job exists.
- The Conductor never needs to know whether a target player is idle before sending.

**Persistence scope:** Durable, on-disk, from day one. Queue and Job records survive
Maestro process exit. Individual player terminal processes are ephemeral — they die
when Maestro exits — but the work state they represent does not. This is the
foundation of detach/reattach: Maestro can exit and relaunch, reading persistent
state from disk to reconstitute exactly where things were.

**Storage:** SQLite, embedded. No external dependencies, no daemon. The `/queue` and
`/jobs` inspection endpoints are SQL queries. Schema is defined up front; nothing
built on the IPC layer will need to be retrofitted for durability.

**Recovery on relaunch:** Players that were running at exit are dead (terminal
processes gone). Their Jobs transition to `DeadLetter` on next launch. Queued
messages for dead players remain queued — the Conductor sees a recovery brief at
boot: dead-letter Jobs, pending Assignments, scratchpads intact. The Conductor
decides what to re-queue and which players to respawn.

**Detach/reattach (phase 2):** Maestro exits cleanly; relaunch resumes from durable
state. No daemon required — durable storage provides the continuity, not a live
process. Gets the majority of detach value with a single binary and no background
process supervision.

**Backend service + remote multi-session (phase 3):** Maestro splits into a
persistent backend service and a thin native client connected over an authenticated
channel. The right architecture for teams, remote access, and shared sessions — but
a separate product moment. Phase 1 and 2 do not preclude it; the durable schema
designed now will carry forward.

---

## Model

### Message

```go
type MsgType  string // Assignment | Done | Blocked | Lifecycle
type Priority string // High | Normal

type Message struct {
    ID         string    // unique identifier
    From       string    // player ID of producer, or "maestro" for system messages
    To         string    // player ID of target consumer
    Type       MsgType
    Priority   Priority
    Payload    string    // free-form: assignment text, done summary, blocked reason
    WaitForAck bool      // if true (Blocked only): surface approval prompt to Kirt
    CreatedAt  time.Time
}
```

### Message types

| Type | Direction | Creates | Description |
|---|---|---|---|
| `Assignment` | Conductor → player | Job (on delivery) | Work for the target player to perform |
| `Done` | Player → Conductor | — | Player finished its current Job |
| `Blocked` | Player → Conductor | — | Player needs a decision before proceeding |
| `Lifecycle` | maestro → Conductor | — | System event: player spawned, died, compacting |

### Priority

| Priority | Used for |
|---|---|
| `High` | `Blocked` with `WaitForAck`, `Lifecycle` (dead/done) |
| `Normal` | `Assignment`, `Done`, `Blocked` without `WaitForAck` |

High-priority messages are delivered before any queued Normal messages regardless
of arrival order. FIFO within a priority tier. No preemption — a running player
completes its current Job before the next message is delivered.

---

## Per-Player Consume Queue

Each player has a priority queue of inbound Messages. Maestro is the broker — it
enqueues, routes, and delivers.

**Delivery rule:** A Message is delivered to a player only when the player is in
`StatusIdle`. On transition to `StatusIdle`, Maestro drains the queue: delivers the
highest-priority pending Message, which transitions the player to `StatusRunning`.

**Assignment delivery** creates a Job record in Maestro before the Assignment text is
injected into the player's terminal. The Job is born in-progress at that moment.

**For the Conductor:** The Conductor's queue is never injected into its terminal. It
is surfaced as a structured notification list in the Maestro UI. The Conductor reads
and acts on notifications at its own pace.

---

## Job Lifecycle

```
Assignment enqueued
       ↓
Assignment delivered → Job created (in-progress, scratchpad path assigned)
       ↓                              ↓
  Player processes            Player dies (StatusDead)
       ↓                              ↓
  Done signal                  Job → dead-letter
       ↓                              ↓
  Job complete            Conductor notification
                          (human decides: re-queue or discard)
```

**Job record:**

```go
type JobStatus string // InProgress | Complete | DeadLetter

type Job struct {
    ID          string
    MessageID   string    // the Assignment that created this Job
    PlayerID    string    // the player currently executing it
    PlayerName  string    // snapshot of player name at delivery time
    Payload     string    // the assignment text
    Scratchpad  string    // path: Maestro-managed, assigned at Job creation
    Status      JobStatus
    CreatedAt   time.Time
    CompletedAt time.Time // zero if not complete
}
```

**Scratchpad ownership:** Maestro assigns a scratchpad path to each Job at creation.
The path is included in the Assignment payload so the player knows where to write.
This replaces the current ad-hoc file naming convention. The player writes to its
assigned scratchpad; the Conductor reads Job records to find scratchpad paths — no
naming agreements needed between Conductor and players.

---

## Conductor Notification Surface

The Conductor is a consumer with a UI-rendered queue rather than a terminal-injected
one. Maestro maintains a structured notification list for the Conductor. When a new
message arrives, Maestro fires a UI event to trigger an update.

**Notification UI** (design TBD — see Open Questions):
- Notification badge on Conductor tab showing unread count
- `ctrl+n` opens notification list panel
- Notifications persist until dismissed by Kirt
- High-priority / `WaitForAck` Blocked messages surface as the existing approval
  overlay — no change to that path

---

## Dead-Letter Handling

When a player dies (`StatusDead`) while executing a Job, Maestro transitions the Job
to `DeadLetter` and enqueues a High-priority `Lifecycle` Message to the Conductor's
notification queue. The notification includes:

- The Job ID, player name, assignment payload snippet
- The scratchpad path (may contain partial work)
- Suggested action: re-queue Assignment to a new player, or discard

The Conductor decides. Maestro does not auto-reassign.

---

## IPC API (revised)

Same endpoint surface — different internals.

```
POST /players/:id/message       → enqueue Assignment (Conductor only)
{ "text": "...", "priority": "normal" }
→ 202 Accepted   (player busy — enqueued, Job will be created on delivery)
→ 204 No Content (player idle — delivered immediately, Job created)

POST /players/:id/done          → enqueue Done signal to Conductor
{ "summary": "..." }
→ 204 No Content

POST /players/:id/blocked       → enqueue Blocked signal to Conductor
{ "summary": "...", "wait": true }
→ 204 No Content (wait=false)
→ holds open until Kirt responds (wait=true — approval overlay)

GET  /players/:id/queue         → inspect pending messages for a player
→ [{ id, type, priority, payload_preview, created_at, age_seconds }]

GET  /jobs                      → inspect all Jobs (Conductor only)
→ [{ id, player_name, payload_preview, scratchpad, status, age_seconds }]

GET  /jobs/:id                  → inspect a specific Job
```

---

## What This Obsoletes

| Ticket | Resolution |
|---|---|
| CONN-67 | Done/blocked no longer inject into Conductor PTY — architectural fix |
| CONN-49 | `/message` → conductor guard is moot — Conductor queue is UI-only |

---

## Design Decisions (Locked)

### 1. Conductor + Player nomenclature

The orchestral model: **Conductor** is the parent agent — the maestro. **Players**
execute the score. The Conductor authorizes all work; players don't self-direct or
communicate laterally. A **Principal Player** is a player with elevated coordination
responsibility within their section — available as a future role designation without
new terminology.

### 2. The Conductor is the parent agent — routing structurally enforced

The Conductor is not first-among-equals. It is THE agent — the root of the
authorization hierarchy. Maestro is opinionated about this. No Conductor, no session.

**Routing rules, enforced at the bus level:**

| From | To | Type | Allowed |
|---|---|---|---|
| Conductor | Any player | Assignment | ✅ |
| Player | Conductor | Done, Blocked | ✅ |
| maestro | Conductor | Lifecycle | ✅ |
| Player | Any player | Any | ❌ rejected |
| Player | Conductor | Assignment | ❌ players don't authorize work |

**Cascading implications:**
- **Boot:** Maestro always starts with a Conductor. A session without one is invalid.
  All players are children of the Conductor.
- **Authorization:** Jobs exist because the Conductor authorized them. A Job without
  a Conductor-originated Assignment cannot exist.
- **Visibility:** Conductor has full read access to all queues, all Jobs, all
  scratchpads. Players have visibility only into their own queue and their own Job.
- **Dead-letter:** always routes to the Conductor — the only entity that can decide
  what to do with failed work.

### 3. Conductor IPC properties are infrastructure concerns — layout position is not

Maestro is opinionated about the Conductor at the IPC level: there must be exactly
one active Conductor, the session is invalid without one, and the Conductor has
structural privileges (routing authority, full Job/queue visibility, dead-letter
recovery). These are enforced by Maestro infrastructure.

Maestro is **not** opinionated about where the Conductor tab lives in the layout,
which Players are spawned at boot, or what the arrangement is. Those are agentic
profile decisions — declared by the Conductor at session start via `PUT /layout`
and the spawn API. A different Conductor persona could choose an entirely different
workspace arrangement. Maestro renders whatever the Conductor declares.

See [`docs/architecture.md`](architecture.md) for the full Maestro vs. agentic
profile responsibility boundary.

### 4. Durable persistence from day one

Queue and Job records are stored in SQLite from the first implementation. No
in-memory-first shortcut. The schema is defined once and carries forward through
detach/reattach (phase 2) and backend service (phase 3) without retrofitting.

---

## Open Questions

2. **Queue depth / backpressure.** If a player's queue grows unboundedly (perpetually
   busy, Assignments accumulating), should Maestro surface a warning to the Conductor?
   Is there a max queue depth after which new Assignments are rejected?

3. **Delivery acknowledgment.** Once an Assignment is delivered to a player's terminal,
   is the Job considered in-progress? Or is there an explicit ack before marking
   in-progress? (Player could crash immediately after delivery before writing
   anything. With durable storage, delivery is on record regardless.)

### 4. Blocked approval: policy-brokered scorecard, not binary ctrl+y

The current ctrl+y/ctrl+n model requires human involvement on every blocked signal.
In Maestro, approval is brokered by the infrastructure against a policy set by the
human — the Conductor exercises judgment first, and the human is only involved when
the policy requires it.

**Model:**

- **Player enriches the blocked signal** — when signaling blocked, the player
  provides: action type (read / write / external / destructive), proposed action,
  justification, estimated reversibility.

- **Conductor scores the request** — evaluates the blocked signal against Job context,
  player track record, and its own judgment. Produces a **multi-dimensional scorecard**
  of 3–5 independent scores across categories such as:
  - *Reversibility* — can this be undone if wrong?
  - *Scope* — blast radius if the action is incorrect
  - *Confidence* — how certain is the Conductor this is the right action?
  - *Precedent* — has this action type been approved in similar context before?
  - *Authorization* — does this action fall within the player's stated Assignment scope?

  The exact dimensions are a Maestro configuration decision. Each dimension is scored
  independently — a single composite number would flatten meaningful distinctions.

- **Maestro brokers against policy** — the human sets approval policy in Maestro
  config once: per-dimension thresholds, hard limits on action types that always
  require human approval, floor scores that always auto-approve. Maestro compares
  the Conductor's scorecard against policy and either auto-approves or escalates.

- **Human sees it only when policy requires** — when escalated, the human sees the
  full scorecard and justification, not just "ctrl+y?". The decision is informed.

- **Every decision is a durable record** — autonomous approvals and human approvals
  alike are recorded in SQLite with the full scorecard. Auditable.

**Implementation note:** The full scoring and policy engine is a later-phase feature.
Sprint 1 platform layer must accommodate it: the blocked signal schema and Job record
must carry approval metadata fields from day one so nothing needs retrofitting.
The ctrl+y fallback remains valid as a simplified first implementation.

**Long-poll HTTP mechanic survives** — `wait=true` still holds the player's HTTP
request open until a decision is reached (autonomous or human). The approval prompt
routes through the Conductor notification surface, not the Conductor's terminal.

### 5. Conductor notification UI: durable queue, badge + ctrl+n panel

The Conductor's notification surface is its primary instrument for understanding
what the orchestra is doing — not a toast, not a log. It must reflect the full
richness of what flows through it: dead-letter recovery briefs, approval scorecards,
done signals, lifecycle events.

**Locked:**
- Durable notification queue in SQLite — notifications survive Maestro restart
- Unread count badge on Conductor tab
- `ctrl+n` opens a structured notification panel: one entry per notification,
  showing type, player name, age, and one-line summary; expandable to full
  scorecard / Job context inline
- Notifications persist until explicitly dismissed by Kirt
- `wait=true` Blocked surfaces as approval overlay (existing mechanic, new routing)
- **Never terminal injection under any circumstances**

**Visual design** (colors, layout, typography) deferred to OKD-38 status/color
review — the same color language defined for player status badges applies here.

### 6. Players may have multiple active Jobs — Job ID mandatory in all signals

A player is not constrained to a single active Job. A player may background a
current Job (signal `Background` to Maestro) and receive a new Assignment while the
backgrounded Job remains in-progress. Capable AI players can productively parallelize
rather than sitting idle on external dependencies. The Conductor retains full
visibility via durable Job records regardless of how many Jobs a player has active.

**Job status gains `Backgrounded`:**
```
InProgress → Backgrounded → InProgress  (resumed)
                           → Complete
                           → DeadLetter (player died)
```

**Delivery triggers on `StatusIdle` OR `Background` signal** — a player signaling
Background is declaring capacity for another Assignment; Maestro delivers the next
queued Assignment immediately.

**Job ID is mandatory in all player signals** — with multiple active Jobs per player,
the Job ID cannot be inferred from the player ID alone. Done, Blocked, and Background
signals must all reference `$MAESTRO_JOB_ID` explicitly.

**Assignment payload injection:** Maestro injects both into every Assignment delivered:
- `$MAESTRO_JOB_ID` — the Job ID for signal referencing
- `$MAESTRO_SCRATCHPAD` — the Maestro-managed scratchpad path for this Job

**Note — LLM agnosticism:** The current injection format appends these as text
lines to the assignment payload (convention-based). This works for any
text-reading process but is a soft coupling: it assumes the player reads the
appended lines as metadata rather than receiving them as real process environment
variables. The stronger design — and the right direction for a general runtime —
is to pass `$MAESTRO_JOB_ID` and `$MAESTRO_SCRATCHPAD` as actual OS environment
variables set at spawn time. That removes the convention dependency and makes the
player interface genuinely process-agnostic. Not blocking for Phase 1; tracked
here for Phase 2 when the spawn mechanism is built.

**Conductor visibility:** the notification surface shows the full Job list per player
with status. A player with multiple active Jobs is `StatusRunning`; Job count and
states are visible in the `ctrl+n` panel.

**Policy guard:** the Conductor's approval policy may include a configurable
max-concurrent-Jobs-per-player limit — a policy dial, not a hard infrastructure
constraint — to prevent runaway accumulation.

---

## Related Design Docs

- [`docs/cron-design.md`](cron-design.md) — Scheduled work infrastructure: the cron API, Monitor as coordinator, scripts as committed artifacts, and the `cron_jobs` store table. Addresses the between-session monitoring gap and generalizes to any Player that needs time-triggered work.
