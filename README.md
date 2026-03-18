# Maestro

AI orchestration platform. Conductor + Players, durable message bus, persistent job tracking.

## Architecture

Maestro is a multi-player AI orchestration runtime built around three core ideas:

- **Conductor** — the parent agent. Authorizes all work, has full visibility, routes signals. No Conductor, no session.
- **Players** — execute Assignments issued by the Conductor. Report Done, Blocked, or Background. Never communicate laterally.
- **Message Bus** — durable, priority-queued, SQLite-backed. Producers fire-and-forget. The infrastructure owns delivery.

See [`docs/ipc-design.md`](docs/ipc-design.md) for the full design.

## Build order (platform first, UI later)

```
internal/store/    SQLite schema + migrations
internal/player/   Player model, status state machine
internal/job/      Job lifecycle (InProgress → Complete | Backgrounded | DeadLetter)
internal/bus/      Message bus, routing rules, delivery engine
internal/api/      Unix socket HTTP server (IPC endpoints)
cmd/maestro/       Main entry point
```

Tests are written alongside each layer. No UI work begins until platform has a green `go test ./...`.
