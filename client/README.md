# TR-12 Client SDK and Application Reference Design — Go

A Go implementation of the TR-12 Client Device Discovery SDK, providing discovery, monitoring, and connection management of streaming video devices using an internet-secure, cloud and NAT friendly, scalable pairing and communication protocol.

## TR-12 Working Group

> Draft design documents related to this project are currently being discussed and revised in the VSF Bi-Weekly Forum.
> For access, please reach out to Brad Gilmer <brad@gilmer.tv> or Brian Rundle <brundle@amazon.com>.

## Quick Start

### 1. Build

```bash
cd client

# Build the SDK daemon
go build -o bin/cdd-sdk ./cmd/cdd-sdk/

# Build the Application Reference Design
go build -o bin/ard ./cmd/application_reference_design/
```

### 2. Start the SDK

```bash
mkdir -p /tmp/cdd_certs /tmp/cdd_logs

./bin/cdd-sdk \
  --internal_device_id my_device_123 \
  --certs_path /tmp/cdd_certs \
  --log_path /tmp/cdd_logs \
  --ip 127.0.0.1 \
  --port 8603 \
  --device_type SOURCE
```

### 3. Start the ARD

In a second terminal:

```bash
# 1-channel encoder against the local TR-12 Host Service:
./bin/ard --host_id tr12-host --registration_file cmd/application_reference_design/payloads/1_channel_encoder/registration.json

# 2-channel encoder:
./bin/ard --host_id tr12-host --registration_file cmd/application_reference_design/payloads/2_channel_encoder/registration.json
```

The ARD will display a pairing code. Claim the device on the host service (see the [Host README](../host/README.md)), then the ARD transitions to `CONNECTED` and begins reporting status and configuration.

### 4. Cross-Compilation

Go's built-in cross-compilation requires no additional toolchain:

```bash
# Linux x86_64 (EC2, server)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/cdd-sdk-linux-amd64 ./cmd/cdd-sdk/

# Linux ARM64 (embedded devices, Raspberry Pi, PetaLinux)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/cdd-sdk-arm64 ./cmd/cdd-sdk/
```

The resulting binary is fully self-contained — no runtime dependencies, no Python interpreter, no pip packages.

---

## Architecture

The SDK runs as a standalone process hosting a REST API on localhost. A device application (like the ARD) makes API requests to the SDK. The SDK handles pairing/auth via HTTPS and real-time communication with the host service via MQTT/TLS.

```
┌──────────────────┐       REST API        ┌──────────────┐     MQTT/TLS      ┌──────────────────┐
│  Device App /    │ ───────────────────── │   CDD SDK    │ ────────────────── │  TR-12 Host      │
│  ARD             │   localhost:port      │   (Go)       │   Port 443        │  Service         │
└──────────────────┘                       └──────────────┘                    └──────────────────┘
```

### State Machine

```
DISCONNECTED → PAIRING → CONNECTING → CONNECTED
                                          ↓
                                     RECONNECTING
```

---

## SDK CLI Arguments

| Argument | Description |
|---|---|
| `--internal_device_id` | Unique device identifier (required) |
| `--certs_path` | Persistent directory for X.509 credential storage (required) |
| `--log_path` | Writable directory for JSON log files (required) |
| `--ip` | IP address for the local REST API (required) |
| `--port` | Port for the local REST API (required) |
| `--device_type` | Device type: `SOURCE`, `DESTINATION`, or `BOTH` (required) |

## ARD CLI Arguments

| Argument | Description |
|---|---|
| `--host_id` | Host ID to connect to (required) |
| `--sdk_url` | Base URL of the running SDK (default: `http://127.0.0.1:8603`) |
| `--registration_file` | Path to registration JSON file (default: `payloads/1_channel_encoder/registration.json`) |
| `--deprovision_after` | If > 0, deprovision from the host and exit once the device has been `CONNECTED` for this long (Go duration, e.g. `90s`, `1m`). Default `0` disables it. Note that deprovisioning removes all stored credentials. |

---

## Application Reference Design (ARD)

The ARD is a companion program that simulates an encoder device making REST calls on the SDK daemon. It demonstrates the full TR-12 lifecycle and serves as the template for real device integrations.

### What the ARD Does

- Calls `PUT /connect` in a loop until the SDK reaches `CONNECTED` state
- Displays the pairing code when in `PAIRING` state
- Once connected, polls `GET /get_configuration` for host-service updates
- Applies desired configuration via the TR-12 shim (settings, connection, state)
- Reports device status via `PUT /report_status`
- Reports actual configuration via `PUT /report_actual_configuration` (including `thumbnailLocalPath` per channel)
- Manages an ffmpeg subprocess for SRT streaming when the host sets channel state to `ACTIVE`

