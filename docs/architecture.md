# Maestro Architecture — Responsibility Boundary

**Status:** Living document — updated as design decisions are locked
**Authors:** Kirt Debique, Damon
**Created:** 2026-03-18

---

## The Core Principle

> *Maestro stays infrastructure. The agent stays smart.*

Maestro provides durable, programmable orchestration primitives. It does not embed
opinions about what those primitives should be used for, how sessions should be
structured, or what the right topology is for a given domain. Those decisions belong
to the agentic profile — the persona definition the AI reads at session start.

This boundary matters because it keeps Maestro composable. A software engineering
team's session looks different from a product manager's session, which looks different
from a CEO's session. Maestro provides the same infrastructure for all of them; the
profile defines the shape.

---

## What Maestro Owns (Infrastructure)

| Concern | Details |
|---|---|
| **Conductor existence** | Every session must have exactly one Conductor. No Conductor, no session. Infrastructure enforced. |
| **Conductor IPC privileges** | The Conductor is the root of the authorization hierarchy: routing authority, full Job/queue visibility, dead-letter recovery, notification surface. Structurally enforced at the bus level — not convention. |
| **Conductor uniqueness** | Only one active Conductor at a time. A new Conductor cannot register while one exists (Dead Conductors do not block replacement). Enforced at the store layer. |
| **Routing rules** | Conductor → Player: Assignments only. Player → Conductor: Done, Blocked, Background. Player → Player: rejected. Enforced by the bus. |
| **Message durability** | Queue and Job records persist to SQLite. Messages survive process exit. |
| **Job lifecycle** | InProgress → Backgrounded \| Complete \| DeadLetter. Scratchpad paths assigned and owned by Maestro. |
| **Dead-letter routing** | Always routes to the Conductor. Only the Conductor can decide what to do with failed work. |
| **Cron execution** | Registered cron scripts fire on schedule, independent of Player terminal process lifetime. Output written to Maestro-assigned scratchpad. CronFired event enqueued to owning Player. |
| **Approval brokering** | Blocked signals scored against policy; Maestro auto-approves or escalates to human. Every decision is a durable record. |
| **Layout rendering** | Maestro renders whatever layout tree the Conductor declares. Panes, splits, ratios. |

---

## What the Agentic Profile Owns (Profile Concern)

| Concern | Details |
|---|---|
| **Layout position of any tab** | Including the Conductor's own tab. The Conductor calls `PUT /layout` at boot with whatever arrangement it wants. Maestro renders it — no defaults, no enforcement. |
| **Which Players to auto-spawn** | The profile defines the standard ensemble (Researcher, Tester, Monitor, etc.) and when on-demand Players are added. Maestro provides the spawn API; the Conductor decides who to call. |
| **Player auto-approve lists** | Which tools each Player gets pre-approved for is a profile/policy decision, not infrastructure. |
| **Boot sequence** | What the Conductor does at session start — reading memory, spawning kids, setting layout, outputting a status brief — is defined in the agentic profile, not hard-coded in Maestro. |
| **Monitoring topology** | What Monitor watches, at what cadence, via which cron scripts. Monitor registers its schedules with the cron API; Maestro executes them. The topology is Monitor's decision. |
| **Cron script content** | Scripts are committed artifacts authored by the agent (or a Utility Coder on its behalf). Maestro runs them; it does not know what they do. |
| **Coordination style** | How the Conductor decomposes work, sequences Players, and synthesizes results. Maestro routes messages; the Conductor decides what to send. |
| **Domain context** | Task files, memory, project conventions, customer context — all agent-side. Maestro has no knowledge of OBS, ODX, Jira, Slack channels, or any specific domain. |

---

## Why This Boundary Exists

**Composability.** A Maestro that hardcodes "Conductor goes left" or "always spawn
a Researcher and a Monitor" has embedded one team's workflow into infrastructure
shared by all. The next team's profile would fight the defaults.

**Programmability.** Kirt's phrasing: *"it keeps the programmable nature of Maestro
intact."* The right architecture is one where the agentic profile can declare
anything valid the infrastructure supports — and Maestro executes it faithfully
without second-guessing the layout.

**Separation of concerns.** Infrastructure bugs (routing failures, message loss,
missed cron windows) are Maestro's to fix. Workflow bugs (wrong Players spawned,
wrong cadence, wrong synthesis logic) are profile concerns. A clear boundary means
both can evolve independently.

---

## How New Design Decisions Get Classified

When a new design question comes up, the test is:

> *"Does this need to be true for **any** valid Maestro session, regardless of domain
> or team?"*

If yes → Maestro owns it.
If it depends on the use case, the team, or the AI persona → agentic profile owns it.

Examples:
- *"There must be exactly one Conductor"* → yes, always true → Maestro owns it.
- *"The Conductor goes on the left"* → depends on the profile → agentic profile owns it.
- *"Messages must be durably persisted"* → yes, always true → Maestro owns it.
- *"Monitor polls #no-humans-allowed every 5 minutes"* → specific to this use case → profile owns it.
- *"Cron scripts run on schedule independent of Player terminal process lifetime"* → yes, always true → Maestro owns it.
- *"The cron script polls Slack channel C0AKLKWH2SX"* → specific to Monitor's profile → profile owns it.

---

## Related Design Docs

- [`docs/ipc-design.md`](ipc-design.md) — Message bus, routing, Job lifecycle, Conductor IPC properties
- [`docs/cron-design.md`](cron-design.md) — Cron API, Monitor-as-coordinator, scripts as committed artifacts

---

## Conductor Crash Recovery

**What happens when the Conductor terminal process exits:**

1. `bus.HandlePlayerDead` is called — Conductor marked Dead, all in-progress Jobs move to DeadLetter, one Lifecycle notification created per dead-letter Job.
2. The Maestro app and all other Players remain alive — Maestro itself does not crash.
3. However, Phase 1 has no in-session affordance to spawn a replacement Conductor. The Conductor slot in the layout is dead with no recovery path within the running session.

**Net effect (Phase 1):** The user must close and reopen Maestro. Durable SQLite state survives the restart. On next session open, the new Conductor registers (Dead Conductors do not block replacement — MAESTRO-2), calls `GetNotifications` at boot, and surfaces a recovery brief: dead-letter Jobs, pending Assignments, scratchpads intact.

**What is preserved across the restart:** all Job records, all scratchpad paths, all queued messages, all notifications. Work state is not lost — only the running terminal processes are gone.

**What is not preserved:** the in-memory state of Players that were running (their terminal processes are gone). They must be respawned. The Conductor decides which Jobs to re-queue and which Players to relaunch based on the recovery brief.

**Phase 2:** Maestro gains an explicit "respawn Conductor" affordance, or detach/reattach means the Conductor process survives app disconnection entirely — making Conductor crash recovery an in-session operation rather than a full restart.

**Design principle:** Maestro does not know how to spawn a Conductor AI. It has no knowledge of Claude, prompts, or agentic profiles. Restart is a human+profile concern. Maestro's contract is: *state is intact when you come back*. How and when to come back is above Maestro's level.
