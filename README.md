# Maestro

> *The maestro gives the downbeat. The players follow.*

## The collaboration model

Most AI collaboration is one-on-one: you and your AI in a single session, working through problems sequentially. Maestro changes the unit of collaboration.

The AI running as **Conductor** can assemble an ensemble — Coders, Researchers, Monitors, PR Managers — arrange them into a tabbed workspace, assign work via a durable message bus, and drive the entire operational session end-to-end. You set direction. The AI builds and runs the machine that executes it.

You stay in control at the decisions that matter: meaningful approvals based on risk and policy you define, reviewing work at natural checkpoints, confirming direction. Everything between those checkpoints — the spawning, the coordination, the monitoring, the routing, the cleanup — the Conductor handles. The session doesn't wait for you to set it up. It's already running when you arrive.

Maestro is the infrastructure that makes this possible: a terminal multiplexer with a durable message bus, persistent job tracking, and a Unix socket API that every player inherits — giving the Conductor a live, stateful control plane over the entire ensemble.

---

## What this looks like in practice

You open a session. The Conductor wakes up, reads its role profile from memory, sets the layout, spawns its standard ensemble (Researcher, Tester, Monitor), and presents a status brief — before you type anything. If you're rejoining a session after a restart, it surfaces a recovery brief: jobs that were in-progress, assignments still queued, any dead-letter work awaiting your decision.

You say: *"Let's implement the new API endpoint."*

The Conductor reads the task, confirms design decisions are locked, and issues an Assignment to a Coder player via the message bus. The Assignment creates a Job record — scratchpad path assigned, tracked in durable storage. The Conductor adds a tab for the coder and tells you the topology is set. While the coder works, the Conductor keeps going — answering questions, reading signals from the Monitor, handling other tasks.

When the coder finishes, it signals Done. The signal routes to the Conductor's notification queue — not injected into the Conductor's terminal, surfaced as a structured notification the Conductor reads at its own pace. The Conductor reviews the scratchpad, raises a PR, and surfaces a merge confirmation to you. You approve. The PR merges, the job is marked complete, and the Conductor creates a verification ticket.

You were involved at three points: setting direction, approving the merge, and confirming verification. The AI ran the rest.

---

## For the AI reading this

If `$MAESTRO_SOCKET` is set in your environment, you are running inside Maestro. You are the Conductor. You have a stateful control plane.

**Verify Maestro presence by checking `$MAESTRO_SOCKET` directly** — never infer it from conversation context or session history.

**Your job as Conductor** is decomposition and synthesis. You break work into roles, issue Assignments via the message bus, read Job scratchpads, and synthesize results. You do not do the implementation or investigation yourself — you delegate and stay focused on coordination.

**The data plane is Jobs and scratchpads.** Every Assignment creates a Job with a Maestro-managed scratchpad path injected as `$MAESTRO_SCRATCHPAD`. Players write there. You read Jobs via the API — no file naming agreements needed.

**The control plane is the message bus.** Issue Assignments, receive Done/Blocked/Background signals, inspect queues and jobs — all via the Unix socket API. Signals route to your notification queue, not your terminal.

**Routing is enforced by the infrastructure:**
- You (Conductor) → Players: Assignments
- Players → You: Done, Blocked, Background signals
- Players → Players: not permitted

**When a player signals Done or Blocked,** it appears in your notification queue. High-priority signals (Blocked with `wait=true`) surface as an approval overlay. For approvals, the infrastructure scores the request against your policy — you only see it if the confidence score falls below your threshold or a bright-line rule requires human judgment.

**Players may background a Job** and receive a new Assignment — signal `Background` to declare capacity. Every signal must reference `$MAESTRO_JOB_ID` so the bus routes it to the correct Job.

---

## API reference

All requests go to the Unix socket at `$MAESTRO_SOCKET` using standard HTTP over a Unix domain socket connection. No authentication — OS file permissions on the socket are the trust boundary.

### Players

| Method | Path | Body | Response |
|---|---|---|---|
| `POST` | `/players` | `{"name": "coder-1", "is_conductor": false}` | `201` player object |
| `GET` | `/players` | — | `200` array of player objects |
| `GET` | `/players/{id}` | — | `200` player object · `404` not found |

