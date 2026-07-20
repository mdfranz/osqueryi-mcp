import asyncio
import json
import os
import sys
import time


RESPONSE_TIMEOUT_SECONDS = 75
EXPECTED_TOOLS = {
    "list_tables",
    "describe_table",
    "run_query",
    "search_tables",
    "preview_table",
    "query_table",
    "refresh_cache",
}


async def main():
    # Use absolute path for server
    server_path = os.path.abspath("osqueryi-mcp")
    env = os.environ.copy()
    env.setdefault("OSQUERYI_LOCKFILE", "off")
    env.setdefault("OSQUERYI_LOGFILE", "off")
    
    # Start the server process
    process = await asyncio.create_subprocess_exec(
        server_path,
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=sys.stderr,
        env=env,
    )

    async def send(msg):
        process.stdin.write(json.dumps(msg).encode() + b"\n")
        await process.stdin.drain()

    async def recv():
        line = await asyncio.wait_for(
            process.stdout.readline(), timeout=RESPONSE_TIMEOUT_SECONDS
        )
        if not line:
            return None
        return json.loads(line)

    def assert_tool_result(response, name, expect_error):
        if response is None:
            raise AssertionError(f"{name}: server closed stdout before responding")
        if "error" in response:
            raise AssertionError(f"{name}: JSON-RPC error: {response['error']}")

        result = response.get("result")
        if not isinstance(result, dict):
            raise AssertionError(f"{name}: missing CallToolResult: {response}")
        if result.get("isError", False) != expect_error:
            raise AssertionError(
                f"{name}: expected isError={expect_error}, got {result.get('isError')}: {result}"
            )

        content = result.get("content")
        if not isinstance(content, list) or not content:
            raise AssertionError(f"{name}: missing content: {result}")
        if not any(
            isinstance(item, dict)
            and item.get("type") == "text"
            and isinstance(item.get("text"), str)
            and item["text"]
            for item in content
        ):
            raise AssertionError(f"{name}: expected non-empty text content: {result}")

    async def call_tool(request_id, name, arguments, *, expect_error=False):
        start = time.perf_counter()
        await send({
            "jsonrpc": "2.0",
            "id": request_id,
            "method": "tools/call",
            "params": {
                "name": name,
                "arguments": arguments,
            }
        })
        result = await recv_id(request_id)
        assert_tool_result(result, name, expect_error)
        elapsed_ms = (time.perf_counter() - start) * 1000
        print(f"{name} ({elapsed_ms:.1f} ms):", json.dumps(result, indent=2))
        return result

    async def recv_id(request_id):
        while True:
            res = await recv()
            if res is None:
                return None
            if "id" in res and res["id"] == request_id:
                return res
            else:
                print(f"Skipping notification/other: {res.get('method') or res.get('id')}")

    try:
        # 1. Initialize
        await send({
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {"name": "test-client", "version": "1.0.0"}
            }
        })
        init_res = await recv_id(1)
        if not isinstance(init_res, dict) or not isinstance(init_res.get("result"), dict):
            raise AssertionError(f"initialize: missing result: {init_res}")
        if not init_res["result"].get("serverInfo", {}).get("name"):
            raise AssertionError(f"initialize: missing serverInfo.name: {init_res}")
        print("Initialize response:", json.dumps(init_res, indent=2))

        # 2. Initialized notification
        await send({
            "jsonrpc": "2.0",
            "method": "notifications/initialized"
        })

        # 3. List tools
        await send({
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/list",
            "params": {}
        })
        tools_res = await recv_id(2)
        tools = tools_res.get("result", {}).get("tools", []) if tools_res else []
        tool_names = {tool.get("name") for tool in tools if isinstance(tool, dict)}
        if missing := EXPECTED_TOOLS - tool_names:
            raise AssertionError(f"tools/list: missing tools: {sorted(missing)}")
        print("Tools list:", json.dumps(tools_res, indent=2))

        print("\n--- Structured discovery workload ---")
        await call_tool(3, "list_tables", {})
        await call_tool(4, "search_tables", {"query": "user", "limit": 8})
        await call_tool(5, "search_tables", {"query": "uid", "search_columns": True, "limit": 8})

        print("\n--- Schema + preview workload ---")
        await call_tool(6, "describe_table", {"table_name": "users"})
        await call_tool(7, "describe_table", {"table_name": "processes"})
        await call_tool(8, "preview_table", {"table_name": "users", "limit": 3})
        await call_tool(9, "preview_table", {"table_name": "processes", "limit": 3})

        print("\n--- Validated single-table query workload ---")
        await call_tool(10, "query_table", {
            "table_name": "users",
            "columns": ["username", "uid", "gid", "shell"],
            "where": "uid >= 0",
            "order_by": ["uid ASC"],
            "limit": 5
        })
        await call_tool(11, "query_table", {
            "table_name": "processes",
            "columns": ["pid", "name", "path", "uid", "on_disk"],
            "where": "on_disk = 1",
            "order_by": ["pid ASC"],
            "limit": 5
        })

        print("\n--- Join / raw SQL workload ---")
        await call_tool(12, "run_query", {
            "sql": """
            SELECT
              p.pid,
              p.name,
              p.path,
              u.username
            FROM processes p
            LEFT JOIN users u ON p.uid = u.uid
            WHERE p.on_disk = 1
            ORDER BY p.pid ASC
            LIMIT 5
            """
        })
        await call_tool(13, "run_query", {
            "sql": """
            SELECT
              l.port,
              l.protocol,
              l.address,
              p.name,
              p.path
            FROM listening_ports l
            LEFT JOIN processes p ON l.pid = p.pid
            ORDER BY l.port ASC
            LIMIT 5
            """
        })

        print("\n--- Cache management ---")
        await call_tool(14, "refresh_cache", {})

        print("\n--- Error path ---")
        await call_tool(
            15,
            "run_query",
            {"sql": "SELECT * FROM non_existent_table"},
            expect_error=True,
        )
    finally:
        if process.returncode is None:
            process.terminate()
            await process.wait()

if __name__ == "__main__":
    asyncio.run(main())
