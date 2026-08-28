# Operator catalog: smell → replacement

Each entry shows the hand-rolled version and the operator that replaces it. All snippets assume `kotlinx.coroutines` ≥ 1.7 and `@OptIn(ExperimentalCoroutinesApi::class)` where required.

## Contents

1. Supersede: cancel-and-restart (`flatMapLatest`)
2. Serialize: queue (`flatMapConcat`)
3. Drop-while-busy (`conflate` + `mapLatest`)
4. Bounded parallelism (`flatMapMerge`)
5. Retry (`retryWhen`)
6. Time (`debounce`, `sample`, `timeout`)
7. Deduplicate (`distinctUntilChanged`)
8. Gate (`first`, `takeWhile`, `dropWhile`)
9. Derive from several (`combine`)
10. Per-key lifecycle
11. Pure fold (`runningFold`)
12. Effects belong in the graph
13. Single writer, hot at the edge

---

## 1. Supersede: cancel-and-restart

**Smell**

```kotlin
sealed interface S { object Off : S; object TurningOn : S; object On : S; object TurningOff : S }
private var state: S = S.Off
private var job: Job? = null

fun setEnabled(on: Boolean) {
    when (state) {
        S.TurningOn, S.TurningOff -> return          // "busy", silently dropped
        S.On -> if (!on) { state = S.TurningOff; job = scope.launch { disconnect(); state = S.Off } }
        S.Off -> if (on) { state = S.TurningOn; job = scope.launch { connect(); state = S.On } }
    }
}
```

**Operator** — the desired value is a flow; the newest wins and cancels the previous inner flow:

```kotlin
fun connection(desired: Flow<Boolean>, connect: () -> Flow<Session>): Flow<Status> =
    desired
        .distinctUntilChanged()
        .flatMapLatest { on ->                           // supersede: newest desired state wins
            if (on) connect().map { Status.On(it) }.onStart { emit(Status.Connecting) }
            else flowOf(Status.Off)
        }
```

`connect()` is a flow whose collection *is* the connection and whose cancellation *is* the disconnect (`callbackFlow` with `awaitClose { close() }`). `TurningOff` never existed; it was cancellation.

Use `transformLatest` when the inner work emits several values and you want to write it imperatively; `mapLatest` when it produces exactly one.

## 2. Serialize: queue

**Smell**

```kotlin
private val mutex = Mutex()
fun handle(cmd: Command) = scope.launch { mutex.withLock { process(cmd) } }
// or: a Channel<Command> plus a hand-written `for (cmd in channel)` loop
```

**Operator**

```kotlin
fun results(commands: Flow<Command>): Flow<Result> =
    commands.flatMapConcat { cmd -> process(cmd) }   // queue: strictly in order, one at a time
```

If the producer must never suspend, add `.buffer(capacity)` upstream and decide overflow explicitly (`BufferOverflow.SUSPEND` / `DROP_OLDEST` / `DROP_LATEST`).

## 3. Drop-while-busy, keep the newest

**Smell**

```kotlin
private var pending: Request? = null
private var busy = false
fun submit(r: Request) { if (busy) { pending = r; return }; busy = true; scope.launch { run(r); busy = false; pending?.let(::submit) } }
```

**Operator**

```kotlin
requests
    .conflate()                     // while busy, keep only the latest
    .mapLatest { run(it) }          // and start it when free
```

or, at a terminal collector, `requests.collectLatest { run(it) }` if cancellation of the in-flight run is acceptable (that is *supersede*, not *conflate* — be deliberate).

## 4. Bounded parallelism

**Smell**: a `MutableList<Job>` with a `size < 4` check.

```kotlin
uploads.flatMapMerge(concurrency = 4) { upload(it) }   // parallel: at most 4 in flight
```

Order of results is not preserved. If it must be, use `flatMapConcat` or tag results with their input.

## 5. Retry

**Smell**: `attempts` field in the state, a `when` branch for `Retrying`.