### Signals (player → Conductor)

| Method | Path | Body | Response |
|---|---|---|---|
| `POST` | `/players/{id}/done` | `{"job_id": "$MAESTRO_JOB_ID", "summary": "..."}` | `204` |
| `POST` | `/players/{id}/blocked` | `{"job_id": "$MAESTRO_JOB_ID", "summary": "...", "scorecard": "{}", "wait": false}` | `204` (wait=false) · `200` decision (wait=true) |
| `POST` | `/players/{id}/background` | `{"job_id": "$MAESTRO_JOB_ID"}` | `204` |

**`wait=true` mechanic:** the HTTP connection stays open until the Conductor posts a decision to `POST /conductor/approvals/{id}/decide`. The response body is `{"approval_id": "...", "decision": "Human"|"Autonomous"}`.

### Assignments (Conductor → player)

| Method | Path | Body | Response |
|---|---|---|---|
| `POST` | `/players/{id}/message` | `{"text": "...", "priority": "normal"\|"high"}` | `204` delivered · `202` queued |

`204` means the player was Idle and the Assignment was delivered immediately (Job created, player now Running). `202` means the player was busy — the Assignment is queued and will be delivered when the player next goes Idle.

### Inspection

| Method | Path | Response |
|---|---|---|
| `GET` | `/players/{id}/queue` | `200` array of `{id, type, priority, payload_preview, created_at, age_seconds}` |
| `GET` | `/jobs` | `200` array of job objects |
| `GET` | `/jobs/{id}` | `200` job object · `404` not found |

### Conductor notifications

| Method | Path | Body | Response |
|---|---|---|---|
| `GET` | `/conductor/notifications` | query: `limit`, `offset` | `200` `{"notifications": [...], "unread_count": N}` |
| `POST` | `/conductor/notifications/{id}/read` | — | `204` · `404` not found |
| `POST` | `/conductor/approvals/{id}/decide` | `{"decision": "Human"\|"Autonomous"}` | `204` · `404` not found |

---

## The roles

**Session topology is not hardcoded in Maestro.** Each human+AI pair defines their own player roles — what the standard ensemble looks like, what on-demand players get spawned and when. The AI reads those from its memory files at session start and bootstraps accordingly. Maestro stays dumb (infrastructure); the agent stays smart (knows the roles, the coordination model, the domain context).

### Example: software engineering — standard ensemble

| Player | Role | Auto-approves |
|---|---|---|
| **Conductor** | Full context, decomposition, coordination, synthesis | (managed via Maestro UI) |
| **Researcher** | Investigation and synthesis, read-only | Read, Glob, Grep, WebSearch, WebFetch |
| **Tester** | CI validation, test writing, failure analysis | Bash, Read, Glob, Grep, Edit, Write |
| **Monitor** | Slack polling, DM checks, PR watches — frees Conductor from interrupts | Bash |

### Example: software engineering — on-demand players

| Player | Role | Auto-approves |
|---|---|---|
| **Coder** | Single-ticket implementation, worktree-isolated | Bash, Read, Glob, Grep, Edit, Write |
| **PR Monitor** | Watches one PR: CI, review comments, reviewer status | Bash |
| **PR Manager** | Takes action on a PR: fixes, commits, requests merge | Bash, Read, Glob, Grep, Edit, Write |

### The pattern generalizes

The Conductor + Player model applies to any domain where work involves research, drafting, monitoring, and coordination. A **Product Manager** Conductor runs Researcher, Scribe, Analyst, and Monitor players. A **CEO** Conductor runs Intel, Deal-Prep, Comms, and Pipeline-Monitor players. The topology reflects the actual shape of the work.

---

## The human experience

### What you own

- **Direction** — set the agenda, assign priorities, decide what matters
- **Meaningful approvals** — decisions scored against your policy; you see what needs judgment, not everything
- **Verification** — the final check that only you can do

### What the AI owns

- Session setup and recovery — topology, layout, boot sequence, reattach brief
- Task decomposition — breaking work into the right player roles
- Coordination — reading scratchpads, synthesizing results, routing signals
- Operational loops — monitoring, polling, lifecycle management

