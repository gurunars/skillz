# Repo conventions

This repo is a collection of agent skills: every top-level directory with a
`SKILL.md` is one skill, published automatically by CI (`gh skill publish`)
on every push to master.

## Rules

- **Cross-references between markdown files must be clickable relative
  links**, never bare inline code. Write `` [`operator-catalog.md`](operator-catalog.md) ``,
  not `` `operator-catalog.md` ``. The docs double as human-browsable pages on
  GitHub, and inline-code references don't render as links there.

- **Every new skill gets a section in the root `README.md`**: a heading that
  links to the skill's `SKILL.md`, followed by a short teaser — a tweet-length
  hook written for a human skimming the repo, not a copy of the frontmatter
  `description` (which is written for model triggering).

## Kotlin code style

Applies to every Kotlin sample in the skills — smells and fixes alike:

- **No semicolons.** One statement per line; never compress a statement
  sequence onto a single line.
- **Braces on every control block.** `if` / `else` / `for` / `while` bodies
  always use `{}`, even for a single statement. `when` arms whose body is a
  bare expression (`-> value`) may stay braceless; arms containing statements
  or control flow get braces.
- **Fluent chains break at every dot.** When a call chain has more than one
  dot, the receiver goes on its own line and each dotted call on its own line:

  ```kotlin
  scannerApi
      .asFlow()
      .shareIn(scope, SharingStarted.WhileSubscribed(5_000), replay = 0)
  ```

  Single-dot calls stay on one line.
