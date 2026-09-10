#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "=== Building TR-12 Client and Host ==="

# --- Regenerate Smithy models ---
# Always regenerate. Skipping this step produces Go code with a broken
# validator tag on ProtocolVersion.Version (openapi-generator over-escapes
# backslashes in @pattern), and every valid registration is rejected at
# runtime with "invalid registration: Version.Version: regular expression
# mismatch". The postprocessor overwrites the buggy tags from the OpenAPI
# spec. See models/cdd_sdk/postprocess-validate-tags.py.
POSTPROC="$SCRIPT_DIR/models/cdd_sdk/postprocess-validate-tags.py"

echo "=== Regenerating TR-12-Models (Go) ==="
cd "$SCRIPT_DIR/models/TR-12-Models"
./generate-tr12-models.sh go
echo "=== Enriching TR-12-Models Go validate tags ==="
python3 "$POSTPROC" \
  --spec "$SCRIPT_DIR/models/TR-12-Models/build/smithy/source/openapi/HostServiceApi.openapi.json" \
  --dir  "$SCRIPT_DIR/models/TR-12-Models/generated/tr12go"

echo "=== Regenerating cdd_sdk models (Go) ==="
cd "$SCRIPT_DIR/models/cdd_sdk"
./generate-client-sdk-models.sh go
echo "=== Enriching cdd_sdk Go validate tags ==="
python3 "$POSTPROC" \
  --spec "$SCRIPT_DIR/models/cdd_sdk/build/smithy/source/openapi/CddService.openapi.json" \
  --dir  "$SCRIPT_DIR/models/cdd_sdk/generated/cdd_sdkgo"

# --- Go binaries ---

echo "=== Building host (macOS) ==="
cd "$SCRIPT_DIR/host"
mkdir -p bin
go build -o bin/tr12-host ./cmd/tr12-host/

echo "=== Building host (Linux amd64 for EC2) ==="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/tr12-host-linux-ec2 ./cmd/tr12-host/

# -tags netgo forces Go's pure-Go DNS resolver instead of libc/getaddrinfo.
# The libc path on macOS uses the system resolver stack (scutil --dns) which
# can return NXDOMAIN for hostnames that /etc/resolv.conf's nameserver
# resolves correctly (typically due to VPN split-DNS or mDNSResponder cache).
# The pure-Go resolver reads /etc/resolv.conf directly, matching `dig`.
echo "=== Building client SDK (macOS) ==="
cd "$SCRIPT_DIR/client"
mkdir -p bin
go build -tags netgo -o bin/cdd-sdk ./cmd/cdd-sdk/

echo "=== Building ARD (macOS) ==="
go build -tags netgo -o bin/ard ./cmd/application_reference_design/

echo ""
echo "✅ Build complete"
echo "   host/bin/tr12-host"
echo "   host/bin/tr12-host-linux-ec2"
echo "   client/bin/cdd-sdk"
echo "   client/bin/ard"