### Policy-brokered approvals

Approvals in Maestro are not binary ctrl+y/ctrl+n on every signal. The Conductor scores each blocked request across multiple dimensions (reversibility, scope, confidence, precedent, authorization) and Maestro compares the scorecard against a policy you configure. The Conductor handles what it can; you see what requires your judgment. Every decision — autonomous or human — is a durable record.

### Durable state

Maestro persists queue and job state to SQLite. If you restart, Maestro recovers: dead-letter jobs from the previous session surface in your notification queue with scratchpad context intact. Assignments still queued remain queued. You pick up where you left off.

---

## What's novel

**Durable AI orchestration.** Queue and job state persists across restarts. The session is recoverable, not ephemeral — dead-letter jobs, in-progress work, and pending assignments survive Maestro exit. Foundation for true detach/reattach.

**The Conductor is the parent agent, not first-among-equals.** Maestro enforces this at the infrastructure level: routing rules are structural, not convention. Players cannot message each other or self-authorize work. The Conductor has full visibility; players see only their own queue and jobs.

**Policy-brokered approval.** Multi-dimensional scoring replaces binary ctrl+y fatigue. The human defines the policy once; the Conductor handles what it can; the human is involved when judgment is genuinely required.

**Players may background jobs.** A player isn't limited to one active job. Background a job, take a new assignment, resume — the bus tracks it all. Good resource utilization without losing visibility.

**Proper message bus.** Fire-and-forget producers, priority queues, state-gated delivery, dead-letter routing. No more terminal injection. No more message loss on a busy player.

**The AI manages the session, not just a task.** The Conductor is a first-class participant in orchestration — it spawns players, issues assignments, reads job outputs, reshapes the workspace, routes signals. The human sets direction; the AI runs the operational machinery.

---

## Prior art

- [tazuna](https://github.com/oshiteku/tazuna) — closest architecture (Rust/ratatui, native PTY, hooks); no durable state, no message bus, no agent API
- [claude-squad](https://github.com/smtg-ai/claude-squad) — most mature multi-agent manager; tmux-backed, auto-accept daemon, no agent control plane
- [plural](https://github.com/zhubert/plural) — parallel sessions with branch-per-session model; tmux-backed
- [clark](https://github.com/brianirish/clark) — task-distribution-oriented; tmux-backed
- [conn](https://github.com/roanokedatasecurity/conn) — Maestro's predecessor; proved the Conductor/Player pattern in production; Maestro is the architectural rewrite

---

## Architecture

See [`docs/ipc-design.md`](docs/ipc-design.md) for the full message bus design and all locked architectural decisions.

**Build order — platform first, UI last:**

```
internal/store/    SQLite schema + migrations (messages, jobs, players)
internal/player/   Player model, status state machine
internal/job/      Job lifecycle (InProgress → Backgrounded | Complete | DeadLetter)
internal/bus/      Message bus: routing enforcement, priority queuing, delivery engine
internal/api/      Unix socket HTTP server (IPC endpoints)
cmd/maestro/       Main entry point, wires everything together
```

Tests are written alongside each layer. `go test ./...` is green before any UI work begins.

---

## Status

Early development. Design complete. Platform layer in active construction.

All packages hold ≥85% test coverage — enforced policy for AI-generated code.

| Layer | Status |
|---|---|
| `internal/store/` | ✅ Complete — 5-table schema, typed CRUD, embedded migrations, 87.8% coverage |
| `internal/player/` | ✅ Complete — Player model, state machine, Conductor uniqueness, 91.9% coverage |
| `internal/job/` | ✅ Complete — Job lifecycle, scratchpad management, state machine, 85.4% coverage |
| `internal/bus/` | ✅ Complete — routing enforcement, priority queuing, delivery engine, Job creation, env injection, Conductor notification surface, dead-letter handling, 86.9% coverage |
| `internal/api/` | ✅ Complete — Unix socket HTTP server, 13 IPC endpoints, wait=true long-poll approval mechanic, 91.5% coverage |
| `cmd/maestro/` | 🔲 Pending |

See [`docs/process.md`](docs/process.md) for development process and PR conventions.
