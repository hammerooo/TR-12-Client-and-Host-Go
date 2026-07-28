# TR-12 Client Integration Architecture

A device integrator building TR-12 support needs to implement three components on the device alongside the CDD SDK process: a **TR-12 Shim** that bridges the device's native control plane to TR-12 models, a **Native Device Control API** that the shim drives, and a **Web Console** that gives the device user visibility into both the TR-12 connection state and the underlying device settings.

## Integration Requirements

1. **User-facing controls** — The Web Console must let the device user enable/disable TR-12, select a host service, view the pairing code, and see the current connection state (DISCONNECTED, PAIRING, CONNECTED, RECONNECTING).

2. **Separation of concerns** — The Web Console handles user input and display only. The TR-12 Shim running on the device is responsible for driving the CDD SDK (connect, disconnect, report status, apply configuration) and translating between TR-12 models and the native device control API. The console never talks to the SDK directly.

3. **Persistent connections** — The shim must persist the user-selected TR-12 host ID across reboots and software updates. On power-up, the shim should automatically call `connect` with the previously configured `hostId` and `registration` payload. Users expect a paired and connected device to reconnect without manual intervention after a restart.

4. **Configuration refresh** — When the host service pushes a new desired configuration via MQTT, the shim applies it to the native device API and the console must refresh its display to reflect the updated state. The device user should always see the complete, current configuration without manually reloading.  The Web Console likely must call an API hosted by the Shim to see when an refresh is needed.

5. **Status reporting** — The shim must periodically read back actual device state from the native control API (bitrate, temperature, channel state, etc.) and publish it to the host service via `report_status`. The reporting interval is governed by the host settings received during authentication.

6. **Actual configuration reporting** — After applying a desired configuration, the shim must read back what the device actually applied and report it via `report_actual_configuration`. This lets the host service detect discrepancies between desired and actual state.

7. **Pairing flow visibility** — During initial pairing, the console must display the pairing code and expiration countdown so the user can claim the device on the host service. The shim polls `connect` until the device transitions from PAIRING to CONNECTED.  

8. **Deprovision support** — The console must provide a way for the user to deprovision the device from a host service, which deletes local credentials and informs the host. The shim calls `deprovision` on the SDK and resets to DISCONNECTED.

The diagram below shows how these pieces interact.

```
  ┌──────────────────────────────┐
  │                              │  Runs in browser.
  │  4. Web Console              │  User enables/disables TR-12, points to TR-12 version
  vice
  │  (Device UI)                 │  Sees connected status,
  │                              │  pairing code, etc.
  └──────────────┬───────────────┘
                 │
                 │  Calls Shim:
                 |  start() / stop() /
                 │  should_console_refresh()
                 │
┌────────────────┼────────────────────────────────────────────────────────────────────────┐
│                │                    DEVICE (on host hardware)                           │
│                │                                                                        │
│                ▼                                                                        │
│  ┌───────────────────┐   connect / disconnect         ┌───────────────────────────┐     │
│  │                   │   get_state / deprovision      │                           │     │
│  │  2. TR-12 Shim    │   get_configuration     ──────▶│  3. CDD SDK               │     │
│  │                   │   report_status                │  (localhost REST API)     │     │
│  │                   │   report_actual_config         │                           │     │
│  └───┬───────────▲───┘                                │  State Machine · MQTT     │     │
│      │           │                                    │  Pairing · Certs          │     │
│      │ apply     │ read                               │                           │     │
│      │ config    │ back                               └─────────────┬─────────────┘     │
│      ▼           │                                                  │                   │
│  ┌───────────────┴───┐                                              │                   │
│  │                   │    ┌────────────┐                            │                   │
│  │  1. Native Device │    │ Persistence│                            │                   │
│  │  Control API      │    │            │                            │                   │
│  │                   │    │ tr-12 host │                            │                   │
│  └───────────────────┘    └────────────┘                            │                   │
│                                                                     │                   │
└─────────────────────────────────────────────────────────────────────┼───────────────────┘
                                                                      │ HTTPS / MQTT
                                                                      │ (port 443)
                                                                      ▼
                                                             ┌─────────────────┐
                                                             │  TR-12 Host     │
                                                             │  Service        │
                                                             └─────────────────┘
```

**Arrow summary**

| From | To | Operations |
|---|---|---|
| 4 → 2 | Web Console → TR-12 Shim | `start()`, `stop()`, `should_console_refresh()` |
| 2 → 3 | TR-12 Shim → CDD SDK | `connect`, `disconnect`, `get_state`, `deprovision`, `get_configuration`, `report_status`, `report_actual_configuration` |
| 2 → 1 | TR-12 Shim → Native API | Apply desired configuration (codec, resolution, channel state, transport, etc.) |
| 1 → 2 | Native API → TR-12 Shim | Read back actual device state for status and configuration reporting |
| 3 → Host | CDD SDK → Host Service | Pairing, authentication, MQTT pub/sub over TLS on port 443 |

## TR-12 Protocol Version

Every device registration carries a `version` field that declares which TR-12 protocol version the device's payloads (registration, status, actual configuration, thumbnails, etc.) conform to. This value is the device's *contract* with the SDK and the host — everything the device sends on the wire must fit within that declared version.

### Semantics

The version string follows `MAJOR.MINOR.PATCH`:

- **MAJOR** — breaking wire change (removed field, renamed field, type change, new required field). Old code fails.
- **MINOR** — additive, backwards compatible (new optional field, new enum value, new transport protocol). Old code ignores what it doesn't recognise.
- **PATCH** — no wire change (documentation, shape renames).

Two versions are compatible if, and only if, their `MAJOR` values match and the receiver's `MINOR` is greater than or equal to the payload's `MINOR`. A receiver on a newer minor is always a strict superset of a payload on an older minor.

