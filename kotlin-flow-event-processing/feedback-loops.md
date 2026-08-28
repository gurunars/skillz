# Feedback: propagating output back to input

Read this before writing any code where the result of processing an event becomes a new event for the same processor.

## Contents

1. Classify the loop first
2. Level 1 — local sequential dependency
3. Level 2 — result selects next stage
4. Level 3 — a real cycle: the `runLoop` template
5. Invariants and why they hold
6. Choosing the execution policy
7. Termination
8. Testing
9. Anti-patterns

---

## 1. Classify the loop first

Most "feedback" is not a cycle. Ask: *does the output re-enter the same reducer/fold?*

| Shape | Example | Level |
|---|---|---|
| Step N+1's input is computed from step N's output, sequentially, in one place | pagination cursor, incremental backoff, multi-round handshake | 1 |
| The result of one stage decides which flow to run next, forward only | connect → authenticate → subscribe | 2 |
| A command produced by the reducer yields an event the *same* reducer must see | sync loop: `SyncRequested` → `Command.Sync` → `SyncSucceeded` → state `Idle` | 3 |

Only Level 3 needs the template. Do not build a Level 3 loop for a Level 1 or 2 problem; the cycle machinery is a cost, not a feature.

## 2. Level 1 — local sequential dependency

A `flow { }` builder with a local variable. The variable is not shared, not observable, and dies with the collection. This is not "hidden state"; it is a loop counter.

```kotlin
fun pages(fetch: suspend (cursor: String?) -> Page): Flow<Page> = flow {
    var cursor: String? = null
    do {
        val page = fetch(cursor)
        emit(page)
        cursor = page.next          // output feeds the next input, locally
    } while (cursor != null)
}
```

Backoff, polling-until-done, and "ask again with the token you just got" are all Level 1.

## 3. Level 2 — result selects next stage

The output of a stage is the input to the *next* stage, never to the same one. Chain `flatMapConcat` (or `flatMapLatest` if a new upstream value must abort the chain):

```kotlin
fun stream(links: Flow<Link>): Flow<Message> =
    links.flatMapLatest { link ->                                   // supersede: a new link aborts the old session
        link
            .open()                                                 // Flow<Session>, cancellation = close
            .flatMapConcat { session ->                             // result selects next stage
                session
                    .authenticate()
                    .map { session to it }
            }
            .flatMapConcat { (session, auth) ->
                session.subscribe(auth.grantedTopics)               // Flow<Message>
            }
    }
```

Each stage's result is visible in the lambda parameter of the next. There is no reducer, no channel, no cycle. `retryWhen` on any stage is still Level 2.

## 4. Level 3 — a real cycle: the `runLoop` template

Use when a pure reducer must see events that are produced by executing its own commands.

```kotlin
sealed interface Event {
    sealed interface External : Event      // from the outside world
    sealed interface Internal : Event      // produced ONLY by executing a Command
}

data class Step<S, C>(val state: S, val commands: List<C> = emptyList())

@OptIn(ExperimentalCoroutinesApi::class)
fun <S, C> runLoop(
    external: Flow<Event.External>,
    initial: S,
    reduce: (S, Event) -> Step<S, C>,                 // pure
    execute: (C) -> Flow<Event.Internal>,             // effects, as flows
): Flow<S> = channelFlow {
    // Delay guards. `feedback` is UNLIMITED so the executor can never block on it — see invariant 2.
    val feedback = Channel<Event.Internal>(Channel.UNLIMITED)
    val commands = Channel<C>(Channel.BUFFERED)

    // Executor. The ONLY place the effect-execution policy is chosen.
    launch {
        commands
            .receiveAsFlow()
            .flatMapConcat(execute)                   // queue: one command at a time, in order
            .collect { feedback.send(it) }
    }

    // Reducer. The ONLY place state lives.
    merge(external, feedback.receiveAsFlow())
        .runningFold(Step<S, C>(initial)) { step, e -> reduce(step.state, e) }
        .collect { step ->
            send(step.state)
            step.commands.forEach { commands.send(it) }
        }
    // Loop convergence: every Internal event must reduce to a Step whose commands
    // eventually stop producing Internal events. Exit is by cancellation (see §7).
}
```

Concrete reducer for a sync loop:

```kotlin
sealed interface SyncState {
    object Idle : SyncState
    data class Syncing(val queuedAgain: Boolean) : SyncState
}
sealed interface Cmd { object Sync : Cmd }

object SyncRequested : Event.External
object Online : Event.External
data class SyncFinished(val ok: Boolean) : Event.Internal

fun reduce(s: SyncState, e: Event): Step<SyncState, Cmd> = when (s) {
    SyncState.Idle -> when (e) {
        SyncRequested, Online -> Step(SyncState.Syncing(queuedAgain = false), listOf(Cmd.Sync))
        is SyncFinished -> Step(s)                                  // stale — ignore
    }
    is SyncState.Syncing -> when (e) {
        SyncRequested, Online -> Step(s.copy(queuedAgain = true))   // coalesce
        is SyncFinished -> if (s.queuedAgain) {
            Step(SyncState.Syncing(false), listOf(Cmd.Sync))
        } else {
            Step(SyncState.Idle)
        }
    }
}
```

