#!/bin/bash

# Exit on error
set -e

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
done

echo ""
echo "--- All tests completed successfully! ---"
