import asyncio
import json
import os
import sys
import time

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
        line = await process.stdout.readline()
        if not line:
            return None
        return json.loads(line)

    async def call_tool(request_id, name, arguments):
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
        elapsed_ms = (time.perf_counter() - start) * 1000
        print(f"{name} ({elapsed_ms:.1f} ms):", json.dumps(result, indent=2))
        return result

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
    init_res = await recv()
    print("Initialize response:", json.dumps(init_res, indent=2))

    # 2. Initialized notification
    await send({
        "jsonrpc": "2.0",
        "method": "notifications/initialized"
    })

    # Wait for list_changed notification (optional but seen in previous runs)
    # notification = await recv()
    # print("Notification:", json.dumps(notification, indent=2))

    async def recv_id(request_id):
        while True:
            res = await recv()
            if res is None:
                return None
            if "id" in res and res["id"] == request_id:
                return res
            else:
                print(f"Skipping notification/other: {res.get('method') or res.get('id')}")

    # ... (after initialized notification)

    # 3. List tools
    await send({
        "jsonrpc": "2.0",
        "id": 2,
        "method": "tools/list",
        "params": {}
    })
    tools_res = await recv_id(2)
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
    await call_tool(15, "run_query", {"sql": "SELECT * FROM non_existent_table"})

    # Cleanup
    process.terminate()
    await process.wait()

if __name__ == "__main__":
    asyncio.run(main())