### DeviceCallbacks Interface

The core integration pattern is the `DeviceCallbacks` interface (`internal/application_reference_design/callbacks_interface.go`). Any device integration implements this interface — the `ApplicationLoop` and `Tr12Shim` only know the interface, never the concrete implementation.

**Apply (set) side** — called when the host sends a new desired configuration:
- `UpdateDeviceKeyValue` — device-level setting (e.g. clock source)
- `UpdateChannelSettings` — channel simple setting (e.g. framerate, codec, bitrate)
- `UpdateChannelProfile` — profile selection
- `UpdateChannelConnection` — transport protocol config (SRT caller/listener, RIST, etc.)
- `UpdateChannelState` — ACTIVE or IDLE

**Read-back (get) side** — called when building the actual configuration to report back:
- `GetDeviceUpdatedValue`, `GetChannelUpdatedValue`, `GetChannelProfileValue`
- `GetChannelConnection`, `GetChannelState`
- `GetDeviceHealth`, `GetChannelHealth`
- `GetDeviceStatus`, `GetChannelStatus`
- `GetChannelThumbnailLocalPath` — returns the local filesystem path to the thumbnail image for a channel based on the device's current input configuration

**One reference implementation ships with this repo:**

`ArdCallbacks` (`internal/application_reference_design/mock_encoder_callbacks.go`) — the reference implementation using a simulated ffmpeg encoder. Study this to understand the pattern.

Any real device integration implements the same interface and wires into `ApplicationLoop`:

```go
// ARD binary — mock encoder:
callbacks := ard.NewArdCallbacks()
loop := ard.NewApplicationLoop(sdkURL, callbacks, &registration)

// Real device — same pattern, different callbacks:
callbacks := device.NewMyDeviceCallbacks(deviceURL)
loop := ard.NewApplicationLoop(sdkURL, callbacks, &registration)
```

### Thumbnail Model

Thumbnails are channel-centric. The host subscribes by `channelId`, and the SDK resolves the local file path from the latest `ActualConfiguration` reported by the application.

- The application sets `thumbnailLocalPath` per channel in ActualConfiguration via `GetChannelThumbnailLocalPath`
- The ARD defaults to SDI1 (`/tmp/image_sdi.jpg`) when no configuration has been pushed yet
- When the host changes a channel's input (e.g. SDI1 → HDMI1), the application updates the path in the next ActualConfiguration report
- The SDK picks up the new path on its next upload cycle — no restart needed

### ARD Data Files

The ARD expects these files relative to its binary location:

- `payloads/1_channel_encoder/registration.json` — 1-channel device registration
- `payloads/2_channel_encoder/registration.json` — 2-channel device registration
- `thumbnails/thumbnail_images_sdi/` — SDI thumbnail source images
- `thumbnails/thumbnail_images_hdmi/` — HDMI thumbnail source images

---

## SDK REST API Reference

All endpoints are served on `http://<ip>:<port>`.

### PUT /connect

Initiates or continues the connection/pairing flow with a host service.

```json
{
  "hostId": "tr12-host",
  "registration": { ... }
}
```

Response includes `state`, `pairingCode` (if pairing), `deviceId`, and `regionName` (if connected).

### PUT /disconnect

Disconnects from the host service and resets state to `DISCONNECTED`.

### GET /get_state

Returns the current connection state.

```json
{
  "success": true,
  "state": "CONNECTED",
  "message": ""
}
```

### PUT /report_status

Publishes a device status payload to the host service.

```json
{
  "status": { ... }
}
```

### PUT /report_actual_configuration

Publishes the device's actual configuration to the host service. The SDK stores this internally for thumbnail path resolution.

```json
{
  "configuration": { ... }
}
```

### GET /get_configuration

Returns the latest desired configuration received from the host service.

### PUT /register

Updates the device registration while connected. Only changes to `channelTemplates[n].profiles` are permitted — all other registration fields must remain identical to the original registration sent on connect. Returns an error if the device is not in `CONNECTED` state.

```json
{
  "registration": { ... }
}
```

### PUT /deprovision

Removes the device from the host service and deletes local credentials. If currently connected, the host is notified before credentials are deleted. Credentials are always deleted regardless of connection state — the `force` parameter is accepted for API compatibility but ignored.

```json
{
  "hostId": "tr12-host"
}
```

---

## Host Configuration

