# TR-12 Client and Host — Go

A complete Go implementation of the [VSF TR-12 Client Device Discovery](https://github.com/vsf-tv/TR-12-Models/blob/main/VSF_TR-12-ClientDeviceDiscoverySpecification.md) protocol, covering both the device side (client SDK + application reference design) and the host/cloud side (self-contained host service with embedded MQTT broker).

TR-12 defines a secure, NAT-friendly pairing and communication protocol for professional streaming video devices. This repo provides everything needed to pair devices, exchange configurations, rotate credentials, stream thumbnails, and manage device lifecycles — all from pure Go binaries with no external service dependencies.

## What's in the Box

| Directory | What it is | README |
|---|---|---|
| [`client/`](client/) | CDD SDK daemon + Application Reference Design (ARD) | [client/README.md](client/README.md) |
| [`host/`](host/) | TR-12 Host Service (REST API + embedded MQTT broker + SQLite) | [host/README.md](host/README.md) |
| [`models/TR-12-Models/`](https://github.com/vsf-tv/TR-12-Models) | Smithy-generated TR-12 protocol types (git submodule) | — |
| [`models/cdd_sdk/`](models/cdd_sdk/) | CDD SDK Smithy models + Go/TypeScript generation scripts | — |

## Dependency Versions

| Dependency | Version |
|---|---|
| [TR-12-Models](https://github.com/vsf-tv/TR-12-Models) | v6.0.0 |

The **client** runs on the device. It exposes a local REST API that a device application (or the included ARD simulator) calls to connect, report status, receive configuration, and handle thumbnails/logs. Under the hood it manages pairing, mTLS credential storage, and an MQTT connection to the host.

The **host** runs wherever you want your control plane. It handles device pairing, account management, configuration push, thumbnail retrieval, log collection, and automatic certificate rotation. Everything persists in a single SQLite file — no cloud services, no external databases.

Both consume the same **shared TR-12 protocol models** from the `models/` submodule. No vendored copies, no duplication.

## Prerequisites

- Go 1.22+ (host service requires 1.24+)
- Git

## Cloning

The TR-12 protocol models live in a git submodule. You must pull them when cloning:

```bash
SSH Method: (recommended)
git clone --recurse-submodules git@github.com:vsf-tv/TR-12-Client-and-Host-Go.git

HTTP Method:
git clone --recurse-submodules https://github.com/vsf-tv/TR-12-Client-and-Host-Go.git
```

If you already cloned without `--recurse-submodules`:

```bash
git submodule update --init --recursive
```

Updates should obtain the updated, underlying TR12Models  `--recurse-submodules`:
This ensures the client update pulls in the latest TR12 models.
```bash
git pull --recurse-submodules
```
Without the submodule, the Go builds will fail because both `client/` and `host/` import types from `models/TR-12-Models/generated/tr12go/`.

## Dependencies
Smithy
```bash
brew install smithy-lang/tap/smithy-cli
```

Open API Generator
```bash
brew install openapi-generator
```

Some corporate networks may block GO build process (below) unless using...
```bash
go env -w GOPROXY=direct
```

## Building

One command, from the repo root:

```bash
./build.sh
```

That regenerates the Smithy models and builds every binary — host, SDK, ARD, and the Linux cross-compiles.

Requires `smithy` and `openapi-generator` on your `PATH`:
```bash
brew install smithy-lang/tap/smithy-cli
brew install openapi-generator
```

And the submodule initialized on first checkout:
```bash
git submodule update --init --recursive
```

### Do not invoke the raw generate scripts directly

`models/TR-12-Models/generate-tr12-models.sh` and `models/cdd_sdk/generate-client-sdk-models.sh` produce Go code with a broken `validate` tag on `ProtocolVersion.Version` (openapi-generator over-escapes backslashes in `@pattern`). `build.sh` runs a post-processor that overwrites the bad tags. If you skip `build.sh`, every valid registration is rejected at runtime with `invalid registration: Version.Version: regular expression mismatch`.

## Quick Start — Running Locally

You need three terminals for the running processes, plus a way to interact with the host service API (curl, Postman, or any HTTP client).

### Terminal 1 — Host Service

```bash
cd host
./bin/tr12-host --host-address 127.0.0.1 --http-port 8080 --mqtt-port 8883
```

On first run this auto-generates a CA, server cert, JWT secret, and SQLite database.

### Terminal 2 — CDD SDK

```bash
# The client SDK holds certs in: CERTS/LOCAL_DEVICE_ID/<host_id>
export LOCAL_DEVICE_ID=my_test_device
export CERTS=~/TR-12-Certs
mkdir -p $CERTS
cd client
./bin/cdd-sdk --internal_device_id $LOCAL_DEVICE_ID --certs_path $CERTS --log_path /tmp/sdk-logs --ip 127.0.0.1 --port 8603 --device_type SOURCE
```

### Terminal 3 — ARD (simulated device application)

```ba sh
cd client
./bin/ard --host_id local_go_host
```

The ARD will print a pairing code, e.g. `Device is not paired. Pairing Code: A3B7K9 Expires in: 1800s.`

### Interacting with the Host Service API

You can use curl, Postman, or any REST client to manage accounts, claim devices, push configurations, etc.




#### Option A: curl

```bash
# Register an account and capture the JWT token
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/account/register \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme1","display_name":"Admin"}' | jq -r .token)
# Host service maintains registered user accounts in a date store: TR-12-Client-and-Host-Go/host/tr12-host.db 

# Claim the device using the pairing code from the ARD output
curl -X PUT http://127.0.0.1:8080/authorize/<pairing code from application reference design> \
  -H "Authorization: Bearer $TOKEN"

# List connected devices
curl http://127.0.0.1:8080/devices \
  -H "Authorization: Bearer $TOKEN" | jq .

# Get the device details
curl http://127.0.0.1:8080/device/<DEVICE_ID> \
  -H "Authorization: Bearer $TOKEN" | jq .

# Request a thumbnail (note: using CH01 from device details request above)
# Note: Starts a new thumbnail subscription request.  First response is
# expected to be empty as it takes some time for the client to send images.
curl "http://127.0.0.1:8080/thumbnail/<DEVICE_ID>?channel=CH01" \
  -H "Authorization: Bearer $TOKEN"

# Push a configuration update — starts channel CH01 as SRT caller to 192.168.1.100:9000
curl -s -X PUT "http://127.0.0.1:8080/device/<DEVICE_ID>" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"channels":[{"id":"CH01","state":"ACTIVE","protocol":{"srtCaller":{"address":"<srt listener ip>","port":<srt listener port>,"minimumLatencyMilliseconds":200}}}]}' | jq .

# Push a configuration update — stops channel CH01 (passes state=IDLE)
curl -s -X PUT "http://127.0.0.1:8080/device/<DEVICE_ID>" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"channels":[{"id":"CH01","state":"IDLE","protocol":{"srtCaller":{"address":"<srt listener ip>","port":<srt listener port>,"minimumLatencyMilliseconds":200}}}]}' | jq .


# Rotate device credentials
curl -X PUT http://127.0.0.1:8080/credentials/<DEVICE_ID> \
  -H "Authorization: Bearer $TOKEN"

# Deprovision a device
curl -X PUT http://127.0.0.1:8080/deprovision/<DEVICE_ID> \
  -H "Authorization: Bearer $TOKEN"
```

#### Option B: Postman

Postman provides a visual interface for exploring the API, which is handy for inspecting JSON responses and managing auth tokens.

1. Set the base URL to `http://127.0.0.1:8080`

2. **Register an account** — create a new request:
   - `POST /account/register`
   - Body (JSON): `{"username":"admin","password":"changeme1","display_name":"Admin"}`
   - Copy the `token` value from the response

3. **Set up authorization** — in Postman's collection or request settings:
   - Auth Type: Bearer Token
   - Token: paste the JWT from step 2
   - All subsequent requests will include the `Authorization: Bearer <token>` header automatically

4. **Claim a device** — using the pairing code from the ARD output:
   - `PUT /authorize/A3B7K9`
   - No body needed (optionally `{"expiration_days": 730}`)

5. **Explore the API**:

   | Method | Endpoint | Description |
   |---|---|---|
   | `POST` | `/account/register` | Register a new account |
   | `POST` | `/account/login` | Login and get a JWT |
   | `GET` | `/account` | Get current account info |
   | `GET` | `/devices` | List all your devices |
   | `GET` | `/device/:id` | Describe a device (registration, config, status) |
   | `PUT` | `/device/:id` | Push a desired configuration |
   | `PUT` | `/authorize/:pairingCode` | Claim a device into your account |
   | `PUT` | `/deprovision/:id` | Deprovision a device |
   | `GET` | `/thumbnail/:id?source=SDI-1` | Request/retrieve a thumbnail |
   | `PUT` | `/credentials/:id` | Trigger certificate rotation |

6. **Tip**: Create a Postman Environment with variables `base_url` = `http://127.0.0.1:8080` and `token` = your JWT. Use `{{base_url}}` in request URLs and `{{token}}` in the Bearer Token field so you can switch between local and remote hosts easily.

Within seconds of claiming, the SDK transitions to `CONNECTED` and the ARD begins its report loop. See the [host README](host/README.md) for the full API reference with request/response examples.

## How the Shared Models Work

Both `client/` and `host/` import the Smithy-generated Go types from the submodule at `models/TR-12-Models/generated/tr12go/`. The generated code uses `package openapi` — each consumer imports it with an alias:

```go
import tr12models "github.com/vsf-tv/TR-12-Client-and-Host-Go/models/TR-12-Models/generated/tr12go"
```

Each module's `go.mod` has a `replace` directive pointing at the local submodule path, and the root `go.work` ties everything together for seamless development. If the upstream TR-12 models are updated, just pull the submodule and rebuild:

```bash
git submodule update --remote models/TR-12-Models
cd client && go build ./...
cd ../host && go build ./...
```

## Testing

### Unit Tests

Unit tests live alongside source files (idiomatic Go). Run them with:

```bash
go test ./client/... ./host/... -count=1
```

~80 test cases across 6 files covering client utilities, credential management, logging, database operations, and service logic.

### Integration Tests

A full end-to-end lifecycle test starts the host service and SDK as real processes and exercises the complete TR-12 protocol — pairing, MQTT, configuration, thumbnails, credential rotation, and deprovision.

```bash
go test -tags integration -v -timeout 300s ./test/integration/
```

| Test | Description |
|------|-------------|
| `TestFullLifecycle` | 11-phase end-to-end lifecycle |
| `TestOfflineConfigDelivery` | Retained MQTT config delivery to offline device |
| `TestTwoChannelEncoder` | 2-channel registration and per-channel configurationId tracking |
| `TestConfigurationIdBumping` | Independent per-entity configurationId bumping |
| `TestARDConfigurationIdEchoBack` | ARD echoes correct configurationIds in actual_configuration |

## Repository Structure

```
TR-12-Client-and-Host-Go/
├── client/                          # Device-side SDK + ARD
│   ├── cmd/cdd-sdk/                 #   SDK entry point
│   ├── cmd/application_reference_design/                     #   ARD entry point
│   ├── internal/                    #   SDK internals (pairing, MQTT, creds, etc.)
│   ├── pkg/cddmodels/              #   CDD SDK protocol models (generated)
│   ├── host_configuration/          #   Host config JSON files
│   ├── payloads/                    #   Sample registration/config payloads
│   │   └── thumbnails/       #   Sample thumbnail images for ARD
│   └── go.mod
├── host/                            # Host-side service
│   ├── cmd/tr12-host/               #   Entry point
│   ├── internal/                    #   API, broker, CA, DB, services
│   └── go.mod
├── models/                          # Shared models (git submodule)
│   └── TR-12-Models/
│       └── generated/tr12go/        #   Generated Go types + go.mod
├── go.work                          # Go workspace
├── .gitmodules                      # Submodule config
└── .gitignore
```

## TR-12 Protocol Reference

- [TR-12 Smithy Models](https://github.com/vsf-tv/TR-12-Models)
- [Protocol Specification](https://github.com/vsf-tv/TR-12-Models/blob/main/VSF_TR-12-ClientDeviceDiscoverySpecification.md)

## License

Apache License, Version 2.0
