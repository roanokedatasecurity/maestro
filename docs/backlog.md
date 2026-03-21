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
| MAESTRO-B-6 | Click-to-focus player tab | Mouse click switches active player. Trivial in React — no BubbleTea focus model to fight. | CONN-27 | M |
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
| MAESTRO-B-16 | Open-source release | Clean public repo (cleaved internal history). MCP server + Skills API are the compelling OSS differentiators. Gate on MAESTRO-B-13 + API stability. | CONN-2o | M |

---

## Utility & Conductor Tools

| ID | Title | Description | Source | Priority |
|----|-------|-------------|--------|----------|
| MAESTRO-B-17 | Warn on unmerged commits before branch switch | Conductor-level guard: before any player is assigned a branch switch task, check `git log origin/main..HEAD` and surface unresolved commits as a blocking notification. | CONN-32 | M |
| MAESTRO-B-18 | Buffer read API | `GET /players/:id/buffer` — read a player's recent output programmatically. Used by Tester player, CRM Enricher, and cross-player search. | CONN-16, CONN-47 | M |
| MAESTRO-B-19 | `store.GetConductor()` — dedicated Conductor lookup | `HandleBlocked` (and any future path that needs the Conductor) currently scans all players via `ListPlayers()` and filters by `IsConductor`. Replace with a dedicated `GetConductor()` store method (`SELECT ... WHERE is_conductor = 1 LIMIT 1`). Cheap SQL; eliminates the scan pattern before it proliferates. | — | M |
| MAESTRO-B-21 | Keybinding system | conn key bindings equivalent for Maestro — pane focus, player minimize/restore, notification drawer, approval actions. Configurable; must not conflict with xterm.js PTY passthrough. | #kirt-todo 2026-03-20 | H |
| MAESTRO-B-22 | Contextual help system | Sophisticated help surface — contextual panel, command palette (`cmd+k`), onboarding overlays, in-app docs. Help adapts to current focus and state. | #kirt-todo 2026-03-20 | H |
| MAESTRO-B-20 | Conductor context window pressure detection + graceful refresh | Maestro's durable SQLite state solves process recovery, but does not address LLM context window compaction in long-lived Conductor sessions. As context fills, reasoning quality degrades and compaction risk rises. Maestro should: (1) track an approximate context pressure signal (message count, token estimate, or elapsed session time); (2) surface a low-friction checkpoint prompt to the Conductor when pressure is high ("context is getting heavy — good time for a refresh?"); (3) on checkpoint: write a structured session summary (active jobs, recent decisions, open items) to the SQLite `jobs` or `notifications` table so the next Conductor session boots with a synthesized brief rather than empty context. Distinct from CONN-42 (absorbed): that was about process state recovery; this is about LLM cognitive continuity across context boundaries. | OKD session 2026-03-20 | H |
| MAESTRO-B-23 | Parameterized Player Catalog | A typed, versioned catalog of reusable player specs. Each entry: metadata, a structured parameter schema, and a spawn prompt template. Conductor picks a player from the catalog by name and passes session params; catalog merges with persistent user preferences at spawn time. Stored as YAML in `~/.maestro/players/` (or repo `docs/players/`). Parameter model: structured fields (precise, versionable) + optional `notes` freeform catch-all string (handles long-tail preferences without schema updates). Enables repeatable, composable player patterns — flight-finder is the prototype. | OKD session 2026-03-20 | H |
| MAESTRO-B-24 | Browser WebView Overlay | A contained browser surface (Wails WebView) that a player can open for web-based human-in-the-loop flows. Player signals `blocked` with `type: "browser"` and a `url` payload; Maestro opens the WebView to that URL. Human completes the flow (booking, OAuth, payment, form) inside the overlay; player receives confirmation and continues. Completion signal options: URL pattern match (redirect to confirmation page), explicit "Done" button in overlay chrome, or DOM selector. Prototype use case: flight-finder presents ranked options → human picks one → browser overlay opens Alaska Airlines booking page → human completes payment → player logs itinerary and adds to calendar. IPC endpoint: `POST /players/:id/blocked` with `{"type":"browser","url":"...","completion":{"url_pattern":"..."}}`. | OKD session 2026-03-20 | H |
| MAESTRO-B-25 | Secure Terminal Input Overlay | A distinct overlay for shell-level human input that must pass through to a running player PTY — passwords, sudo prompts, 2FA codes, passphrases. Player signals `blocked` with `type: "secure-terminal-input"` and a prompt string; Maestro renders a masked input field in the UI; keystrokes are forwarded to the player's xterm.js PTY. Does not expose input to Conductor context. Architecturally separate from MAESTRO-B-24 (Browser WebView): this is PTY passthrough, not a web surface. IPC endpoint: `POST /players/:id/blocked` with `{"type":"secure-terminal-input","prompt":"Enter sudo password:","mask":true}`. | OKD session 2026-03-20 | H |
| MAESTRO-B-26 | Shared + Portable AI Collaborator Memory | A portable storage system for AI collaborator memory and toolboxes using `git-crypt` + GPG. Three layers: (1) shared org-wide memory space, (2) private portable memory per collaborator that travels across sessions/machines, (3) collaborator toolbox — reusable scripts/utilities co-located with memory. Architecture proposal in `memory/okd-7-ai-memory-architecture.md`. First collaborators: Damon + Megatron. Self-onboarding pattern documented for Veda, Chip, etc. | OKD-7 | H |

---

## Notes

**CONN-39 (multi-model) and CONN-40 (approval delegation/away mode)** were marked
"Design — discuss with Kirt" in conn. Both have clean homes in Maestro:
- Multi-model: player spawn config includes `model` field; bus routes accordingly
- Away mode: `notifications` + approval inbox in the Conductor tab is the
  natural implementation surface

| MAESTRO-B-27 | External deep research agent enablement | Design how Maestro enables players that are external deep research agents (e.g. agents that perform multi-step web research, competitor analysis, customer intelligence gathering). Covers: player profile type for research agents, how long-running research jobs surface progress + results, integration with Conductor scratchpad protocol, and whether Maestro needs a dedicated "research" player catalog entry. Source: #kirt-todo 2026-03-20. | #kirt-todo 2026-03-20 | M |

**CONN-42 absorbed note:** CONN-42 was absorbed on the basis that durable SQLite state eliminates the need for a checkpoint sentinel for *process recovery*. MAESTRO-B-20 addresses the separate concern of *LLM context window pressure* — which durable state does not solve.

**CONN-2k (MCP server)** overlaps with the ODX Developer Platform roadmap.
MAESTRO-B-13 is the Maestro-native implementation; its design should be
coordinated with the broader ODX MCP server story.
