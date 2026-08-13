#!/bin/bash

# Exit on error
set -euo pipefail

# Parse arguments
USE_LOGS=false
LOG_FILE="osqueryi-mcp.log"

show_help() {
    echo "Usage: $0 [OPTIONS]"
    echo "Options:"
    echo "  --logs              Enable logging to default file ($LOG_FILE)"
    echo "  --log-file FILE     Enable logging to specified FILE"
    echo "  -h, --help          Show this help message"
}

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --logs) USE_LOGS=true; shift ;;
        --log-file) USE_LOGS=true; LOG_FILE="$2"; shift 2 ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *) echo "Unknown parameter: $1"; show_help; exit 1 ;;
    esac
done

echo "--- Building osqueryi-mcp binary ---"
make build

# Ensure the binary is in the PATH for the test scripts
export OSQUERYI_DEBUG=true
export PATH=$PATH:$(pwd)

# The Go server exports telemetry only when both OTLP vars are set, and it never reads
# .logfire/logfire_credentials.json the way the Python harness does — so without this the
# agent runs are traced and the server they call is not. Reuse the same project token.
LOGFIRE_CREDENTIALS=".logfire/logfire_credentials.json"
if [ -z "${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ] && [ -f "$LOGFIRE_CREDENTIALS" ]; then
    LOGFIRE_ENDPOINT=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['logfire_api_url'])" "$LOGFIRE_CREDENTIALS" 2>/dev/null || true)
    LOGFIRE_TOKEN=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['token'])" "$LOGFIRE_CREDENTIALS" 2>/dev/null || true)
    if [ -n "$LOGFIRE_ENDPOINT" ] && [ -n "$LOGFIRE_TOKEN" ]; then
        export OTEL_EXPORTER_OTLP_ENDPOINT="$LOGFIRE_ENDPOINT"
        export OTEL_EXPORTER_OTLP_HEADERS="Authorization=$LOGFIRE_TOKEN"
        export OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-osqueryi-mcp}"
        # The metrics PeriodicReader defaults to 60s, which is longer than a short run.
        export OTEL_METRIC_EXPORT_INTERVAL="${OTEL_METRIC_EXPORT_INTERVAL:-5000}"
        echo "--- Server telemetry -> $OTEL_EXPORTER_OTLP_ENDPOINT as $OTEL_SERVICE_NAME ---"
    else
        echo "--- Could not read $LOGFIRE_CREDENTIALS; server telemetry disabled ---"
    fi
elif [ -z "${OTEL_EXPORTER_OTLP_ENDPOINT:-}" ]; then
    echo "--- No OTLP endpoint and no $LOGFIRE_CREDENTIALS; server telemetry disabled ---"
fi

if [ "$USE_LOGS" = true ]; then
    export OSQUERYI_LOGFILE="$LOG_FILE"
    # Clear log file from previous runs
    > "$OSQUERYI_LOGFILE"
    echo "--- Server logs will be sent to $OSQUERYI_LOGFILE instead of stderr ---"
else
    export OSQUERYI_LOGFILE="off"
fi

echo ""
echo "--- Running basic MCP protocol test (test_mcp.py) ---"
uv run python tools/test_mcp.py

MODELS_FILE="models.txt"
if [ -f "$MODELS_FILE" ]; then
    # Read models into array, ignoring empty lines and comments
    MODELS=()
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ -z "$line" || "$line" =~ ^# ]] && continue
        MODELS+=("$line")
    done < "$MODELS_FILE"
else
    echo "--- No models.txt found, using default model ---"
    MODELS=("gemini-2.0-flash")
fi

for MODEL in "${MODELS[@]}"; do
    echo ""
    echo "================================================================================"
    echo " MODEL: $MODEL"
    echo "================================================================================"

    echo ""
    echo "--- Running Agno integration test (agno_test_mcp.py) ---"
    uv run python tools/agno_test_mcp.py "$MODEL"

    echo ""
    echo "--- Running Strands integration test (strands_test_mcp.py) ---"
    uv run python tools/strands_test_mcp.py "$MODEL"

    echo ""
    echo "--- Running Pydantic AI integration test (pydantic_ai_test_mcp.py) ---"
    uv run python tools/pydantic_ai_test_mcp.py "$MODEL"
done

echo ""
echo "--- All tests completed successfully! ---"