Host configuration files are JSON files in the `host_configuration/` directory, named `<host_id>.json`. The SDK looks for this directory relative to the binary location, falling back to the working directory.

Example for the local TR-12 Host Service:

```json
{
  "serviceId": "tr12-host",
  "serviceName": "My TR-12 Host",
  "deviceTypes": ["SOURCE", "DESTINATION", "BOTH"],
  "createPairingCodeUrl": "http://127.0.0.1:8080/pair",
  "authenticatePairingCodeUrl": "http://127.0.0.1:8080/authenticate",
  "thumbnailMaximumSizeKB": 100,
  "logFileMaximumSizeKB": 500
}
```

## Credential Storage

Credentials are stored at `<certs_path>/<internal_device_id>/<host_id>/`:

- Use the same `--internal_device_id` to reconnect with existing credentials
- Use a different `--internal_device_id` to start fresh
- Each host ID gets its own subfolder, so one device identity can connect to multiple hosts

## Logging

The SDK writes JSON-formatted rotating log files to the `--log_path` directory. Log files are capped at 500 KB with up to 3 rotated backups. Logs are also printed to stdout.

When the host service requests log uploads, the SDK automatically uploads rotated log files to the provided pre-signed URL.

---

## Requirements

- Go 1.22 or newer
- Outbound HTTPS access on port 443
- Persistent read/write filesystem for credential storage

## Dependencies

Managed via Go modules (`go.mod`):

- `github.com/gin-gonic/gin` — HTTP server
- `github.com/gin-contrib/cors` — CORS middleware
- `github.com/eclipse/paho.mqtt.golang` — MQTT client

## Project Structure

```
client/
├── cmd/
│   ├── cdd-sdk/main.go                          # SDK entry point
│   ├── application_reference_design/             # ARD entry point + payloads
│   │   ├── main.go
│   │   ├── payloads/
│   │   │   ├── 1_channel_encoder/registration.json
│   │   │   └── 2_channel_encoder/registration.json
│   │   └── thumbnails/
│   │       ├── thumbnail_images_sdi/
│   │       └── thumbnail_images_hdmi/
├── host_configuration/                           # Host config JSON files
├── internal/
│   ├── api/server.go                             # Gin REST API server
│   ├── application_reference_design/
│   │   ├── application.go                        # ARD initialization
│   │   ├── application_loop.go                   # Reusable TR-12 lifecycle loop
│   │   ├── callbacks_interface.go                # DeviceCallbacks interface
│   │   ├── mock_encoder_callbacks.go             # ArdCallbacks implementation
│   │   ├── mock_encoder_device.go                # ffmpeg encoder simulation
│   │   ├── tr12_client_caller.go                 # SDK REST client
│   │   ├── tr12_model_traversal.go               # TR-12 model shim
│   │   └── thumbnail_simulator.go                # Thumbnail image cycling
│   ├── cddlogger/logger.go                       # JSON rotating file logger
│   ├── credentials/store.go                      # X.509 cert persistence
│   ├── models/tr12_models.go                     # State constants + model aliases
│   ├── pairing/pairing.go                        # Pairing/auth flow
│   ├── sdk/
│   │   ├── sdk.go                                # Core SDK struct and state machine
│   │   ├── connect.go                            # Public API methods
│   │   ├── register.go                           # PUT /register — profile-only re-registration
│   │   └── mqtt.go                               # MQTT connection and callbacks
│   ├── thumbnails/manager.go                     # Thumbnail upload manager
│   └── utils/utils.go                            # TLS, upload, throttle, key gen
├── go.mod
└── go.sum
```

## TR-12 Protocol Reference

- Smithy Models: https://github.com/vsf-tv/TR-12-Models
- Specification: https://github.com/vsf-tv/TR-12-Models/blob/main/VSF_TR-12-ClientDeviceDiscoverySpecification.md

## License

Apache License, Version 2.0

---

## Integration Guide

This guide covers how to integrate a real device with the TR-12 SDK. The Application Reference Design (ARD) is the working example — read it alongside this guide.

### Overview

Your integration consists of two processes:

1. **The SDK daemon** (`cdd-sdk`) — handles pairing, authentication, MQTT, and credential management. You start it as a subprocess or system service and leave it running.
2. **Your device application** — makes REST calls to the SDK daemon in a loop, applies configuration changes to your device's native API, and reports status back.

The SDK daemon and your application communicate over a local REST API (default `http://127.0.0.1:8603`). Your application never touches MQTT or TLS directly.

---

### Step 1 — Start the SDK Daemon

Launch `cdd-sdk` before your application starts. It must be running before any API calls are made.

