---
name: kotlin-flow-event-processing
description: Write and review Kotlin event-processing code using kotlinx.coroutines Flow operators instead of hand-rolled state machines, and structure output→input feedback loops explicitly. Use this skill whenever Kotlin/KMP code handles events, commands, streams, device or connection lifecycles (connect/disconnect, on/off, start/stop, enable/disable), UI intents, retries, debouncing, queues, "processing" flags, or anything involving Flow, Channel, StateFlow, SharedFlow, or coroutines that react to inputs over time — even if the user says "state machine", "handler", "controller", "manager", "toggle", "lifecycle", or never mentions Flow at all. Also use when reviewing or refactoring existing Kotlin code that has states like TurningOn/TurningOff/Processing/Pending, `isBusy` flags, `Job?` fields, or Mutex-guarded handlers. Also use when bridging callback/listener-based APIs (SDK listeners, BLE, sockets, platform callbacks) to or from Flow — callbackFlow, suspendCancellableCoroutine, asFlow() adapters.
---

# Flow-first event processing

## The core rule

**In-flight work is not state. It is an operator.**

Hand-written state machines in reactive Kotlin code almost always contain two kinds of states mixed together:

1. **Domain states** — facts about the world that persist across events and change how future events are interpreted (`Anonymous` vs `Authenticated(token)`, `Draft` vs `Submitted`). These are legitimate.
2. **Transit states** — `TurningOn`, `TurningOff`, `Connecting`, `Processing`, `Pending`, `isBusy = true`, `job?.isActive`. These exist only because a suspending call is currently running. They are the smell.

Transit states are the coroutine machinery re-implemented by hand, with mutable fields, races, and a `when` that grows every sprint. kotlinx.coroutines already has the machinery: `flatMapLatest` *is* "cancel the in-flight thing and start the new one", `flatMapConcat` *is* "queue it", `conflate` *is* "drop intermediate requests", `retryWhen` *is* the retry counter. Use them.

The test to apply to every state variant and every flag: **"Does this exist because of the domain, or because a suspend call is running?"** If the latter, delete it and pick an operator.

Reporting a transient status to a UI (`Connecting…`) is fine — *derive* it with `onStart { emit(Connecting) }` on the inner flow. The smell is *steering* on it: `if (state == TurningOn) return`.

## Workflow

When writing new event-processing code:

1. **Identify the input flows.** Everything that arrives over time is a `Flow<Event>` (or a `Flow<Boolean>`, `Flow<Command>`). Callback interfaces are the same IO events in a different calling convention — convert them once at the boundary with an `asFlow()` extension on the callback-bearing type (`callbackFlow` inside; one-shot callbacks become suspend extensions instead), per [`callback-interfaces.md`](callback-interfaces.md). Never a method like `fun onToggle(desired: Boolean)` that mutates a field.
2. **Identify the persistent domain state**, if any. Often there is none — the output is just a transform of inputs. If there is, it is a fold: `runningFold` / `scan` over events with a **pure** reducer. Nothing suspends inside the reducer; nothing launches.
3. **Map each transit behavior to an operator** using the table below. Write the chosen policy as a one-line comment at the call site (`// supersede: latest desired state wins`), because the choice between `flatMapLatest`, `flatMapConcat`, and `flatMapMerge` is the actual design decision and the reader must not have to infer it.
4. **If the output feeds back into the input**, build the cycle explicitly per [`feedback-loops.md`](feedback-loops.md). Never close the loop through a shared `MutableStateFlow` that some other class writes into.
5. **Expose the result** as a cold `Flow<State>` from a pure function, and let the caller decide hotness with `stateIn` / `shareIn`. The function signature should read like a dataflow graph: inputs in, flow out.
6. **Run the checklist** at the bottom before finishing.

When reviewing or refactoring existing code: list every transit state and flag found, name the operator that replaces each, then rewrite. Show before/after.

## Smell → operator

| You are about to write… | Use instead | Policy it encodes |
|---|---|---|
| `TurningOn`/`TurningOff`, `Connecting`, cancel-and-restart logic | `flatMapLatest` / `transformLatest` / `mapLatest` | Latest input supersedes; in-flight work is cancelled |
| `isProcessing` guard, `Mutex` around a handler, manual queue `Channel` + loop | `flatMapConcat` (or `buffer` upstream) | Inputs are serialized in order |
| `if (job?.isActive) return` (drop while busy) | `conflate()` then `mapLatest`, or `collectLatest` | Keep only the newest pending input |
| Fan-out N concurrent jobs, `Job` list | `flatMapMerge(concurrency = n)` | Parallel with bounded concurrency |
| Retry counter / backoff fields in state | `retryWhen { cause, attempt -> ... }` | Retry policy lives with the flow |
| Timer `Job` fields, "wait 300ms then…" | `debounce`, `sample`, `timeout`, `withTimeoutOrNull` inside `mapLatest` | Time is an operator input |
| `lastValue` field to skip duplicates | `distinctUntilChanged()` | Idempotent input |
| "Waiting for X" state | `first { }`, `filter`, `takeWhile`, `dropWhile` | Gate on a predicate |
| Fields caching the latest of several inputs | `combine(a, b, c) { ... }` | Derived value from latest of each |
| Map of `Job` keyed by id | A per-key operator (project helper like `flatMapLatestOnKey`, or `groupBy` implemented once via `channelFlow`) | Independent lifecycle per key |
| `var state` + `when(event)` + mutation | `runningFold(initial) { s, e -> reduce(s, e) }` | Pure fold; the only place state lives |
| `scope.launch { }` inside `onEach` / `map` | Move the effect into the `flatMap*` stage | Effects are part of the graph, cancellable with it |
| `MutableStateFlow` mutated from several places | One producer, `stateIn` at the edge | Single writer, no hidden coupling |