```kotlin
fetch()
    .retryWhen { cause, attempt ->
        cause is IOException && attempt < 5 && run { delay(200L shl attempt.toInt()); true }
    }
```

The retry count lives in the operator's closure for the duration of one collection. It is not state.

## 6. Time

| Hand-rolled | Operator |
|---|---|
| `debounceJob?.cancel(); debounceJob = launch { delay(300); fire() }` | `.debounce(300.milliseconds)` |
| "emit at most once per second" | `.sample(1.seconds)` |
| "give up if no value in 5 s" | `.timeout(5.seconds)` (throws) or `mapLatest { withTimeoutOrNull(5.seconds) { … } }` |
| Heartbeat | `flow { while (true) { emit(Unit); delay(period) } }` merged as an input |

## 7. Deduplicate

`distinctUntilChanged()` or `distinctUntilChangedBy { it.id }`. Replaces any `if (value == last) return` field.

## 8. Gate

- `events.first { it is Ready }` — suspend until a condition; replaces a `waitingForReady` state.
- `takeWhile { it !is Terminal }` — complete the flow on a terminal event.
- `dropWhile { !initialized }` — ignore until ready.

## 9. Derive from several

**Smell**: fields `latestA`, `latestB`, each set from its own collector, plus a `recompute()`.

```kotlin
combine(a, b, c) { a, b, c -> derive(a, b, c) }.distinctUntilChanged()
```

`zip` when you need pairwise pairing instead of latest-of-each.

## 10. Per-key lifecycle

**Smell**: `private val jobs = mutableMapOf<Id, Job>()` with manual cancel-on-replace.

kotlinx.coroutines has no `groupBy`. Prefer the project's helper if one exists (e.g. `flatMapLatestOnKey`). If none, implement it **once**, as an operator, encapsulating the job map — this is the single acceptable home for a job map:

```kotlin
fun <T, K, R> Flow<T>.flatMapLatestByKey(key: (T) -> K, transform: (T) -> Flow<R>): Flow<R> = channelFlow {
    val jobs = HashMap<K, Job>()
    collect { value ->
        val k = key(value)
        jobs.remove(k)?.cancelAndJoin()
        jobs[k] = launch { transform(value).collect { send(it) } }
    }
}
```

Call sites then read `events.flatMapLatestByKey({ it.deviceId }) { …per-device flow… }` with no visible jobs.

## 11. Pure fold

**Smell**

```kotlin
private var state = Initial
fun on(e: Event) { state = when (e) { … }; if (state is Ready) scope.launch { … } }
```

**Operator**

```kotlin
fun reduce(s: State, e: Event): Step = …           // pure: no suspend, no launch, no I/O
val steps: Flow<Step> = events.runningFold(Step(Initial)) { step, e -> reduce(step.state, e) }
```

`runningFold` emits the initial value first; `scan` is its alias. The fold is the *only* place state lives. If the reducer wants to cause an effect, it returns a `Command` in `Step`; see `feedback-loops.md`.

## 12. Effects belong in the graph

**Smell**

```kotlin
events.onEach { scope.launch { sideEffect(it) } }.launchIn(scope)
```

The launched job is now outside the flow's cancellation and completion. Move it in:

```kotlin
events.flatMapMerge { sideEffectAsFlow(it) }   // or flatMapConcat/flatMapLatest — choose the policy
```

`onEach` is for cheap, synchronous, non-launching observation only (logging, `send` to a channel you own).

## 13. Single writer, hot at the edge

**Smell**: a `MutableStateFlow<UiState>` that three classes call `.update {}` on.

**Operator**: one pure function builds the cold flow; the scope owner makes it hot exactly once:

```kotlin
val uiState: StateFlow<UiState> =
    uiStates(intents, repository.items)                       // cold, pure, testable
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), UiState.Loading)
```

Tests collect the cold flow with `turbine` or `toList()`; no scope, no clock, no mutation.