```bash
cdd-sdk \
  --internal_device_id <unique_device_id> \
  --certs_path /path/to/certs \
  --log_path /path/to/logs \
  --ip 127.0.0.1 \
  --port 8603 \
  --device_type SOURCE
```

- `internal_device_id` — a stable identifier for this physical device. Used to locate stored credentials. Reuse the same value across restarts to reconnect without re-pairing.
- `device_type` — `SOURCE`, `DESTINATION`, or `BOTH`.
- `certs_path` — persistent storage for X.509 credentials. Must survive reboots.

The SDK exposes its REST API immediately on startup. Your application can begin calling it right away.

---

### Step 2 — The Application Loop

Your application runs a loop that calls `PUT /connect` repeatedly until the device is connected, then calls `GET /get_configuration`, `PUT /report_status`, and `PUT /report_actual_configuration` on each iteration.

```
loop:
  resp = PUT /connect {hostId, registration}
  if resp.state == "PAIRING":
      display resp.pairingCode to operator
  if resp.state == "CONNECTED":
      cfg = GET /get_configuration
      if cfg has new desired configuration:
          apply changes to device
          PUT /report_actual_configuration
      PUT /report_status
  sleep 5s
```

The `ApplicationLoop` in `internal/application_reference_design/application_loop.go` implements this pattern. You can use it directly by providing your own `DeviceCallbacks` implementation, or replicate the pattern in your own language.

**Host configuration** — the SDK looks for a JSON file named `<host_id>.json` in a `host_configuration/` directory relative to the binary. This file tells the SDK where to find the host service's pairing and auth endpoints. See the `host_configuration/` directory for examples.

---

### Step 3 — Implement DeviceCallbacks

`DeviceCallbacks` (`internal/application_reference_design/callbacks_interface.go`) is the integration contract. Implement it to bridge TR-12 model operations to your device's native control API.

The interface has two sides:

**Apply side** — called by the shim when the host sends a new desired configuration. Your job is to call your device's native API for each change.

| Method | When called | What to do |
|---|---|---|
| `UpdateDeviceKeyValue(key, value)` | Device-level setting changed (e.g. clock source) | Call your device API to apply the setting |
| `UpdateChannelSettings(channelID, key, value)` | Channel simple setting changed (framerate, codec, bitrate, etc.) | Call your device API for that channel |
| `UpdateChannelProfile(channelID, profileID)` | Profile selection changed | Apply the named profile to the channel |
| `UpdateChannelConnection(channelID, connection)` | Transport protocol config changed (SRT, RIST, etc.) | Configure the transport on your device |
| `UpdateChannelState(channelID, state)` | Channel state changed to ACTIVE or IDLE | Start or stop the channel on your device |

**Read-back side** — called by the shim when building the actual configuration to report back to the host. Return the current state of your device.

| Method | Return |
|---|---|
| `GetDeviceUpdatedValue(key)` | Current value of a device-level setting |
| `GetChannelUpdatedValue(channelID, key)` | Current value of a channel setting |
| `GetChannelProfileValue(channelID)` | Active profile ID as confirmed by the device, or `("", false)` if the device has not yet confirmed the profile. This is called only when the desired config is in profile mode — the value must come from the device's native API, not be echoed from desired. |
| `GetChannelConnection(channelID)` | Current transport protocol configuration |
| `GetChannelState(channelID)` | `ACTIVE` or `IDLE` |
| `GetDeviceHealth()` | `Healthy`, `Degraded`, or `Critical` |
| `GetChannelHealth(channelID)` | Per-channel health |
| `GetDeviceStatus()` | Device-level status values (CPU, temp, model, serial) |
| `GetChannelStatus(channelID)` | Per-channel status values (bitrate, output state) |
| `GetChannelThumbnailLocalPath(channelID)` | Local filesystem path to the current thumbnail image for this channel |

**Key principle:** the shim calls your callbacks in order — settings first, then connection, then state. By the time `UpdateChannelState(ACTIVE)` is called, all settings and connection config have already been applied. Your `UpdateChannelState` implementation should start the channel using whatever was set in the preceding calls.

**The shim handles model traversal.** You never parse the raw TR-12 JSON. The `Tr12Shim` walks the `DeviceConfiguration` structure and calls your callbacks for each changed value. Your callbacks only need to know your device's native API.

Wire your implementation into `ApplicationLoop`:

```go
callbacks := NewMyDeviceCallbacks(deviceURL)
loop := ard.NewApplicationLoop(sdkURL, callbacks, &registration)
loop.Run(ctx, hostID)
```

