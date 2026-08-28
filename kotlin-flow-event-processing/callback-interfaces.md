# Callback interfaces: `asFlow()` at the boundary

A callback interface is an event stream delivered through a vtable instead of a channel. `onFound(device)` / `onLost(device)` / `onError(e)` *is* `Flow<ScanEvent>` — same IO events, different calling convention. Potato, potahto. So the rule is:

**Convert at the boundary, exactly once, via an `asFlow()` extension on the callback-bearing type. Everything past the boundary is Flow.**

```kotlin
scannerApi.asFlow()                    // Flow<ScanEvent>
    .filterIsInstance<ScanEvent.Found>()
    .flatMapLatest { ... }
```

The extension is **dumb** — it translates callbacks to events and ties the registration lifecycle to collection, nothing else. No filtering, no dedup, no state, no decisions inside a callback body; those are operators and they live downstream in the graph.

## Contents

1. The `asFlow()` extension
2. Naming when one interface hides several streams
3. Buffering and backpressure at the seam
4. One-shot callbacks → `suspendCancellableCoroutine`
5. Sharing one registration (`shareIn`)
6. Bidirectional sessions (callbacks in, method calls out)
7. Anti-patterns
8. Testing

---

## 1. The `asFlow()` extension

**Smell** — a class implements the listener and republishes through a hot flow:

```kotlin
class Scanner(private val api: ScannerApi) : ScanListener {
    private val _events = MutableSharedFlow<ScanEvent>(extraBufferCapacity = 16)
    val events: SharedFlow<ScanEvent> = _events

    init { api.register(this) }                     // registered forever, hot forever
    override fun onFound(d: Device) { _events.tryEmit(ScanEvent.Found(d)) }   // drops on overflow, silently
    override fun onLost(d: Device) { _events.tryEmit(ScanEvent.Lost(d)) }
    override fun onError(e: Throwable) { /* now what? */ }
}
```

**Boundary extension**

```kotlin
sealed interface ScanEvent {
    data class Found(val device: Device) : ScanEvent
    data class Lost(val device: Device) : ScanEvent
}

fun ScannerApi.asFlow(): Flow<ScanEvent> = callbackFlow {
    val listener = object : ScanListener {
        override fun onFound(d: Device) { trySend(ScanEvent.Found(d)) }
        override fun onLost(d: Device) { trySend(ScanEvent.Lost(d)) }
        override fun onError(e: Throwable) { close(e) }      // error → flow fails, loudly
        override fun onStopped() { close() }                 // terminal callback → normal completion
    }
    register(listener)
    awaitClose { unregister(listener) }                      // cancellation = unregistration
}
```

Rules, each of which the smell violates:

- **It is an extension on the callback-bearing type, not a wrapper class.** No `Scanner` object to construct, hold, and dispose — the receiver already exists and `asFlow()` is a pure derivation from it. Call sites read as dataflow: `scannerApi.asFlow().flatMapLatest { ... }`.
- **One listener interface → one sealed event type.** The interface's methods were always a union spread across the vtable; make the union explicit so downstream `when`s are exhaustive. Do not write one extension per method and `merge` — that tears one registration into several and loses relative ordering across methods.
- **Registration lifetime = collection lifetime.** `register` runs when collection starts; `awaitClose` unregisters on cancellation *or* close. No listener fields, no `init` registration, no `start()`/`stop()` pair to call in the right order — the collector's scope is the lifecycle. `awaitClose` is mandatory; `callbackFlow` throws without it.
- **Error callbacks call `close(cause)`, terminal callbacks call `close()`.** The failure becomes a flow failure that `retryWhen`/`catch` downstream can handle. Never swallow the error callback, and never smuggle it through as a normal event unless the domain genuinely treats it as one.
- **Callbacks may arrive on any IO thread.** `trySend` is thread-safe; that is the whole point of converting here. No `synchronized`, no `runBlocking { send(...) }` inside a callback.
- The result is cold. Each collection registers its own listener; if the underlying API can't support that, share at the edge (§5) — don't make the extension hot.

