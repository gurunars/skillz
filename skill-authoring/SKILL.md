---
name: skill-authoring
description: Author, review and refactor agent skills (SKILL.md directories in the agentskills.io format) — write descriptions that actually trigger, structure the body so it teaches instead of lectures, split reference material into linked sibling files, and decide what belongs in the skill versus the host CLAUDE.md/AGENTS.md. Use whenever creating a new skill, editing or reviewing a SKILL.md, extracting repeated guidance or a recurring correction into a skill, or deciding whether something should be a skill at all.
---

# Skill authoring

## What a skill is

One directory. A `SKILL.md` at its root with `name` and `description` frontmatter,
plus optional sibling markdown files for deep reference. The directory must be
liftable into another repository and still work: no paths into the host repo, no
reliance on host rules being loaded, its own trigger stated inside it.

## The description is the trigger — write it for recall

The frontmatter `description` is what the model sees *before* deciding to load the
skill. It has one job: fire at the right moment. The formula:

1. **What the skill teaches**, compressed to a clause — including its most
   opinionated claim, so the model knows what kind of guidance is inside.
2. **"Use whenever …"** — the situations, written in the *user's* vocabulary, not
   the skill's. List the words users actually say, including the ones that never
   name the topic: a Flow skill must fire on "state machine", "handler", "toggle",
   "manager"; a testing skill on "fix this bug", not just "write a test".
3. **The negative-space triggers** — "even if the user never mentions X at all".
   Most missed activations are tasks that are the topic without using its name.

The description is for the model, and it is the **only** place the trigger lives:
hosts catalogue installed skills automatically by injecting each description into
context, so restating the trigger in the body ("When to use this" sections) is
dead weight — the body is read after the decision to load has already been made.

A human-facing teaser (README index, catalog entry) is a different text with a
different job — never reuse one as the other.

## Opening: core rule first

The H1 is a plain human-readable name for the skill. Right after it, the first
screen states the core rule and the test that applies it — the single sentence a
reader should retain if they retain nothing else ("In-flight work is not state.
It is an operator."). Background and justification come after the rule, never
before it.

## The body teaches by contrast

- **Smell → fix pairs.** Show the hand-rolled version the reader was about to
  write, then the disciplined version. A table of smells with replacements
  outperforms prose describing virtues.
- **Concrete commands.** If the skill involves running anything, include the exact
  invocations ("What it runs"). A skill that says "run the tests" without the
  command teaches nothing.
- **Decision procedures, numbered.** Where the skill guides judgement, give the
  steps in the order they are applied, each one answerable on the spot.
- **Behaviours the skill owes the user.** If following the skill means proactively
  doing something — pointing at an existing task, blocking rejected work, asking
  before a destructive step — state it as an obligation, not a suggestion.
- **A pre-finish checklist** at the bottom, phrased so each item is checkable
  against the diff.

## Splitting: SKILL.md stays skimmable

`SKILL.md` is the always-loaded surface — keep it to the rules, the tables, and the
workflow. Deep material (full snippet catalogs, worked examples, edge-case essays)
goes into sibling files, each opened with a contents list, and referenced from
`SKILL.md` with **clickable relative links** — `` [`operator-catalog.md`](operator-catalog.md) ``,
not bare inline code — so the skill doubles as browsable documentation. Say *when*
to read each file ("read this before writing a Level-3 loop"), not just that it
exists.

## What stays out of the skill

Skills are loaded by judgement, so **anything that must never be violated belongs in
the host rules file** (`CLAUDE.md` / `AGENTS.md`), which is always in force. The
skill may restate such a rule for context but is never its home. Triggering needs no
host-side wiring either — the catalogue handles it; at most, a host may add a
one-line reminder in its rules file for a skill that must fire reliably, mirroring
the description.

Also out: anything the host repo already records (code structure, git history),
restated API documentation the model already knows, and aspirational process nobody
follows — a skill that lags reality is worse than none, because it is believed.

## Pre-finish checklist

- The description contains a "Use whenever" clause with the user's words, including
  triggers that never name the topic.
- The core rule and its test fit on the first screen.
- Every smell has a shown fix; every runnable step has its exact command.
- Obligations to the user are stated as obligations.
- Reference files are linked with clickable relative links and each says when to
  read it.
- The trigger lives only in the description; the body does not restate it.
- Nothing inviolable lives only in the skill; nothing in the skill restates what
  the host already enforces.
- The directory works when lifted into another repository unchanged.
