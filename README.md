# skillz

Agent skills in the [agentskills.io](https://agentskills.io) format, for
coding agents — Claude Code, Gemini CLI, Codex, Cursor, and friends.

Install any of them with:

```sh
gh skill install gurunars/skillz <skill-name> --agent <claude-code|gemini|codex|cursor|antigravity>
```

## Skills

### [skill-authoring](skill-authoring/SKILL.md)

How to write skills that actually fire. Descriptions tuned for recall (trigger
on the user's words, not the topic's name), the core rule on the first screen,
smell → fix tables over prose, and a clean split between the skill and the host
rules file. The meta-skill this repo is built with.

### [tasks](tasks/SKILL.md)

File-based task tracking where the folder *is* the status: `planned/`,
`active/`, `done/`, `rejected/` — git is the history, no timestamps, no
changelog. Comes with teeth: the agent must point at existing tasks instead of
duplicating them, and must refuse re-proposed rejected work until you
explicitly override.

### [kotlin-flow-event-processing](kotlin-flow-event-processing/SKILL.md)

Stop hand-rolling state machines in Kotlin. `TurningOn`, `isBusy`, `Job?` —
that's coroutine machinery reimplemented by hand, races included. In-flight
work is not state, it's an operator: delete the flags and pick between
`flatMapLatest`, `flatMapConcat`, and friends. Covers callback-to-Flow
bridging and explicit feedback loops too.