## 2. Naming when one interface hides several streams

`asFlow()` is the right name when the receiver has one obvious stream. When an API genuinely carries independent streams (e.g. connection state *and* incoming data with unrelated consumers), give each extension a named variant — still extensions, still one registration each:

```kotlin
fun PlayerApi.progressAsFlow(): Flow<Progress> = callbackFlow { ... }
fun PlayerApi.trackChangesAsFlow(): Flow<Track> = callbackFlow { ... }
```

The test for splitting: would any consumer of stream A ever care about ordering relative to stream B? If yes, they are one stream — one `asFlow()`, one sealed type. If no, separate extensions are fine and each keeps its own registration lifecycle.

## 3. Buffering and backpressure at the seam

Callbacks cannot suspend, so the seam has a buffer (`callbackFlow` defaults to `Channel.BUFFERED`, capacity 64) and `trySend` fails when it is full. Decide the overflow policy explicitly, per source:

| Events are… | Do | Why |
|---|---|---|
| Must-not-lose (protocol frames, state transitions) | `callbackFlow { ... }.buffer(Channel.UNLIMITED)`, or check `trySend(...).isSuccess` and `close(...)` on failure | A silent drop is a corrupted stream; fail loud or don't fail |
| Latest-wins (sensor readings, positions) | `.conflate()` right after the `callbackFlow` | Intermediate values have no meaning |
| Bursty but bounded | keep the default buffer, comment the assumption | 64 is a policy too — say so |

This is the same decision as "queue" vs "conflate" in the operator table, made once at the seam. Do not make it twice (a bigger buffer here *and* a `conflate` downstream) — one place, one comment.

## 4. One-shot callbacks → `suspendCancellableCoroutine`

If the callback fires **exactly once**, it is not a stream and must not become one. A `Flow` that emits a single result is a `suspend` function wearing a costume. Same convention: an extension on the callback-bearing type.

```kotlin
suspend fun Api.connectAwait(): Session = suspendCancellableCoroutine { cont ->
    val call = connect(object : ConnectCallback {
        override fun onSuccess(s: Session) = cont.resume(s)
        override fun onFailure(e: Throwable) = cont.resumeWithException(e)
    })
    cont.invokeOnCancellation { call.cancel() }              // cancellation reaches the IO layer
}
```

- `invokeOnCancellation` is the `awaitClose` of the one-shot world: without it, cancelling the coroutine leaks the in-flight request.
- Success/failure map to return/throw — never to a `Result` wrapper or a nullable, unless the failure is a domain value.
- The classification is per *invocation*, not per interface: a callback with `onNext` fires many times (→ `asFlow()`); one with `onResult`/`onError` fires once (→ suspend extension), even if they ship in the same SDK.

These suspend extensions then enter the graph through the usual doors: `mapLatest { api.connectAwait() }`, `flow { emit(api.connectAwait()) }`, or as a stage in a Level-2 chain.

## 5. Sharing one registration (`shareIn`)

If the underlying registration is expensive or the API allows only one listener (BLE scans, location updates, socket accept loops), do **not** solve it inside `asFlow()` with a listener list. The extension stays cold and single-collector; the owner of the scope shares it at the edge, exactly like `stateIn`:

```kotlin
val scans: SharedFlow<ScanEvent> =
    scannerApi.asFlow().shareIn(scope, SharingStarted.WhileSubscribed(5_000), replay = 0)
```

`WhileSubscribed` gives you reference-counted registration for free: the first collector registers, the last one leaving unregisters. That is the hand-rolled `listeners.isEmpty() -> api.unregister()` bookkeeping, deleted.

## 6. Bidirectional sessions (callbacks in, method calls out)

Session-shaped APIs — a socket, a GATT connection, a media player — have callbacks (events in) *and* methods (commands out). Stitch both directions in one extension, so one lifetime owns the resource, the writer, and the registration. The outgoing side is a `Flow` parameter, keeping the signature "flows in, flow out":

