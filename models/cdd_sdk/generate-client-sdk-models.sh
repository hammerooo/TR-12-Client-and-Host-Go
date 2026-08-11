#!/bin/bash
set -e

# Generates CDD Client SDK models (CddService)
# Run this script from src/models/cdd_sdk directory

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

SMITHY_SERVICE="CddService"
OPENAPI_SPEC="build/smithy/source/openapi/${SMITHY_SERVICE}.openapi.json"
OUTPUT_DIR="./generated/cdd_sdk"
LANGUAGES=("cpp-restsdk" "python" "typescript" "typescript-fetch" "cpp-tiny" "cpp-oatpp-client" "go")

# Check arguments
if [ $# -ne 1 ]; then
    echo "Usage: $0 <language>"
    echo "Supported languages: ${LANGUAGES[*]}"
    exit 1
fi

LANG="$1"

# Validate language
if [[ ! " ${LANGUAGES[*]} " =~ " ${LANG} " ]]; then
    echo "❌ Error: Unsupported language '$LANG'"
    echo "Supported languages: ${LANGUAGES[*]}"
    exit 1
fi

# 1. Build the Smithy SDK model
echo "🚀 Building Smithy Client SDK model..."
smithy build

# 2. Check for spec
if [ ! -f "$OPENAPI_SPEC" ]; then
    echo "❌ Error: OpenAPI spec not found at $OPENAPI_SPEC"
    exit 1
fi

# 3. Clean previous generated output for this language
OUTPUT_PATH="${OUTPUT_DIR}${LANG}"
if [ -d "$OUTPUT_PATH" ]; then
    echo "🧹 Cleaning previous generated output: $OUTPUT_PATH"
    rm -rf "$OUTPUT_PATH"
fi

# 4. Generate SDK models
echo "📦 Generating Client SDK models..."
if [ "$LANG" = "python" ]; then
    openapi-generator generate \
        -i "$OPENAPI_SPEC" \
        -g "$LANG" \
        -o "$OUTPUT_PATH" \
        --additional-properties=projectName="${SMITHY_SERVICE}SDK",packageName=cdd_sdk_client
elif [ "$LANG" = "typescript-fetch" ]; then
    openapi-generator generate \
        -i "$OPENAPI_SPEC" \
        -g "$LANG" \
        -o "$OUTPUT_PATH" \
        --additional-properties=projectName="${SMITHY_SERVICE}SDK",typescriptThreePlus=true,withInterfaces=true
else
    openapi-generator generate \
        -i "$OPENAPI_SPEC" \
        -g "$LANG" \
        -o "$OUTPUT_PATH" \
        --additional-properties=projectName="${SMITHY_SERVICE}SDK",packageName=cdd_sdkgo,enumClassPrefix=true \
        --git-user-id=vsf-tv \
        --git-repo-id=TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo
fi

echo "✅ Done! Client SDK is in $OUTPUT_PATH"

# Fix module name — openapi-generator uses a placeholder that breaks go.work.
# Use sed -i.bak (portable across BSD/macOS and GNU/Linux), then drop the backup.
if [ "$LANG" = "go" ]; then
    sed -i.bak 's|github.com/GIT_USER_ID/GIT_REPO_ID|github.com/vsf-tv/TR-12-Client-and-Host-Go/models/cdd_sdk/generated/cdd_sdkgo|' "${OUTPUT_PATH}/go.mod"
    rm -f "${OUTPUT_PATH}/go.mod.bak"
    echo "✅ Fixed go.mod module name"
fi