### Where the integrator sets it

In the registration JSON your shim sends to the SDK via `/connect` (and `/register`):

```json
{
  "version": { "version": "11.0.0" },
  "channelTemplates": [ ... ],
  "channelAssignments": [ ... ],
  "settings": [ ... ]
}
```

The value **must match the MAJOR.MINOR the device's shim was implemented against**. It is not a marketing version and it is not the device firmware version — it is the TR-12 protocol version the shim commits to speaking.

### Where the version is validated

The device-declared version is checked at **three** points and must pass all three:

1. **SDK `/connect` and `/register` HTTP handlers** — reject the registration if the JSON does not include `version.version`, if the value does not match `MAJOR.MINOR.PATCH`, or if it is not compatible with the SDK's own compiled-in TR-12 model. The response is `success=false` with a message beginning `Incompatible registration version X (SDK: Y). Ensure the device is compatible with TR12 major/minor version`. **Pairing does not start** if this check fails.
2. **`CreatePairingCode` request to the host** — the SDK carries the *device-declared* version verbatim into the pairing request so the host applies its own compatibility check against what the device claims, not against the SDK's own model version.
3. **Host MQTT registration receipt** — the host runs the same compatibility check against its own model when the SDK publishes the registration on `cdd/{deviceId}/registration/report`. Defense in depth: if a custom or older SDK skips step 1, the host still refuses to store an incompatible registration.

The console surfaces the accepted version in the device detail panel under `TR-12 Version`.

### Practical guidance for integrators

- **Keep your registration JSON in lockstep with the model version you build against.** When you upgrade the CDD SDK dependency, review whether your MAJOR or MINOR has changed and update `version.version` in your registration JSON to match.
- **Never inject or auto-fill the version.** The device makes the claim explicitly. If you rely on the shim to synthesise it from the linked model, you can lose visibility into a version drift where the shim's model is ahead of what the device's user-facing configuration actually supports.
- **A newer SDK with an older declared version is fine.** SDK 11.2 accepting a registration that declares 11.0 is a valid deployment (the SDK is a strict superset). But the *device* is bound by 11.0 — any 11.1 or 11.2 fields it accidentally populates will not be honoured by an 11.0 host.
- **If a host rejects your device on connect**, check the SDK response message for both versions. It tells you whether the device declared something too new for the host, or the SDK is on a different major from the device's declaration.

## DeviceCallbacks Interface

The shim is implemented by providing a `DeviceCallbacks` interface. This is the only code a device integrator needs to write — the `ApplicationLoop` and `Tr12Shim` are reusable and device-agnostic.

```go
type DeviceCallbacks interface {
    // Apply (set) side — called when applying desired configuration from the host
    UpdateDeviceKeyValue(key, value string)
    UpdateChannelSettings(channelID, key, value string)
    UpdateChannelProfile(channelID, profileID string)
    UpdateChannelConnection(channelID string, connection *cddsdkgo.Connection)
    UpdateChannelState(channelID string, state cddsdkgo.ChannelState)

    // Read-back (get) side — called when building actual configuration to report back
    GetDeviceUpdatedValue(key string) (string, bool)
    GetChannelUpdatedValue(channelID, key string) (string, bool)
    GetChannelProfileValue(channelID string) (string, bool)
    GetChannelConnection(channelID string) *cddsdkgo.Connection
    GetChannelState(channelID string) cddsdkgo.ChannelState
    GetDeviceStatus() []cddsdkgo.StatusValue
    GetChannelStatus(channelID string) []cddsdkgo.StatusValue
}
```

The `Tr12Shim` walks the TR-12 model structures and dispatches to these callbacks in a defined order per channel: settings → connection → state (last). This ordering ensures all settings and transport configuration are applied before the channel state is set.

## Channel State Management and Restart Behavior

Many devices require a channel restart for new settings to take effect. The `UpdateChannelState` callback is responsible for managing this correctly.

### Example Channel State Machine

A typical device has four channel states with strict transitions:

```
stopped  →  [start]  →  starting  →  started
started  →  [stop]   →  stopping  →  stopped
```

Where transitional states have only one possible next state:
- `stopping` can only transition to `stopped`
- `starting` can only transition to `started`

### Restart Logic

When `UpdateChannelState(ACTIVE)` is called, the implementation must handle all possible current states — including transitional ones — to reliably reach the desired state. A typical approach:

| Current state | Action |
|---|---|
| `stopped` | Start immediately |
| `stopping` | Wait for `stopped`, then start |
| `started` | Stop, wait for `stopped`, then start (settings restart) |
| `starting` | Wait for `started`, stop, wait for `stopped`, then start |

Making `UpdateChannelState` fully synchronous — blocking until the device reaches the desired state — ensures that when `GetActualConfiguration` is called immediately after, the reported channel state is accurate.

### Why Restart is Always Performed When Already Running

The `ApplicationLoop` gates configuration updates on `updateId` — `ApplyDesiredConfiguration` is only called when the host sends a genuinely new configuration. Since individual setting changes are not tracked, a restart is always performed when the channel is running and a new config arrives. This is correct behavior: the host only increments `updateId` when something actually changed.

### Reporting Channel State During Transitions

`GetChannelState` should map device-native status to TR-12 state conservatively — only return `IDLE` when the channel is fully stopped. Transitional states (`stopping`, `starting`) should be reported as `ACTIVE` because the channel is either mid-restart (will be running shortly) or shutting down as part of a restart sequence.

| Device status | TR-12 state |
|---|---|
| `stopped` | `IDLE` |
| `stopping`, `starting`, `started` | `ACTIVE` |

