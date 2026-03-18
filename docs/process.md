# Maestro Development Process

## Standing commitments

### 1. Design decisions go in docs/

Any significant architectural decision, direction change, or trade-off gets captured
as a markdown file in `docs/` alongside the code. The IPC design doc
(`docs/ipc-design.md`) is the model — vocabulary section, locked decisions, open
questions worked through to closure. Don't let decisions live only in conversation
history or commit messages.

When to write a design doc:
- New subsystem or layer being introduced
- Non-obvious trade-off being made
- Design question that required discussion to resolve
- Anything a future contributor would need to understand *why*, not just *what*

### 2. README is a living artifact

The README is updated alongside code — not as an afterthought at PR time. It should
always hold a complete, digestible understanding of what's been built: architecture,
build order, key concepts, current state. A first-time reader should be able to
orient from README alone.

Update the README when:
- A new layer or package reaches working state
- The build order changes
- Key vocabulary or concepts are added or redefined
- The current state of the project changes meaningfully

### 3. Tests alongside, not after

Each platform layer gets a test suite written alongside the implementation. No layer
is considered complete without passing tests. The goal: `go test ./...` is always
green on main.

### 4. PR checklist

Before merging any MAESTRO-* PR:
- [ ] Is the relevant design doc updated or created?
- [ ] Does the README reflect the new state?
- [ ] Does `go test ./...` pass?