See `mock_encoder_callbacks.go` and `mock_encoder_device.go` for a complete working example.

---

### Step 4 — Handle Configuration Changes

The `ApplicationLoop` tracks `configurationId` per entity (device-level and per-channel independently). It only calls your callbacks when something actually changed — unchanged channels are skipped. You do not need to implement change detection yourself.

When a channel's configuration changes:
1. `UpdateChannelSettings` is called for each changed setting
2. `UpdateChannelConnection` is called if the transport changed
3. `UpdateChannelState` is called last

After applying changes, the loop calls your read-back methods to build the actual configuration and reports it to the host via `PUT /report_actual_configuration`. The `configurationId` values you echo back tell the host exactly what was applied.

**Health reporting** — if any native API call fails during `UpdateChannel*`, set the channel health to `Degraded` or `Critical` in your implementation. The shim reads this via `GetChannelHealth` and includes it in the actual configuration report. The host service and console display this to the operator.

---

### Step 5 — Report Status

On each connected loop iteration, the loop calls `GetDeviceStatus()` and `GetChannelStatus(channelID)` for each registered channel, then publishes the result via `PUT /report_status`. Return whatever is meaningful for your device — bitrate, output state, signal lock, temperature, etc.

```go
func (cb *MyCallbacks) GetChannelStatus(channelID string) []cddsdkgo.StatusValue {
    return []cddsdkgo.StatusValue{
        {Name: "bitrate-bps", Value: cb.getCurrentBitrate(channelID), Info: "Current output bitrate"},
        {Name: "signal_lock", Value: "true", Info: "Input signal locked"},
    }
}
```

Status values are free-form key/value pairs. The host stores them and the console displays them.

---

### Step 6 — Construct registration.json

The registration file declares your device's capabilities to the host service. The host uses it to validate configuration updates and the console uses it to render the UI.

```json
{
  "version": { "version": "6.0.0" },
  "deviceType": "SOURCE",
  "settings": [
    {
      "id": "sync_clock_source",
      "name": "Clock Source",
      "description": "Sets the clock source for all active channels.",
      "constraint": {
        "enums": {
          "values": ["INTERNAL", "GENLOCK", "NTP", "PTP"],
          "defaultValue": "NTP"
        }
      }
    }
  ],
  "channelTemplates": [
    {
      "id": "encoder_hd",
      "channelType": "SOURCE",
      "protocols": ["SRT_CALLER", "SRT_LISTENER"],
      "profiles": [
        {
          "id": "h264_hd",
          "name": "H.264 HD",
          "description": "H.264, 1080p, 10Mbps, 30fps"
        }
      ],
      "settings": [
        {
          "id": "RS01",
          "name": "Resolution",
          "description": "Output video dimensions.",
          "constraint": {
            "enums": {
              "values": ["1920x1080", "1280x720"],
              "defaultValue": "1920x1080"
            }
          }
        },
        {
          "id": "MB01",
          "name": "Max Bitrate",
          "description": "Maximum output bitrate in Kbps.",
          "constraint": {
            "ranges": {
              "minimum": 1000,
              "maximum": 50000,
              "defaultValue": 10000
            }
          }
        }
      ]
    }
  ],
  "channelAssignments": [
    {
      "channelId": "CH01",
      "templateId": "encoder_hd",
      "name": "Encoder Channel 1"
    }
  ]
}
```

**Fields:**

- `version` — TR-12 protocol version. Must be `{ "version": "6.0.0" }`.
- `deviceType` — `SOURCE`, `DESTINATION`, or `BOTH`.
- `settings` (device-level) — settings that apply to the whole device. The `id` values are the keys passed to `UpdateDeviceKeyValue`.
- `channelTemplates` — reusable channel capability definitions. Multiple channel assignments can share the same template.
  - `id` — template identifier referenced by `channelAssignments`.
  - `channelType` — `SOURCE` or `DESTINATION`.
  - `protocols` — supported transport protocols. Valid values: `SRT_CALLER`, `SRT_LISTENER`, `RIST_SIMPLE_CALLER`, `RIST_SIMPLE_LISTENER`, `ZIXI_PUSH`, `ZIXI_PULL`, `RTP`.
  - `profiles` — optional named presets. If present, the host can send a profile ID instead of individual settings. The `id` is passed to `UpdateChannelProfile`.
  - `settings` — per-channel settings. The `id` values are the keys passed to `UpdateChannelSettings`. Use `enums` for discrete values or `ranges` for numeric ranges.
- `channelAssignments` — maps logical channel IDs to templates.
  - `channelId` — the channel identifier used in all callback calls. Must be stable across restarts.
  - `templateId` — references a `channelTemplates` entry.
  - `name` — display name shown in the console.