Full snippets for each row: [`operator-catalog.md`](operator-catalog.md).

## Callback interfaces: convert at the boundary

A callback interface is an event stream delivered through a vtable — same IO events, different calling convention. Convert exactly once, at the outermost boundary, as an extension on the callback-bearing type; everything past it is Flow:

- **Repeating callbacks** → `fun TheApi.asFlow(): Flow<TheEvent> = callbackFlow { ... }`. One interface → one sealed event type; `register` on collect, `awaitClose { unregister }`; error callback → `close(cause)`.
- **One-shot callbacks** → a suspend extension via `suspendCancellableCoroutine` with `invokeOnCancellation`. Never a single-emission Flow.
- **Bidirectional sessions** (callbacks in, method calls out) → one extension taking the outgoing `Flow` as a parameter; the writer is a collector inside the same `callbackFlow` lifetime.
- The extension is dumb: it translates and manages registration lifetime, nothing else. Overflow policy is chosen explicitly at the seam; sharing one expensive registration is `shareIn` at the edge, never a listener list inside the adapter.

Full patterns, anti-patterns, and tests: [`callback-interfaces.md`](callback-interfaces.md).

## Feedback: output → input

Most loops are not really loops. Classify before reaching for a cycle:

- **Level 1 — local sequential dependency** (pagination, cursor, handshake step N depends on N-1): a `flow { }` builder with a local variable. No shared state, no cycle.
- **Level 2 — result selects next stage**: `flatMapConcat { result -> next(result) }`. Still acyclic; the "feedback" is just data flowing forward.
- **Level 3 — real cycle** (command results are new events for the same reducer): explicit `runLoop` with a reducer producing `Step(state, commands)`, an executor coroutine, and buffered channels as the delay guard.

Only Level 3 needs the full template. Read [`feedback-loops.md`](feedback-loops.md) before writing one — it covers the template, the deadlock invariant, and why the effect-execution policy (`flatMapConcat` vs `flatMapLatest` vs `flatMapMerge`) is chosen in exactly one place.

Invariants for any Level 3 loop:
- Feedback events are a distinct type (`Event.Internal`) so the reducer's `when` is exhaustive and it is obvious which branches come from the loop.
- The cycle is broken by a buffer (`Channel`), never by synchronous re-emission into the same collector.
- The loop is visible in one function. Anyone can find the cycle by reading `runLoop`.
- Every command must eventually produce an event that yields no further command (the loop converges), or it must be cancelled by scope. State this in a comment.

## Worked example

[`worked-example.md`](worked-example.md) shows a device connection controller written both ways: 90 lines of `TurningOn/TurningOff` + `Job?` + races, versus 12 lines of `distinctUntilChanged().flatMapLatest { }`. Read it when you want to see the shape of the refactor, or to calibrate what "done" looks like.

## When a state machine is actually right

Keep an explicit state type when the state is a **domain fact** that changes how the next event is interpreted and there are more than two such facts with non-trivial transitions (a payment lifecycle, a multi-step auth handshake with distinct recovery paths). Even then:

- The reducer is a pure function `(State, Event) -> Step(State, List<Command>)`.
- Commands are data. Execution happens downstream in a `flatMap*` stage, never inside the reducer.
- Transit states still do not appear in `State`. "Waiting for the payment provider" is a command in flight, not a state.

If the project already has a Flow-based FSM library or custom operators (`Machine.run`, `flatMapLatestOnKey`, `withSideEffects`), use them rather than re-deriving the pattern. Check for them first.

## Pre-finish checklist

- No state variant or flag exists solely because a suspend call is running.
- No `var` holding state outside a `runningFold`/`scan` reducer (or a `flow {}` builder's local loop).
- Callback interfaces are converted once at the boundary via `asFlow()` / suspend extensions; no wrapper classes implementing listeners, no listener fields, no logic in callback bodies, every `callbackFlow` has `awaitClose`.
- No `Job?` fields. No `Mutex` guarding a handler. No `launch` inside `onEach`/`map`.
- Every `flatMap*` has a one-line comment naming the policy (supersede / queue / parallel).
- Reducers are pure: no suspension, no I/O, no launching.
- Cycles are explicit, buffered, and live in one function with a convergence comment.
- Public API is a pure function returning a cold `Flow`; hotness (`stateIn`/`shareIn`) is applied at the edge by the owner of the scope.
- `@OptIn(ExperimentalCoroutinesApi::class)` is applied (or configured project-wide) for `flatMapLatest`/`flatMapConcat`/`flatMapMerge`/`transformLatest` — do not avoid these operators because of the annotation.
