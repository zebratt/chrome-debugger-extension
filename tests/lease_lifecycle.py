#!/usr/bin/env python3
"""Exercise the built CLI/MCP with an isolated broker and no Chrome connection."""

import argparse
import json
import os
from pathlib import Path
import select
import signal
import socket
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default=str(Path(__file__).resolve().parents[1] / "bin/chrome-connector"))
    parser.add_argument("--idle-seconds", type=float, default=35)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    processes = []
    with tempfile.TemporaryDirectory(prefix="cc-lease-", dir="/tmp") as directory:
        env = dict(os.environ, CHROME_CONNECTOR_RUN_DIR=directory)
        broker = subprocess.Popen([binary, "broker", "serve"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        processes.append(broker)
        sock = str(Path(directory) / "broker.sock")

        def rpc(method, params):
            with socket.socket(socket.AF_UNIX) as connection:
                connection.settimeout(4)
                connection.connect(sock)
                connection.sendall((json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}) + "\n").encode())
                with connection.makefile("r") as stream:
                    return json.loads(stream.readline())

        def mcp_request(process, tool, params):
            process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": tool, "arguments": params}}) + "\n")
            process.stdin.flush()
            if not select.select([process.stdout], [], [], 5)[0]:
                raise AssertionError("MCP response timed out")
            response = json.loads(process.stdout.readline())
            assert not response["result"].get("isError"), response
            return json.loads(response["result"]["content"][0]["text"])

        try:
            deadline = time.monotonic() + 5
            while not Path(sock).exists():
                if broker.poll() is not None:
                    raise AssertionError(broker.stderr.read())
                assert time.monotonic() < deadline, "broker did not start"
                time.sleep(0.02)
            for index, ending in enumerate(("sigterm", "eof", "sigint", "output-error"), 1):
                process = subprocess.Popen([binary, "mcp"], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                processes.append(process)
                target = {"profileId": "lease-lifecycle-test", "tabId": index}
                lease = mcp_request(process, "tab_claim", target)
                if ending == "sigterm":
                    print(f"Holding MCP claim idle for {args.idle_seconds:g}s...", flush=True)
                    time.sleep(args.idle_seconds)
                    renewed = rpc("tab.renew", dict(target, leaseToken=lease["leaseToken"]))
                    assert "result" in renewed, renewed
                    assert rpc("tab.claim", target)["error"]["data"]["kind"] == "TAB_BUSY"
                    process.send_signal(signal.SIGTERM)
                elif ending == "eof":
                    process.stdin.close()
                elif ending == "sigint":
                    process.send_signal(signal.SIGINT)
                else:
                    process.stdout.close()
                    process.stdin.write('{"jsonrpc":"2.0","id":2,"method":"ping"}\n')
                    process.stdin.flush()
                process.wait(timeout=5)
                reclaimed = rpc("tab.claim", target)
                assert "result" in reclaimed, (ending, process.returncode, reclaimed)
                assert reclaimed["result"]["leaseToken"] != lease["leaseToken"]
                rpc("tab.release", dict(target, leaseToken=reclaimed["result"]["leaseToken"]))
                print(f"PASS {ending}: claim released immediately", flush=True)
        finally:
            for process in reversed(processes):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)
                for stream in (process.stdin, process.stdout, process.stderr):
                    if stream is not None:
                        stream.close()


if __name__ == "__main__":
    main()
