# beads-lite

This project uses beads-lite for task tracking. You MUST use it to track work.

## Required Workflow

1. Run `bl ready` at session start to see available work
2. When you discover new work, create a task: `bl create "description"`
3. When tasks depend on each other: `bl update <id> --blocked-by <blocker>`
4. When you complete work: `bl close <id>`

## Commands

```
bl ready              # what can I work on now?
bl ready --json       # machine-readable output
bl list               # all tasks
bl list --tree        # dependency visualization
bl create "title"     # new task
bl close <id>         # complete task (resolution: done)
bl close <id> --resolution wontfix   # close as won't fix
bl close <id> --resolution duplicate # close as duplicate
bl update <a> --blocked-by <b>       # a blocked by b
bl show <id>          # task details
bl show <id> --tree   # show issue with dependency subtree
bl list --status done --resolution wontfix  # filter by resolution
```

## Closing Tasks

When closing tasks, specify WHY with --resolution:
- `done` (default): Work completed successfully
- `wontfix`: Intentionally rejected (document reasoning in description)
- `duplicate`: Duplicate of another issue

Use `bl list --status done --resolution wontfix` to review rejected ideas.

## Multi-Agent Claiming

When multiple agents share a database, use atomic claiming to avoid duplicate work:

```
bl claim <id> --agent <name>   # atomically claim (fails if already claimed)
bl unclaim <id>                # release claim
bl ready --unclaimed           # only show tasks no one has claimed
bl ready --assigned-to <name>  # show tasks assigned to specific agent
bl list --assigned-to <name>   # list tasks assigned to specific agent
```

## Epic Workflow

Epics group related tasks. Use `--epic` to link tasks under epics (non-blocking).
Use `--blocked-by` only for real work dependencies.

```
# Create epic to track a feature
bl create "User authentication" --type epic

# Create tasks linked to the epic
bl create "Add login endpoint" --epic <epic-id>
bl create "Add session storage" --epic <epic-id>
bl create "Add logout endpoint" --epic <epic-id>

# Link existing tasks to an epic
bl update <task-id> --epic <epic-id>

# If tasks have real work dependencies, add blockers
bl update <logout-id> --blocked-by <login-id>

# View tree (epics show children)
bl list --tree
bl ready --tree

# Close tasks as completed, close epic when feature is done
bl close <epic-id>
```

IMPORTANT: Always link tasks to their parent epic with `--epic <id>`.
Epics without linked children are invisible in tree views.

## Rules

- Always check `bl ready` before starting work
- Create tasks for any new work you discover
- Link tasks to their parent epic with `--epic <id>`
- Close tasks when complete - this unblocks dependent tasks
- Use `--json` flag when you need to parse output programmatically
