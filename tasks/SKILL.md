---
name: tasks
description: Manage this project's work items under tasks/ — create a task when work is planned or discussed, move it between planned/active/done/rejected, look up whether something already exists as a task, and block re-proposing rejected work without an explicit override. Use whenever the user proposes, discusses, starts, finishes or abandons a piece of work, or asks what is planned or in flight.
---

# Task management

Work lives in `tasks/` as one markdown file per item. The **folder is the status**, git
is the history — no status field, no timestamps, no changelog inside the file.

```
tasks/planned/    not started
tasks/active/     being worked on now
tasks/done/       finished
tasks/rejected/   decided against
```

## File format

Name: `{N}-{kebab-title}.md`, where **N is an arrival ordinal** — the next unused
integer across *all four* folders at the time the task is created.

- N is assigned once and **never reused, never renumbered**, not even when tasks are
  completed or rejected. It records when the task arrived, nothing else. Priority is a
  field precisely so that ordering never requires renaming files.
- Moving between folders never changes the name.

Frontmatter carries only what git cannot:

```markdown
---
priority: high | medium | low
size: S | M | L          # optional, rough effort
depends_on: [2, 4]       # optional, task numbers that must land first
rejection_reason: ...    # required in tasks/rejected/, omitted everywhere else
---
```

**Dependencies must stay acyclic.** `depends_on` names tasks that have to land before
this one can start — not "relates to", which is what the body is for. Before adding one,
walk the chain: if following `depends_on` from the new edge leads back to the task you
started from, the cycle is the real finding, and it means the two tasks are one task or
the dependency is aspirational. Say so rather than recording it. A task may depend on one
already in `done/`; that edge is satisfied, and worth keeping as the record of why the
order was what it was.

Body, in this order:

```markdown
# Title

## Why
The business case. What the user gets, and why it is worth doing at all. If this
section is hard to write, the task is probably not ready to plan.

## Technical notes
Research outcomes, constraints discovered, decisions already made, hazards. Anything
that would otherwise be rediscovered the expensive way. Link to `CLAUDE.md` for rules
rather than restating them.

## TODO
- [ ] Reasonably granular steps — each one a thing someone could sit down and do
- [ ] Tick them off in place as work lands

## Done when
The observable condition that ends the task.
```

## Lifecycle

Move with `git mv` so history follows the file:

```bash
git mv tasks/planned/7-drawn-routes.md tasks/active/7-drawn-routes.md
```

- **Work is discussed or planned** → create it in `tasks/planned/`. This includes ideas
  that arrive mid-conversation: if it is worth doing later, it is worth a file now.
- **Work starts** → move to `tasks/active/`.
- **Work finishes** → move to `tasks/done/`. Before moving, carry any rule or watch-out
  that still constrains future code into `CLAUDE.md`: the task holds intentions,
  `CLAUDE.md` holds rules.
- **Work is decided against** → move to `tasks/rejected/` and add `rejection_reason` to
  the frontmatter. Keep the body; the reasoning is the point.

Keep the TODO list current *while* working, not in a tidy-up afterwards. A task that
lags reality is worse than no task, because it is believed.

## Behaviours this skill owes the user

**Point at existing tasks.** Before answering a request to build something, check
whether it already exists:

```bash
ls tasks/*/ ; grep -ril "<topic>" tasks/
```

If it does, say so and name the path and folder — "that's `tasks/planned/3-alerts.md`"
— rather than silently starting fresh work or duplicating the entry. This is explicitly
wanted: the user will forget what has been filed and should be pointed back to it.

**Block rejected work.** If the user proposes something that lives in `tasks/rejected/`,
**stop and say so before doing any of it**. Quote the `rejection_reason`, then ask for
an explicit override. Do not begin implementing on the assumption that raising it again
implies reconsideration — the whole point of the folder is that these were decided, and
re-deciding should be deliberate. If the user does override, move the file back to
`tasks/planned/` (or `active/`), strip `rejection_reason`, and record in the body what
changed to justify the reversal.

**Do not silently renumber, merge or delete tasks.** Superseded work is rejected with a
reason pointing at what replaced it.

## Pre-finish checklist

- Anything proposed or discussed this session that is worth doing later has a file in
  `tasks/planned/`.
- Every status change was a `git mv`; no file was renamed or renumbered.
- The active task's TODO list matches what actually landed, ticked in place.
- Rules or watch-outs from a task moving to `done/` were carried into the host rules
  file before the move.
- Anything moved to `rejected/` carries a `rejection_reason`.
- No new `depends_on` edge creates a cycle.
