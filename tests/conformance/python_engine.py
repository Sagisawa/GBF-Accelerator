"""
Python Proxy Engine Launcher for GBF-Accelerator Conformance Test Suite.
Starts gbf_proxy and control_server in an isolated process on specified test ports.
"""

import argparse
import asyncio
import os
import signal
import sys

# Ensure repository root is in sys.path
_repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
if _repo_root not in sys.path:
    sys.path.insert(0, _repo_root)

import control_server
import gbf_proxy
from config_manager import config_manager


def parse_args():
    parser = argparse.ArgumentParser(description="GBF-Accelerator Python Engine Runner")
    parser.add_argument("--proxy-port", type=int, default=8126, help="Port for proxy listener")
    parser.add_argument("--control-port", type=int, default=8127, help="Port for control plane")
    parser.add_argument("--upstream-proxy", type=str, default="", help="Upstream proxy URL (e.g. mock upstream)")
    return parser.parse_args()


import tempfile
from pathlib import Path

def main():
    args = parse_args()

    # Isolate test config to a temporary file so mutations never touch the real config.json
    temp_cfg_fd, temp_cfg_path = tempfile.mkstemp(prefix="conformance_cfg_", suffix=".json")
    os.close(temp_cfg_fd)
    config_manager.config_path = Path(temp_cfg_path)

    # Configure proxy & control plane
    config_manager.config["listen_port"] = args.proxy_port
    config_manager.config["control_port"] = args.control_port
    config_manager.config["allow_lan"] = False
    config_manager.config["auto_system_proxy"] = False
    config_manager.config["clean_zombies"] = False
    config_manager.config["enable_control_server"] = True
    config_manager.config["verify_upstream_tls"] = False
    config_manager.config["enable_ram_warmup"] = False

    if args.upstream_proxy:
        config_manager.config["upstream_proxy"] = args.upstream_proxy
        config_manager.config["direct_mode"] = False
        gbf_proxy.UPSTREAM_PROXY = args.upstream_proxy
        gbf_proxy.DIRECT_MODE = False

    # Start control server on background thread
    ctrl_started = control_server.start_control_server(port=args.control_port, host="127.0.0.1", timeout=5.0)
    if not ctrl_started:
        sys.stderr.write(f"[-] Failed to start control server on port {args.control_port}\n")
        sys.exit(1)

    # Set proxy parameters
    gbf_proxy.LISTEN_HOST = "127.0.0.1"
    gbf_proxy.LISTEN_PORT = args.proxy_port

    def sig_handler(*_):
        control_server.stop_control_server()
        sys.exit(0)

    try:
        signal.signal(signal.SIGINT, sig_handler)
        signal.signal(signal.SIGTERM, sig_handler)
    except Exception:
        pass

    try:
        asyncio.run(gbf_proxy.main())
    except (KeyboardInterrupt, SystemExit):
        pass
    finally:
        control_server.stop_control_server()
        try:
            if os.path.exists(temp_cfg_path):
                os.remove(temp_cfg_path)
        except Exception:
            pass


if __name__ == "__main__":
    main()
