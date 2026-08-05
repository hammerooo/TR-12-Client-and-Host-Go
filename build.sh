#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Pass --regen to also regenerate Smithy models (requires smithy + openapi-generator)
REGEN=false
for arg in "$@"; do
  [[ "$arg" == "--regen" ]] && REGEN=true
done

echo "=== Building TR-12 Client and Host ==="

# --- Regenerate Smithy models (optional) ---
if [ "$REGEN" = true ]; then
  # postprocess enriches openapi-generator's Go struct tags with min/max/pattern
  # from the OpenAPI spec (openapi-generator emits only @pattern by default and
  # over-escapes backslashes). See models/cdd_sdk/postprocess-validate-tags.py.
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

  echo "=== Regenerating cdd_sdk models (TypeScript) ==="
  cd "$SCRIPT_DIR/models/cdd_sdk"
  ./generate-client-sdk-models.sh typescript-fetch
  # Add package.json so console can reference it as a local npm dependency
  cat > "$SCRIPT_DIR/models/cdd_sdk/generated/cdd_sdktypescript-fetch/package.json" << 'EOF'
{
  "name": "cdd-sdk-models",
  "version": "1.0.0",
  "description": "Auto-generated TR-12 CDD SDK TypeScript models (do not edit manually)",
  "main": "index.ts",
  "types": "index.ts",
  "private": true
}
EOF
  echo "✅ TypeScript models ready at models/cdd_sdk/generated/cdd_sdktypescript-fetch/"
fi

# --- Go binaries ---

echo "=== Building host (macOS) ==="
cd "$SCRIPT_DIR/host"
mkdir -p bin
go build -o bin/tr12-host ./cmd/tr12-host/

echo "=== Building host (Linux amd64 for EC2) ==="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/tr12-host-linux-ec2 ./cmd/tr12-host/

echo "=== Building client SDK (macOS) ==="
cd "$SCRIPT_DIR/client"
mkdir -p bin
go build -o bin/cdd-sdk ./cmd/cdd-sdk/

echo "=== Building ARD (macOS) ==="
go build -o bin/ard ./cmd/application_reference_design/

echo "=== Building console ==="
cd "$SCRIPT_DIR/console"
npm run build

echo ""
echo "✅ Build complete"
echo "   host/bin/tr12-host"
echo "   host/bin/tr12-host-linux-ec2"
echo "   client/bin/cdd-sdk"
echo "   client/bin/ard"
echo "   console/dist/"
echo ""
echo "Usage: ./build.sh [--regen]"
echo "  --regen  Also regenerate Smithy models (requires smithy + openapi-generator)"