```kotlin
sealed interface SocketEvent {
    object Connected : SocketEvent
    data class Received(val frame: Frame) : SocketEvent
}

fun SocketApi.asFlow(outgoing: Flow<Frame>): Flow<SocketEvent> = callbackFlow {
    val socket = open(object : SocketListener {
        override fun onOpen() { trySend(SocketEvent.Connected) }
        override fun onFrame(f: Frame) { trySend(SocketEvent.Received(f)) }
        override fun onClosed(cause: Throwable?) { if (cause != null) close(cause) else close() }
    })
    launch { outgoing.collect { socket.send(it) } }           // commands out: a collector inside the same lifetime
    awaitClose { socket.close() }
}
```

- No `fun send(frame)` methods on a returned handle — the writer is a collector of `outgoing` and dies with the session automatically.
- Cancellation closes the socket; the socket closing (`onClosed`) completes the flow. Both directions of shutdown converge on the same two lines.
- This shape drops directly into the Level-3 loop from `feedback-loops.md`: `api.asFlow(outgoing)` is an `external` source, and `outgoing` is fed by the executor (`execute(cmd)` produces the frames to send). The callback interface never reaches the reducer.

If a consumer must *await* a command's completion (request/response over the session), keep the session as above and correlate downstream: send tagged frames, `first { it.tag == myTag }` on the shared event flow — not by adding suspend methods to the adapter.

## 7. Anti-patterns

| Anti-pattern | Why it fails | Fix |
|---|---|---|
| Wrapper class implementing the listener, registered in `init` / `start()` | Lifetime detached from any consumer; leaks, double-starts, ordering bugs | `asFlow()` extension; lifetime = collection |
| Bridge via `MutableSharedFlow` + `tryEmit` in the callback | Silent drops on overflow, no unregistration story, hot state | `callbackFlow` + explicit overflow policy (§3) |
| Logic in the callback body (filter, dedup, state update) | Untestable, invisible to the graph, races with downstream | The extension translates only; logic is operators |
| One extension per callback method, then `merge` | N registrations for one interface, ordering across methods lost | One `asFlow()`, one sealed event type (§2 for the exception) |
| Single-result callback wrapped as `Flow` | Streams a non-stream; every caller must know "it emits once" | suspend extension via `suspendCancellableCoroutine` (§4) |
| Missing `awaitClose` | `callbackFlow` throws; or registration leaks past cancellation | Always `awaitClose { unregister }` |
| `runBlocking { send(it) }` inside a callback | Blocks an IO/platform thread; deadlock-prone | `trySend` + §3 policy |
| Raw listener interface passed through layers, converted deep inside | Every layer inherits the vtable calling convention | `asFlow()` once, at the outermost boundary |
| Listener list + manual refcount inside the adapter to share a registration | Hand-rolled `shareIn` | Cold `asFlow()`, then `shareIn(scope, WhileSubscribed(...))` (§5) |

## 8. Testing

The extension is dumb, so its tests are dumb — and everything downstream never sees a callback at all.

- **The extension**: a fake receiver that captures the registered listener; drive it and assert the emitted events, unregistration on cancel, and failure on the error callback.

  ```kotlin
  class FakeScannerApi : ScannerApi {
      var listener: ScanListener? = null
      override fun register(l: ScanListener) { listener = l }
      override fun unregister(l: ScanListener) { listener = null }
  }

  @Test fun `error callback fails the flow and unregisters`() = runTest {
      val api = FakeScannerApi()
      api.asFlow().test {                                   // Turbine
          api.listener!!.onFound(device)
          assertEquals(ScanEvent.Found(device), awaitItem())
          api.listener!!.onError(Boom)
          assertEquals(Boom, awaitError())
      }
      assertNull(api.listener)                              // awaitClose ran
  }
  ```

- **Everything downstream**: test against plain flows — `flowOf(ScanEvent.Found(d))`, Turbine, `runTest`. No fake listeners, no capturing, no threads. This is the payoff of converting at the boundary: the callback interface appears in exactly one test file.
