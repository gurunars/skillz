# Worked example: device connection controller

Requirement: a device has an enable switch. When enabled, connect and keep streaming readings; when disabled, disconnect. Toggling rapidly must not leak connections or get stuck. The UI wants to show Off / Connecting / On(readings) / Error.

## Before — hand-rolled state machine (the pattern to eliminate)

```kotlin
class DeviceController(private val scope: CoroutineScope, private val driver: Driver) {

    sealed interface State {
        object Off : State
        object TurningOn : State
        data class On(val latest: Reading?) : State
        object TurningOff : State
        data class Error(val cause: Throwable) : State
    }

    private val _state = MutableStateFlow<State>(State.Off)
    val state: StateFlow<State> = _state
    private var connectJob: Job? = null
    private var readingsJob: Job? = null
    private var pendingDesired: Boolean? = null

    fun setEnabled(on: Boolean) {
        when (_state.value) {
            State.TurningOn, State.TurningOff -> {   // busy — remember for later
                pendingDesired = on
                return
            }
            is State.On, is State.Error -> if (!on) {
                turnOff()
            }
            State.Off -> if (on) {
                turnOn()
            }
        }
    }

    private fun turnOn() {
        _state.value = State.TurningOn
        connectJob = scope.launch {
            try {
                driver.connect()
                _state.value = State.On(null)
                readingsJob = launch {
                    driver
                        .readings()
                        .collect { r ->
                        _state.update {
                            if (it is State.On) {
                                State.On(r)
                            } else {
                                it
                            }
                        }
                    }
                }
            } catch (t: Throwable) {
                _state.value = State.Error(t)
            } finally {
                drainPending()
            }
        }
    }

    private fun turnOff() {
        _state.value = State.TurningOff
        readingsJob?.cancel()
        readingsJob = null
        connectJob = scope.launch {
            try { driver.disconnect() } finally {
                _state.value = State.Off
                drainPending()
            }
        }
    }

    private fun drainPending() {
        val p = pendingDesired ?: return
        pendingDesired = null
        setEnabled(p)
    }
}
```

What is wrong, concretely:

- `TurningOn` and `TurningOff` exist only because `connect()`/`disconnect()` suspend. They are transit states.
- Three mutable fields (`connectJob`, `readingsJob`, `pendingDesired`) and a `MutableStateFlow` written from four places.
- `pendingDesired` is a hand-written one-slot `conflate()`. `drainPending()` is a hand-written `flatMapLatest` continuation, and it is wrong: a `turnOff()` can be entered while `connectJob` is still running and `readingsJob` was never assigned, so the reading collector started later leaks.
- The `_state.update { if (it is State.On) … }` guard exists because the readings job can outlive the state it belongs to.
- Untestable without a scope, a driver fake with controllable suspension, and careful `advanceUntilIdle()` choreography.

## After — operators

```kotlin
sealed interface Status {
    object Off : Status
    object Connecting : Status
    data class On(val latest: Reading?) : Status
    data class Error(val cause: Throwable) : Status
}

@OptIn(ExperimentalCoroutinesApi::class)
fun deviceStatus(desired: Flow<Boolean>, driver: Driver): Flow<Status> =
    desired
        .distinctUntilChanged()
        .flatMapLatest { on ->                                    // supersede: newest switch position wins
            if (!on) {
                flowOf(Status.Off)
            } else {
                driver
                    .session()                                    // Flow<Reading> — cancellation == disconnect
                    .map<Reading, Status> { Status.On(it) }
                    .onStart {
                        emit(Status.Connecting)
                        emit(Status.On(null))
                    }
                    .catch { emit(Status.Error(it)) }
            }
        }
```

with the driver exposing the connection *as a flow*:

```kotlin
fun Driver.session(): Flow<Reading> = callbackFlow {
    connect()                                   // suspends until connected, throws on failure
    val sub = subscribe { reading -> trySend(reading) }
    awaitClose {                                // cancellation IS disconnect
        sub.cancel()
        disconnect()
    }
}
```

The owner applies hotness once at the edge:

```kotlin
val status = deviceStatus(enabledSwitch, driver)
    .stateIn(scope, SharingStarted.WhileSubscribed(5_000), Status.Off)
```

What happened to each piece of the old code:

| Old | New |
|---|---|
| `TurningOn` | `onStart { emit(Connecting) }` — derived, not steered on |
| `TurningOff` | cancellation of the inner flow; `awaitClose { disconnect() }` |
| `connectJob`, `readingsJob` | the inner flow's own lifetime under `flatMapLatest` |
| `pendingDesired` + `drainPending()` | `flatMapLatest` semantics — the newest value is always applied |
| `setEnabled(on)` mutating method | `desired: Flow<Boolean>` input |
| `_state.update { if (it is On) … }` | impossible: readings only exist inside the `On` branch |
| `try/catch → Error` | `.catch { emit(Error(it)) }` |
| `MutableStateFlow` written from four places | one cold pure function; `stateIn` once |

Test:

```kotlin
@Test fun rapidToggleEndsInLastPosition() = runTest {
    val desired = MutableSharedFlow<Boolean>()
    val driver = FakeDriver(readings = flowOf(Reading(1)))
    deviceStatus(desired, driver).test {
        desired.emit(true)
        desired.emit(false)
        desired.emit(true)
        // whatever interleaving happens, the final state follows the last position
        assertEquals(Status.On(Reading(1)), expectMostRecentItem())
        assertEquals(1, driver.openSessions)
    }
}
```

## Extension: what if the device needs a handshake after connecting?

That is Level 2 feedback (result selects next stage), not a state machine:

```kotlin
driver
    .session()
    .flatMapConcat { session ->                                   // result selects next stage
        session
            .handshake()
            .map { session to it }
    }
    .flatMapConcat { (session, caps) -> session.readings(caps.rate) }
```

Only if the *readings themselves* must change how future switch positions are interpreted — say, a reading of `Overheated` should make `desired = true` a no-op until `Cooled` arrives — do you have a domain state, and then it is a `runningFold` over `merge(desired, readings)` with a pure reducer. Even then, `Connecting` still never appears in that reducer's state type.
