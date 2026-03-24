---
name: rb-planning
description: >-
  Break down work into board cards for ralph-ban orchestration. This skill
  should be used when the user has a plan, design doc, or clear requirement
  to decompose into epics and tasks with EARS specs. Triggers on "plan this",
  "create cards", "break this down", "decompose into tasks", "rb-planning",
  "put this on the board", or when a design doc from /rb-brainstorm needs
  to become actionable work. Creates cards via bl CLI with specs, priorities,
  dependencies, and epic grouping.
argument-hint: "[plan, design doc path, or requirement description]"
---

# Board Planning

Break down $ARGUMENTS into epics and tasks on the bl board. The rb-orchestrator will dispatch workers from these cards, so every card must be self-contained and worker-ready.

## Phase 0: Design Preamble (conditional)

**Skip this phase when:**
- Input is a design doc from `rb-brainstorm` (already contains Architecture and Decisions sections)
- Input is already a task-level breakdown (lists specific files and changes)

**Run this phase when:**
- Input describes work at the requirement level (goals, behaviors) without a prior brainstorm

Steps:
1. Read the requirement or goal
2. Explore codebase context: read target files, trace existing patterns
3. Make architectural decisions: file structure, module boundaries, interfaces
4. Run `mkdir -p .agent-history/` and save decisions to `.agent-history/YYYY-MM-DD-<topic>-design.md`
5. Flow into Phase 1 with the architectural context

## Phase 1: Understand the Work

Read the plan, outline, or context provided. If $ARGUMENTS references files, read them. If it describes work verbally, use that directly.

Identify the codebase context:
- Read CLAUDE.md for architecture and conventions
- Check `bl ready` and `bl list --status backlog` for existing board state (never full `bl list` which dumps the large done column)
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

Specs are acceptance criteria. The bl CLI blocks the review transition until all specs are checked off. For notation patterns and examples, read `references/ears-guide.md`.

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

## Phase 5: Review Decomposition

Before creating cards, present the full plan to the user:
- List each epic and its tasks in tree form
- Show the dependency graph
- For each task: title, description summary, file scope, spec count
- Ask: "Does this breakdown look right? Any cards to add, remove, or restructure?"

Revise based on feedback. This catches decomposition problems before cards are created.

## Phase 6: Create Cards

Use bl to create the cards. Create epics first, then tasks with `--epic` and `--blocked-by` flags. **All cards start in backlog**, then promote to todo when specs and file scope are verified.

```
bl create "Epic title" --type epic -p1 --status backlog --description "..."
bl create "Task title" --epic <epic-id> -p1 --status backlog --blocked-by <dep-id> \
  --description "..." \
  --spec "When X, the system shall Y" \
  --spec "The module shall be registered as 'name' in Registry" \
  --spec "Unit test: <specific test scenarios>"
```

After creating cards with specs, promote to todo:

```
bl update <id> --status todo
```

## Quality Checklist

Before presenting the board, verify:

- [ ] Every non-epic task has 3+ specs
- [ ] Every task description names specific files
- [ ] No task touches more than 4 files
- [ ] Dependencies capture all data-flow relationships
- [ ] Research questions are in spike tasks, not mixed into implementation
- [ ] Priorities reflect actual ordering needs (P1 gates P2 work)
- [ ] When two tasks modify the same file, note the overlap in both descriptions so the orchestrator can plan merge order

After verification, run `bl ready --tree` and present the board to the user:
"Board ready for orchestrator dispatch?"
