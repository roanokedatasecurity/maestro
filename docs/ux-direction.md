# Maestro UX Direction

**Status:** Locked
**Authors:** Kirt Debique, Damon
**Created:** 2026-03-18

---

## Decision: Native App via Wails

Maestro ships as a native macOS application built with **Wails** (Go backend +
React/TypeScript frontend + embedded WebView). This replaces the BubbleTea TUI
model used in conn v1.

**Why native app over localhost web:**
- Single binary distribution — `brew install maestro` or a signed `.app`, no
  separate browser or server process for the user to manage
- OS window management for free — Cmd+Tab, Mission Control, Spaces, Spotlight
- Cohesive interaction model — Maestro is one thing, not a tab competing with
  other browser tabs for attention
- Lifecycle hooks (OnStartup, OnShutdown) map cleanly to SQLite open/close and
  session management
- Upgrade path to menu bar presence, global keyboard shortcut, Dock badge for
  pending approvals

**Why not BubbleTea:**
- Layout management is complex and fragile at conn v1 scale; it only gets harder
  as pane count grows
- BubbleTea owns the event loop, which fights Claude Code's own terminal handling
- Approval surfaces, notification inboxes, and multi-tab management are all
  dramatically simpler in React than in a TUI constraint model

---

## Terminal Emulation: xterm.js

Each Player tab and the Conductor tab embed an **xterm.js** terminal. This is a
full PTY-capable terminal emulator as a React component — used by VS Code,
GitHub Codespaces, Warp, and others.

**What this buys:**
- Real ANSI rendering, true color, scrollback, text selection — without fighting
  a TUI rendering model
- Clean separation: xterm.js owns output rendering; a separate React input
  component owns the command prompt area
- The input widget can have history, multiline support, and send affordances
  independent of the PTY stream
- Streaming output from Claude Code (or any process) maps directly to xterm.js
  write calls

---

## Communication: Two Channels, Two Consumers

Maestro exposes two communication surfaces that serve different consumers and
must both exist:

**1. Wails bindings (UI fast path)**
Go functions bound directly to the React frontend via the Wails runtime. Used
for all UI-initiated calls and for high-frequency data flows (streaming output
to xterm.js, real-time status updates). No socket overhead, no serialization
round-trip through the OS network stack.

**2. Unix socket HTTP API (external surface)**
The HTTP API defined in MAESTRO-5, served over `$MAESTRO_SOCKET`. This is the
contract for external consumers: CLI tooling, MCP server, ODX integrations,
other agents operating outside the Wails WebView. The UI does NOT use this path
for normal operations — that would add unnecessary latency.

These are not redundant. The Wails binding is the UI's private fast path. The
Unix socket API is the public integration surface. Both are built; MAESTRO-5
delivers the Unix socket API, and Phase 2 adds Wails bindings when the UI layer
begins.

---

## Layout: Conductor Tab + Player Tabs

The Maestro window has a persistent tab bar:

- **Conductor tab** — always present, always pinned leftmost. Contains the
  Conductor's xterm.js console, the notification inbox badge/panel, and the
  approval surface. This is Kirt's primary interaction pane.
- **Player tabs** — open when a Player is spawned, persist (as Dead) after the
  Player exits until explicitly dismissed. Tab label shows Player name + status
  indicator.
- **Tab overflow** — handled by the browser chrome natively (scroll, dropdown);
  not a layout constraint Maestro needs to manage in code.

The input widget lives at the bottom of each tab, separated visually from the
xterm.js output pane above it. The split is a React layout concern, not a PTY
concern — clean separation of rendering from input.

---

## Approval Surface

Approvals are a first-class UI element — not a line of text in a Player's PTY
output. When a pending approval exists:

1. The Conductor tab badge increments
2. Clicking the badge opens the approval panel (slide-over or modal)
3. The panel shows the scorecard, the declared work scope, and Approve/Reject
   controls
4. Decision is written to the `approvals` table; the blocked Player is unblocked

This interaction is impossible to design cleanly in a TUI. In React it is a
~30-line component.

---

## Durable State Is the Primary Value

Maestro's most important UX improvement over conn v1 is not session continuity
(detach/reattach) — it is **durable state**. When Maestro restarts:

- All Jobs and their statuses are known
- All Messages in the queue are intact
- All Notifications (read and unread) are preserved
- Dead-letter Jobs are surfaced for recovery

The user experience of launching Maestro after a restart should feel like
opening Slack after being offline — you know exactly what happened, you pick up
where you left off, nothing is lost.

True detach/reattach (keeping the Go process alive across a UI disconnect) is a
Phase 3 concern. It requires a separate window server process and re-attaching
xterm.js to a live PTY. That is a non-trivial problem. Durable state via SQLite
delivers 90% of the value with none of that complexity.

---

## Boot Sequence

See `boot-sequence.md` for the full boot flow design.

---

## Phase Plan

| Phase | Scope |
|-------|-------|
| Phase 1 | Platform layer: store → player → job → bus → API → wire-up (MAESTRO-1 through MAESTRO-6). No UI. `go test ./...` is the definition of done. |
| Phase 2 | Wails shell: React chrome, Conductor tab, xterm.js, Wails bindings. Connect to platform layer. |
| Phase 3 | Player tabs, approval surface, notification inbox, full multi-kid workflow. |
| Phase 4 | Menu bar, global shortcut, Dock badge, distribution (signed `.app`, Homebrew). |
| Later | Detach/reattach (persistent Go process, re-attachable PTY). |