---

### Integration Considerations

These are practical lessons for anyone building a real device integration. They apply regardless of device type or native API.

**Resiliency of your native device APIs.**.  TR12 will likely hit your native device APIs with a greater frequency (probing for current status, making a configuration update across lots of settings all at once) that would otherwise be the case for human users updating a local interface item-by-item.  It is likely you will expose pre-existing rough edges and bugs in your device's API. The most sinister kind are only resolved with a proccess restart or worse yet, a reboot.  TR12 is automation around your APIs for remote users with limited insights into "what went wrong' unless the respond to a configuration update informs the host service.  Error states that are no made apparent to the host service user must be avoided at all costs.  This raise the bar for your device's API resiliency, care you take in handling TR12 request/responses to your API, and most importantly testing.  

**Persist connection state across reboots.** A paired and connected device should reconnect automatically on reboot without operator intervention. Save the last connected host ID to persistent storage when a connection is established. On startup, read it back and reconnect before the device is considered ready. If no saved state exists, wait for an operator to initiate pairing.

**One connection at a time.** Your integration process should manage exactly one active connection loop. Use a command channel or equivalent pattern — a single goroutine owns the loop lifecycle and processes start/stop/switch commands sequentially. Never start a new connection before the previous one has fully stopped. Overlapping connections produce unpredictable state transitions that are very hard to debug.

**Graceful disconnect before switching hosts.** When switching from one host to another, explicitly disconnect from the current host before connecting to the new one. This allows the host to mark the device offline immediately rather than waiting for a keepalive timeout. The host's console will show the correct state.

**Configuration is desired state, not a command.** The host sends a desired configuration — it does not send "start" or "stop" as one-shot actions. If applying a configuration fails (network error, device busy, hardware fault), the host will not retry. Your integration must detect the failure, report it via health status, and retry on the next configuration cycle. Treat every configuration application as idempotent.

**Device APIs may require restart to apply settings.** Many device APIs apply settings immediately but only take effect after a pipeline restart. Know which settings on your device require a restart and which don't. If a restart is needed, stop the pipeline, apply settings, then restart — in that order. Applying settings to a running pipeline and then restarting is safer than restarting first.

**Snapshot device state before applying changes.** Read the current device state (running/stopped, current settings) at the start of each configuration apply cycle, not mid-way through. State can change asynchronously — a pipeline that was running when you started may have stopped by the time you check again. Take one snapshot and drive all decisions from it.

**Device APIs may be sensitive to rapid sequential calls.** Some devices reject or misbehave when many API calls arrive in quick succession. If your device has this characteristic, add small delays between setting changes, or batch them where the API supports it. Do not assume that a 200 OK response means the setting took effect immediately.

**HTTP timeouts on device commands must be generous.** Pipeline start and stop operations can take significantly longer than typical API calls — codec initialization, hardware negotiation, and stream establishment all take time. Use a longer timeout (10–30 seconds) for start/stop commands than for settings reads. A timeout on a start command does not mean the command failed — poll the device status to confirm the actual outcome.

**Never trust your command response alone.** Some device APIs return HTTP 200 with an error in the response body. Always parse the response body and check the device's own status code. After issuing a start or stop command on your pipeline API for example, poll your device's status endpoint until it reaches the expected state or a timeout elapses.

**Report health accurately.** If a native API call fails during configuration apply, set the channel health to DEGRADED or CRITICAL. The host and console display this to the operator. Do not silently swallow errors — an operator looking at a "healthy" device that is actually misconfigured has no way to know something is wrong.

**Read-back must use live device state.** When reporting actual configuration back to the host, read values from the device's native API — not from a cache or by echoing the desired values. The one exception is the `channelSettings` union branch: the desired config determines whether actual reports a profile or standard settings (you should not report a profile if standard settings were requested, and vice versa). But within that branch, the values must come from the device. For standard settings, read each key from the device API. For profile mode, call your device's API to confirm which profile is currently active — do not simply echo back the profile ID the host asked for. If the device has not yet confirmed the profile, return `("", false)` from `GetChannelProfileValue` and omit channelSettings from the actual config until it is confirmed.

---

### Health Error Lifecycle

Health is reported per-channel as a single `oneOf(Healthy, Degraded, Critical)` value in the actual configuration. Getting health right requires understanding *when* to set it, *when* to clear it, and *how* to merge multiple errors into a single value.

**Two independent failure domains.** Track errors from two separate sources independently:

