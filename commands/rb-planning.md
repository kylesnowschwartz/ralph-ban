---
description: Break down planned work into bl cards for ralph-ban orchestration
argument-hint: [plan-or-context]
allowed-tools: Read, Grep, Glob, Bash(bl:*), Bash(git:*)
---

# Board Planning

Break down $ARGUMENTS into epics and tasks on the bl board. The rb-orchestrator will dispatch workers from these cards, so every card must be self-contained and worker-ready.

## Phase 1: Understand the Work

Read the plan, outline, or context provided. If $ARGUMENTS references files, read them. If it describes work verbally, use that directly.

Identify the codebase context:
- Read CLAUDE.md for architecture and conventions
- Check `bl list --tree` for existing board state and naming patterns
- Identify which files and packages the work touches

## Phase 2: Structure into Epics and Tasks

Group related work into epics. Each epic is a theme (e.g., "New Widgets", "Rendering Improvements"). Tasks within an epic share a concern but can often be worked independently.

**Epic guidelines:**
- 3-8 tasks per epic (fewer means the epic is a task; more means split it)
- Epic description states the goal, not implementation details
- Epics are never dispatched to workers; they're organizational containers

**Task guidelines:**
- One task = one worker invocation = one commit
- If a task touches more than 3-4 files, it's probably too big
- If a task can't be described in 2-3 sentences, it needs decomposition
- If a task involves design choices (new APIs, data schemas, module boundaries),
  mark it for a Plan agent first. The orchestrator dispatches a Plan agent,
  converts the design decisions into EARS specs, then dispatches a worker.
  Workers should implement decided designs, not make architectural choices.

## Phase 3: Write Card Content

For each task, write all three parts: description, specs, and metadata.

### Description

The description is the worker's primary context. It must answer:
- **What** to build or change (concrete, not aspirational)
- **Where** in the codebase (name specific files and packages)
- **How** to verify it works (point to test patterns or existing examples)

Include reference file paths when prior art exists (e.g., `.cloned-sources/` projects, existing similar code in the repo). A worker with a good description and no other context should be able to complete the card.

End every description with:
`REMINDER: Read existing code patterns in the target package before implementing.`

### Specifications (EARS notation)

Specs are acceptance criteria. The bl CLI blocks the review transition until all specs are checked off. Write specs so a worker can read each one and unambiguously determine whether it's satisfied.

Use EARS (Easy Approach to Requirements Syntax) patterns:
- **Ubiquitous**: `The <system> shall <response>`
- **Event-driven**: `When <trigger>, the <system> shall <response>`
- **State-driven**: `While <precondition>, the <system> shall <response>`
- **Unwanted behavior**: `If <trigger>, then the <system> shall <response>`

Concrete specs (good):
- `When cost is nil, the widget shall return empty string`
- `The function shall be registered as 'cost' in widget.Registry`

Vague specs (bad):
- `Handle errors properly`
- `Implement the feature correctly`

Every task needs at minimum:
1. A spec naming the target file(s)
2. A spec for the happy path behavior
3. A spec for edge cases or empty/nil handling
4. A spec for tests

When a card introduces or changes constants, defaults, or magic numbers, add a
spec pinning the values: `"Default thresholds shall be: warn=60%, critical=80%"`.
Without this, a later card touching the same code may silently supersede the values.
The spec makes the change intentional and visible.

### Metadata

- **Priority**: P1 = must-have for the current goal, P2 = should-have, P3 = nice-to-have
- **Type**: `task` for implementation, `epic` for grouping
- **Dependencies**: `--blocked-by <id>` when task B needs task A's output
- **Epic membership**: `--epic <id>` to group under a parent

## Phase 4: Identify Dependencies

Map the dependency graph before creating cards:
- Data producers block data consumers (parse fields before building widgets that use them)
- Infrastructure blocks features (color detection before themes)
- Research spikes block conditional implementation

For research-dependent work, split into two cards:
1. **Spike** (P1): time-boxed investigation, output is findings in description
2. **Implementation** (P2, blocked by spike): conditional on spike results

## Phase 5: Create Cards

Use bl to create the cards. Create epics first, then tasks with `--epic` and `--blocked-by` flags.

```
bl create "Epic title" --type epic -p1 --description "..."
bl create "Task title" --epic <epic-id> -p1 --blocked-by <dep-id> \
  --description "..." \
  --spec "When X, the system shall Y" \
  --spec "The module shall be registered as 'name' in Registry" \
  --spec "Unit test: <specific test scenarios>"
```

After creating all cards, run `bl list --status todo --tree` and present the full board to the user for review.

## Quality Checklist

Before presenting the board, verify:

- [ ] Every non-epic task has 3+ specs
- [ ] Every task description names specific files
- [ ] No task touches more than 4 files
- [ ] Dependencies capture all data-flow relationships
- [ ] Research questions are in spike tasks, not mixed into implementation
- [ ] Priorities reflect actual ordering needs (P1 gates P2 work)
- [ ] When two tasks modify the same file, note the overlap in both descriptions so the orchestrator can plan merge order (file-scope overlap is acceptable — the orchestrator resolves conflicts during merge)

Present the tree view and ask: "Board ready for orchestrator dispatch?"