Note that `Syncing` here is a **domain** state: it changes how `SyncRequested` is interpreted (coalesce vs start). It is not tracking whether a coroutine is alive — that is the executor's job. If `Syncing` carried no information that altered a transition, it would be a transit state and should be removed.

## 5. Invariants and why they hold

1. **Internal events are a distinct type.** The reducer's `when` is exhaustive over `Event`; every branch that handles an `Internal` event is visibly a feedback branch. A reader can enumerate the cycle's edges by grepping for `Event.Internal` implementors.

2. **The cycle is broken by a buffer, and the feedback buffer is unbounded.** Consider the alternative, `feedback = Channel(BUFFERED)`. The reducer may block on `commands.send` (commands buffer full while a long command runs); the executor may block on `feedback.send` (feedback buffer full because the reducer is blocked). Both wait on each other: deadlock. With `feedback` unbounded, the executor never blocks, so it always drains `commands`, so the reducer always unblocks. The memory cost is bounded by loop convergence (invariant 4), which you must guarantee anyway.

   Never use `MutableSharedFlow.tryEmit` for feedback: on overflow it silently returns `false` and the event is lost, violating fail-loud.

3. **The loop lives in one function.** Anyone can find the cycle by reading `runLoop`. Do not close the loop by having `execute` write into some `MutableSharedFlow` that is also merged upstream by another class — that is a hidden cycle, and it re-introduces the shared mutable state the whole design exists to remove.

4. **The loop converges.** Every `Internal` event must, within finitely many reductions, reach a `Step` with no commands. Write this down next to the reducer as a comment. If convergence depends on the external world (e.g. "retry until the server says OK"), bound it with `retryWhen` inside `execute`, not with a counter in `State`.

5. **The reducer is pure.** No `suspend`, no I/O, no `launch`, no clock. Anything that needs those becomes a `Command`. This makes the reducer trivially unit-testable and the policy decisions (what runs concurrently with what) live entirely in the executor.

## 6. Choosing the execution policy

The `flatMapConcat(execute)` line in the executor is the single design decision that determines concurrency semantics. Replace it deliberately and comment it:

| Executor line | Meaning | Typical use |
|---|---|---|
| `flatMapConcat(execute)` | Commands run one at a time, in order | Anything touching a single resource; default |
| `flatMapLatest(execute)` | A new command cancels the running one | "Latest request wins" fetches; the reducer must model cancellation as an `Internal` event or accept silence |
| `flatMapMerge(execute)` / `flatMapMerge(n, execute)` | Commands run concurrently | Independent I/O; results arrive out of order and the reducer must tolerate that |
| `flatMapLatestByKey({ it.key }, execute)` | Per-key supersede | One in-flight command per device/user/document |

If different command types need different policies, split into two executor coroutines each filtering its command type — do not put concurrency logic in the reducer.

## 7. Termination

`runLoop` never completes on its own: `feedback.receiveAsFlow()` is never closed, so `merge` never completes even after `external` does. This is deliberate — the loop's lifetime is its collector's scope, and cancellation is the only exit, which cancels the executor and any in-flight command atomically.

For a loop that should finish, terminate from the outside on a state predicate:

```kotlin
runLoop(...).takeWhile { it !is SyncState.Done }
```

or `first { it is SyncState.Done }` if you only want the terminal state.

## 8. Testing

- **Reducer**: plain function tests. `reduce(Idle, SyncRequested) == Step(Syncing(false), [Sync])`. No coroutines.
- **Loop**: `runTest` + a fake `execute` that returns `flowOf(SyncFinished(ok = true))`, collect with `toList()` after `takeWhile`, or with Turbine. Assert the sequence of states, not the timing.
- **Executor policy**: test by making `execute` a `flow` that `delay`s before it emits, and checking whether overlapping commands interleave, cancel, or queue.

## 9. Anti-patterns

| Anti-pattern | Why it fails | Fix |
|---|---|---|
| `reduce` launches the command | Effect escapes cancellation; reducer untestable | Return a `Command`, execute downstream |
| Feedback via a `MutableStateFlow` the executor `.update`s | Hidden cycle, two writers, lost events on conflation | `Channel` inside `runLoop` |
| `feedback = Channel(BUFFERED)` or `RENDEZVOUS` | Deadlock under backpressure (invariant 2) | `UNLIMITED` + convergence guarantee |
| `State.Executing(cmd)` variant | Transit state; duplicates what the executor already knows | Delete; executor policy handles in-flight |
| Same policy question answered in two places (a `Mutex` in `execute` *and* `flatMapConcat`) | Redundant, and the two can disagree | Policy lives only on the executor line |
| Level 3 loop for a pagination cursor | Ten times the code for a local `var` | Level 1 `flow { }` |
