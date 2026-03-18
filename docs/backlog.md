# Maestro Feature Backlog

**Status:** Living document — sourced from CONN-* feature ticket review (2026-03-18)
**Tracking prefix:** MAESTRO-B-* (backlog items, not sprint tickets)
**Sprint tickets:** MAESTRO-1 through MAESTRO-6 (platform layer, see conn-tasks.md)

---

## Absorbed — No Ticket Needed

These conn tickets described workarounds for architectural limitations that
Maestro's design inherently solves. No backlog item required.

| CONN | Title | Why absorbed |
|------|-------|-------------|
| CONN-4 | Persistent notification queue | `notifications` table in MAESTRO-1 schema |
| CONN-9 | First-run bootstrap + onboarding | First-run wizard + `maestro migrate` in boot-sequence.md |
| CONN-28 | Conn-native boot sequence | boot-sequence.md covers this completely |
| CONN-30 | Session event log | SQLite bus is the event log; `notifications` table is the conductor queue |
| CONN-31 | First-class Q&A IPC endpoints | Maestro's entire IPC model is first-class; this was a workaround for PTY injection |
| CONN-42 | Compaction prep + auto-restart | Durable state means no checkpoint sentinel; boot sequence handles recovery |
| CONN-45 | Test infrastructure | Every MAESTRO sprint ticket ships with its own test suite |
| CONN-48 | Kid profile directory | Player profiles are rows in the `players` table + config.toml |
| CONN-51 | Migrate to BubbleTea v2 | Maestro does not use BubbleTea |
| CONN-2l | Detach mode / persistent daemon | Superseded by durable SQLite state + native app; true detach is Phase 3 |
| CONN-2u | `conn --prompt` boot flag | `OnDomReady` boot prompt injection replaces wrapper flag |

---

## Player Types & Crew

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-1 | Tester Player | Verification queue, regression suite, operator UX testing. Owns all `verify-*` jobs. | CONN-2c | H |
| MAESTRO-B-2 | CRM Enricher Player | Enriches customer/contact profiles via Clay MCP. Triggered by crew or Conductor. | CONN-46 | M |
| MAESTRO-B-3 | Multi-model crew (heterogeneous players) | Each Player can specify a different model (Opus, Sonnet, Haiku) in its spawn config. Conductor routes high-complexity jobs to Opus, bulk/fast jobs to Haiku. | CONN-39 | H |

---

## UX & Shell

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-4 | Theming system | Configurable color palette for player status states, accent colors, header chrome. CSS variables in React + persisted in config.toml. Replaces the ad-hoc color work in conn. | CONN-5, CONN-56 | M |
| MAESTRO-B-5 | Markdown viewer overlay | Render markdown documents (task files, memory files, scratchpads) in an overlay panel within the Conductor tab. Trivial in React; a pain in BubbleTea. | CONN-41 | M |
| MAESTRO-B-6 | Click-to-focus player tab | Mouse click switches active player. Trivial in React — no BubbleTea focus model to fight. | CONN-27 | L |
| MAESTRO-B-7 | Player tab workspaces | Named workspace groups. Keyboard shortcut to switch (e.g. cmd+1–9). Useful when crew size exceeds comfortable tab bar width. | CONN-2j | M |
| MAESTRO-B-8 | Voice input (speech-to-text) | Mic button in input widget activates speech-to-text; transcript populates command input. Visual indicator during listening. Browser Web Speech API or local Whisper. | CONN-43 | M |
| MAESTRO-B-9 | Cross-player output search | Search across all player xterm.js scrollbacks from Conductor tab. Highlight matches, jump to player + position. | CONN-2n, CONN-16, CONN-47 | M |

---

## Approval & Governance

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-10 | Approval granularity + auto-approve rules | Fine-grained auto-approve rules: by tool, by player, by pattern. Configurable allow-list stored in DB. Bulk clear via API. | CONN-29, CONN-23 | H |
| MAESTRO-B-11 | Away mode + approval delegation | When Conductor is away, pending approvals queue rather than block. Conductor returns to an approval inbox. Optional: delegate approval authority to a designated player (e.g. Tester). | CONN-40 | H |
| MAESTRO-B-12 | Q&A surface (non-blocking ask) | Player can ask the Conductor a question without blocking on a tool approval. Surfaces as a notification with a reply input. Lower friction than a full approval flow. | CONN-26 | M |

---

## Integration & Distribution

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-13 | MCP server | Expose Maestro's API surface as an MCP server alongside the Unix socket HTTP API. Enables MCP-compatible AI clients to spawn players, read job state, post messages. ODX integration surface. | CONN-2k | H |
| MAESTRO-B-14 | Memory sync on player lifecycle | On player spawn: read collaborator memory from git-crypt repo. On player checkpoint/exit: push memory changes. Depends on OKD-7 portability implementation. | CONN-2m | H |
| MAESTRO-B-15 | Distribution (Homebrew + signed .app) | Signed macOS .app + Homebrew formula. Part of ux-direction.md Phase 4. | CONN-8 | M |
| MAESTRO-B-16 | Open-source release | Clean public repo (cleaved internal history). MCP server + Skills API are the compelling OSS differentiators. Gate on MAESTRO-B-13 + API stability. | CONN-2o | L |

---

## Utility & Conductor Tools

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-17 | Warn on unmerged commits before branch switch | Conductor-level guard: before any player is assigned a branch switch task, check `git log origin/main..HEAD` and surface unresolved commits as a blocking notification. | CONN-32 | M |
| MAESTRO-B-18 | Buffer read API | `GET /players/:id/buffer` — read a player's recent output programmatically. Used by Tester player, CRM Enricher, and cross-player search. | CONN-16, CONN-47 | M |
| MAESTRO-B-19 | `store.GetConductor()` — dedicated Conductor lookup | `HandleBlocked` (and any future path that needs the Conductor) currently scans all players via `ListPlayers()` and filters by `IsConductor`. Replace with a dedicated `GetConductor()` store method (`SELECT ... WHERE is_conductor = 1 LIMIT 1`). Cheap SQL; eliminates the scan pattern before it proliferates. | — | L |

---

## Notes

**CONN-39 (multi-model) and CONN-40 (approval delegation/away mode)** were marked
"Design — discuss with Kirt" in conn. Both have clean homes in Maestro:
- Multi-model: player spawn config includes `model` field; bus routes accordingly
- Away mode: `notifications` + approval inbox in the Conductor tab is the
  natural implementation surface

**CONN-2k (MCP server)** overlaps with the ODX Developer Platform roadmap.
MAESTRO-B-13 is the Maestro-native implementation; its design should be
coordinated with the broader ODX MCP server story.