1. **Desired config apply failures** — settings writes, protocol configuration, or state transitions that fail during the apply phase.
2. **Actual config read failures** — device API calls that fail when reading back current values for the periodic actual config report.

These are independent. A failed settings write does not resolve when a status read succeeds, and vice versa. A successful status read means the device API is reachable; it does not mean the settings were applied correctly.

**Clear on cycle boundaries.** At the start of each config apply cycle, clear any accumulated apply errors. At the start of each actual-config read cycle, clear any accumulated read errors. This prevents stale errors from a cancelled or superseded attempt from bleeding into the current report.

```go
// Start of apply cycle — clear previous apply errors
func BeginChannelUpdate(channelID string) {
    pendingErrors[channelID] = nil
    applyHealth[channelID] = Healthy
}

// Start of read-back cycle — clear previous read errors
func BeginGetActualConfiguration(channelID string) {
    readHealth[channelID] = Healthy
}
```

**Clear on successful outcome.** If all settings are applied and the channel successfully reaches the desired state (confirmed "started" or "stopped"), transient errors accumulated during the apply phase are moot — the device is operating correctly. Clear them before evaluating final health. This prevents the operator from seeing a stale timeout error on a channel that is running perfectly.

**Evaluate health at the end of the apply cycle.** After all settings, protocol, and state-transition work is complete (or has failed), evaluate the accumulated errors into a single health value. This is the value that `GetChannelHealth` returns.

**Merge when both domains have errors.** If both apply-health and read-health indicate a problem, merge them: pick the worst severity (`Critical > Degraded > Healthy`), concatenate messages, and truncate to 128 characters.

**The 128-character limit.** `HealthError.message` is limited to 128 characters by the TR-12 spec. The SDK enforces truncation before MQTT publish, but you should truncate at the source with a clean `"..."` suffix rather than getting a mid-word cut. When accumulating multiple errors, join them with `"; "` and truncate the result:

```go
msg := strings.Join(errors, "; ")
if len(msg) > 128 {
    msg = msg[:125] + "..."
}
```

---

### Handling Device Rejections vs Transient Failures

Not all errors are equal. Your state-transition logic must distinguish between transient failures (which may resolve on retry) and permanent rejections (which will never succeed without external intervention).

**Transient failures** — HTTP timeout, connection refused, device busy, "try again later" response codes. These warrant retrying within the current apply cycle up to a reasonable deadline (10–30 seconds). The device may recover, the network may stabilize, or the pipeline may finish its current operation.

**Device rejections** — the device explicitly responds with an error indicating the operation is impossible in its current state. Examples: "SDI Source is not detected", "Unsupported codec for this input", "License expired". These will *never* succeed by retrying. The physical world must change first (operator plugs in a cable, uploads a license, changes an input).

**How to distinguish them:**

| Signal | Classification | Action |
|--------|---------------|--------|
| HTTP timeout or connection error | Transient | Retry until deadline |
| Device returns its own error code with a message | Rejection | Stop immediately, report DEGRADED |
| HTTP 200 but device status shows unexpected state | Transient | Poll again |
| HTTP 200, device status stable in wrong state after deadline | Timeout | Report DEGRADED |

**On rejection:** Record the error message, set health to DEGRADED, and exit the state-transition loop immediately. Do not retry — you are wasting time and potentially hammering a device that is telling you it cannot proceed. The host will need to send a new configuration to resolve it (e.g., the operator changes the input source, or plugs in the SDI cable and re-pushes config).

```go
// Example: device rejected start
if deviceResponse.Code == "400" {
    appendError(channelID, "start rejected: " + deviceResponse.Error)
    return  // do not retry
}
```

---

### Periodic Actual Configuration Reporting

Report actual configuration in two situations:

1. **After every config apply cycle** — so the host sees the new state (including health) immediately.
2. **Periodically (every 60 seconds)** — even when no config changes arrive.

The periodic report serves several purposes:

- The host sees updated health. If a transient error was reported earlier but the channel has since recovered, the periodic report reflects the current healthy state.
- Device-level status values (CPU, temperature) and channel status values (bitrate) are refreshed.
- If the actual-config read itself fails (device API unreachable), the health reflects that — the host knows the device is in trouble even though no config was pushed.

**Avoid duplicating reports.** If a config-change report was just sent within the last few seconds, skip the periodic report for that cycle. The config-change path already provided fresh data.

**Periodic reports re-read live state.** Each periodic report calls your `GetChannelUpdatedValue`, `GetChannelConnection`, `GetChannelState`, and `GetChannelHealth` methods. These must read from the device — not return cached values from the last apply cycle.

