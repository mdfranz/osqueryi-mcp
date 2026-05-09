import asyncio
import json
import os
import sys

async def main():
    # Use absolute path for server
    server_path = os.path.abspath("osqueryi-mcp")
    
    # Start the server process
    process = await asyncio.create_subprocess_exec(
        server_path,
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=sys.stderr,
    )

    async def send(msg):
        process.stdin.write(json.dumps(msg).encode() + b"\n")
        await process.stdin.drain()

    async def recv():
        line = await process.stdout.readline()
        if not line:
            return None
        return json.loads(line)

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

    # 4. Call list_tables
    await send({
        "jsonrpc": "2.0",
        "id": 3,
        "method": "tools/call",
        "params": {
            "name": "list_tables",
            "arguments": {}
        }
    })
    call_res = await recv_id(3)
    print("list_tables result:", json.dumps(call_res, indent=2))

    # 5. Call describe_table (users)
    await send({
        "jsonrpc": "2.0",
        "id": 4,
        "method": "tools/call",
        "params": {
            "name": "describe_table",
            "arguments": {"table_name": "users"}
        }
    })
    desc_res = await recv_id(4)
    print("describe_table (users) result:", json.dumps(desc_res, indent=2))

    # 6. Call run_query
    await send({
        "jsonrpc": "2.0",
        "id": 5,
        "method": "tools/call",
        "params": {
            "name": "run_query",
            "arguments": {"sql": "SELECT username, uid, gid FROM users LIMIT 2"}
        }
    })
    query_res = await recv_id(5)
    print("run_query result:", json.dumps(query_res, indent=2))

    # Cleanup
    process.terminate()
    await process.wait()

if __name__ == "__main__":
    asyncio.run(main())
