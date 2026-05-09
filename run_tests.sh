#!/bin/bash

# Exit on error
set -e

echo "--- Building osqueryi-mcp binary ---"
make build

# Ensure the binary is in the PATH for the test scripts
export OSQUERYI_DEBUG=true
export PATH=$PATH:$(pwd)

echo ""
echo "--- Running basic MCP protocol test (test_mcp.py) ---"
uv run python tools/test_mcp.py

echo ""
echo "--- Running Agno integration test (agno_test_mcp.py) ---"
uv run python tools/agno_test_mcp.py

echo ""
echo "--- Running Strands integration test (strands_test_mcp.py) ---"
uv run python tools/strands_test_mcp.py

echo ""
echo "--- All tests completed successfully! ---"