---

### Per-Channel Version Gating

The host assigns a monotonically-increasing `version` string to each channel independently. The `ApplicationLoop` tracks the last-applied version per channel and only dispatches work for channels whose version changed.

**What this means for integrators:**

- Your callbacks will only be called for channels that actually changed. You do not need to implement change detection or idempotency checks yourself.
- If `UpdateChannelState("CH01", IDLE)` is called, it means the host explicitly changed CH01's config — act on it unconditionally.
- Unchanged channels are never touched. If CH01 changes and CH02 doesn't, only CH01's callbacks fire. CH02 continues running undisturbed.

**Cancellation on new version.** If a newer version arrives while a previous apply is still in progress for the same channel, the in-flight work is cancelled and a new cycle starts fresh. This means:

- Your `UpdateChannelState` implementation should accept cancellation (via `context.Context` if using Go, or equivalent in other languages) and exit promptly when cancelled.
- Do not block indefinitely on a device API call. Use timeouts on all HTTP requests.
- When cancelled, do not report health or actual config — the new cycle will handle that.

```go
// Optional interface for context-aware state transitions
type ContextAwareStateUpdater interface {
    UpdateChannelStateWithContext(ctx context.Context, channelID string, state ChannelState)
}
```

If your callbacks implement this interface, the `ApplicationLoop` will use it automatically. Otherwise it falls back to the plain `UpdateChannelState` without cancellation support.

---

### Implementing UpdateChannelState

`UpdateChannelState` is the most complex callback to implement correctly. It must drive the channel to the desired state, handle both transient failures and device rejections, and exit cleanly on cancellation.

**Do not fire-and-forget.** If you issue a start command and return immediately, you cannot report failure if the start is rejected. You must poll the device until the desired state is confirmed or an error is reported.

**Recommended pattern for ACTIVE:**

```
1. Phase 1 — Drive to stopped (restart requires clean stop):
   - Poll device status
   - If "started" or "starting" → issue stop, continue polling
   - If "stopping" → wait
   - If "stopped" → phase 1 done

2. Phase 2 — Drive to started:
   - Issue start
   - Poll device status
   - If "started" → success, clear transient errors, return
   - If device rejects start → record error, return immediately
   - If "stopped" still after timeout → record timeout, return
   - If cancelled → return (new config version arrived)
```

**Recommended pattern for IDLE:**

```
1. Drive to stopped:
   - Poll device status
   - If "started" or "starting" → issue stop, continue polling
   - If "stopping" → wait
   - If "stopped" → done
```

**Key principles:**

- **Issue commands only from valid precondition states.** Only issue start when the device reports "stopped" — not when it's "stopping" or "starting". Only issue stop when "started" or "starting". This avoids command-rejected errors from the device.
- **Distinguish "device is transitioning" from "device is stuck."** If the device reports "stopping" for 20 seconds without reaching "stopped", that's a stuck state — report DEGRADED and exit.
- **Clear transient errors on success.** If the channel reaches "started" successfully, any HTTP timeouts that occurred during earlier settings writes were clearly transient (the device processed them eventually). Clear accumulated errors so the health report shows HEALTHY.
- **Exit on device rejection.** If the device returns an explicit error (e.g., "SDI Source is not detected"), do not retry. Record the error and return. The device cannot start without external intervention.

---

### Testing Your Integration

The subtle interactions between configuration apply, state transitions, health reporting, and periodic heartbeats produce failure modes that are difficult to discover through casual testing. Design your test plan around these scenarios:

| Scenario | What to verify |
|----------|---------------|
| Apply config with device API unreachable | Health reports DEGRADED; periodic report refreshes health |
| Start channel with no input signal | Device rejection surfaces as DEGRADED immediately, not after 20s timeout |
| Settings write times out but channel starts successfully | Health clears to HEALTHY (transient error is moot) |
| New config version arrives mid-apply | Previous apply is cancelled; new apply starts fresh with no stale errors |
| Sequential per-channel updates (stop CH01, then stop CH02) | Both channels process independently; second update is not lost |
| Device API returns HTTP 200 with error in body | Error is detected and reported, not silently treated as success |
| Periodic report while channel is degraded then recovers | Health transitions from DEGRADED back to HEALTHY within 60s |
| Multiple settings fail in one apply cycle | Errors are joined and truncated to 128 chars, not one arbitrarily selected |

**Integration tests beat manual testing.** The scenarios above are regressions waiting to happen. Automate them with real processes (host + SDK + your application) exercising the full protocol. A 60-second integration test catches bugs that weeks of manual testing miss.
