"""
Conformance Test Suite Runner (tests/conformance/run_suite.py)
Cross-engine wire-level black-box behavior contract validator.
Supports --proxy-port, --control-port, --engine (python/go/auto), --record-baseline, --filter <regex>.
"""

import argparse
import asyncio
import datetime
import json
import os
import re
import socket
import ssl
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Dict, List, Optional

import httpx

# Ensure project root in sys.path
_repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
if _repo_root not in sys.path:
    sys.path.insert(0, _repo_root)

from tests.conformance.cases import ALL_CASES, ConformanceContext, TestCase
from tests.conformance.mock_upstream import MockUpstreamServer


def is_port_listening(port: int, host: str = "127.0.0.1", timeout: float = 0.3) -> bool:
    """Check if a TCP port is currently listening."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.settimeout(timeout)
        try:
            return sock.connect_ex((host, port)) == 0
        except Exception:
            return False


async def wait_for_ports(ports: List[int], timeout: float = 6.0) -> bool:
    """Poll until all ports are listening or timeout expires."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if all(is_port_listening(p) for p in ports):
            return True
        await asyncio.sleep(0.1)
    return False


def parse_args():
    parser = argparse.ArgumentParser(description="GBF-Accelerator Cross-Engine Conformance Suite")
    parser.add_argument("--proxy-port", type=int, default=8126, help="Target proxy port (default: 8126)")
    parser.add_argument("--control-port", type=int, default=8127, help="Target control server port (default: 8127)")
    parser.add_argument("--engine", choices=["python", "go", "auto"], default="python", help="Engine under test (default: python)")
    parser.add_argument("--record-baseline", action="store_true", help="Record test run as golden baseline JSON")
    parser.add_argument("--filter", type=str, default="", help="Regex pattern to filter test cases by name")
    return parser.parse_args()


async def run_suite_async():
    args = parse_args()
    print("=" * 70)
    print("   GBF-Accelerator Cross-Engine Conformance Test Suite (M2)")
    print(f"   Target Engine : {args.engine.upper()}")
    print(f"   Proxy Port    : {args.proxy_port}")
    print(f"   Control Port  : {args.control_port}")
    print(f"   Baseline Mode : {'RECORD' if args.record_baseline else 'VERIFY'}")
    if args.filter:
        print(f"   Filter Regex  : {args.filter}")
    print("=" * 70)

    # 1. Start mock upstream server on a dynamic port
    os.environ["NO_PROXY"] = "*"
    mock_upstream = MockUpstreamServer(host="127.0.0.1", port=0)
    mock_port = await mock_upstream.start()
    print(f"[*] Mock upstream server listening on 127.0.0.1:{mock_port}")

    proxy_proc: Optional[subprocess.Popen] = None
    spawned_proxy = False
    revert_config: Optional[Dict[str, Any]] = None

    # Backup repo config.json to guarantee clean disk state
    repo_cfg_path = Path(_repo_root) / "config.json"
    saved_cfg_content: Optional[str] = None
    if repo_cfg_path.is_file():
        try:
            saved_cfg_content = repo_cfg_path.read_text(encoding="utf-8")
        except Exception:
            pass

    actual_engine = args.engine
    try:
        # 2. Ensure proxy and control server are running
        proxy_listening = is_port_listening(args.proxy_port)
        control_listening = is_port_listening(args.control_port)

        if not (proxy_listening and control_listening):
            if args.engine == "go":
                exe_names = [
                    "GBF_Accelerator.exe" if sys.platform == "win32" else "GBF_Accelerator",
                    "gbf_proxy.exe" if sys.platform == "win32" else "gbf_proxy",
                    "gbf-proxy.exe" if sys.platform == "win32" else "gbf-proxy",
                ]
                go_candidates = []
                for name in exe_names:
                    go_candidates.extend([
                        Path(_repo_root) / "bin" / name,
                        Path(_repo_root) / name,
                        Path(_repo_root) / "engine" / name,
                    ])
                go_bin = next((c for c in go_candidates if c.is_file() and os.access(c, os.X_OK)), None)
                if not go_bin:
                    sys.stderr.write(
                        f"[-] Go engine requested (--engine go), but Go binary not found in bin/ or engine/.\n"
                        f"    Please compile the Go core first or use '--engine python'.\n"
                    )
                    return 1
                cmd = [
                    str(go_bin),
                    "--proxy-port", str(args.proxy_port),
                    "--control-port", str(args.control_port),
                    "--upstream-proxy", f"http://127.0.0.1:{mock_port}",
                    "--headless",
                ]
                actual_engine = "go"
            elif args.engine == "auto":
                exe_names = [
                    "GBF_Accelerator.exe" if sys.platform == "win32" else "GBF_Accelerator",
                    "gbf_proxy.exe" if sys.platform == "win32" else "gbf_proxy",
                    "gbf-proxy.exe" if sys.platform == "win32" else "gbf-proxy",
                ]
                go_candidates = []
                for name in exe_names:
                    go_candidates.extend([
                        Path(_repo_root) / "bin" / name,
                        Path(_repo_root) / name,
                        Path(_repo_root) / "engine" / name,
                    ])
                go_bin = next((c for c in go_candidates if c.is_file() and os.access(c, os.X_OK)), None)
                if go_bin:
                    print(f"[*] Auto-detected Go engine binary: {go_bin}")
                    cmd = [
                        str(go_bin),
                        "--proxy-port", str(args.proxy_port),
                        "--control-port", str(args.control_port),
                        "--upstream-proxy", f"http://127.0.0.1:{mock_port}",
                        "--headless",
                    ]
                    actual_engine = "go"
                else:
                    print("[*] No Go engine binary found, auto-defaulting to Python proxy engine.")
                    cmd = [
                        sys.executable,
                        "-m",
                        "tests.conformance.python_engine",
                        "--proxy-port", str(args.proxy_port),
                        "--control-port", str(args.control_port),
                        "--upstream-proxy", f"http://127.0.0.1:{mock_port}",
                    ]
                    actual_engine = "python"
            else:
                cmd = [
                    sys.executable,
                    "-m",
                    "tests.conformance.python_engine",
                    "--proxy-port", str(args.proxy_port),
                    "--control-port", str(args.control_port),
                    "--upstream-proxy", f"http://127.0.0.1:{mock_port}",
                ]
                actual_engine = "python"

            print(f"[*] Starting {actual_engine.upper()} proxy engine on ports {args.proxy_port}/{args.control_port}...")
            proxy_proc = subprocess.Popen(
                cmd,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                cwd=_repo_root,
            )
            spawned_proxy = True

            ready = await wait_for_ports([args.proxy_port, args.control_port], timeout=7.0)
            if not ready:
                stdout, stderr = (proxy_proc.communicate(timeout=1.0) if proxy_proc.poll() is not None else (b"", b""))
                sys.stderr.write(f"[-] Failed to start engine on ports {args.proxy_port}/{args.control_port}\n")
                if stdout:
                    sys.stderr.write(f"STDOUT: {stdout.decode('utf-8', errors='replace')}\n")
                if stderr:
                    sys.stderr.write(f"STDERR: {stderr.decode('utf-8', errors='replace')}\n")
                return 1
            print(f"[+] Engine successfully started and listening on ports {args.proxy_port} and {args.control_port}")
        else:
            print(f"[*] Attaching to already-running proxy on ports {args.proxy_port}/{args.control_port}")
            # Configure running proxy to route through mock upstream
            async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{args.control_port}", timeout=3.0, trust_env=False) as cfg_client:
                r_status = await cfg_client.get("/api/status")
                if r_status.status_code == 200:
                    detected_engine = r_status.json().get("engine", "python")
                    if args.engine == "auto":
                        actual_engine = detected_engine
                    elif args.engine != detected_engine:
                        print(f"[!] Warning: requested engine is '{args.engine}', but running proxy is '{detected_engine}'")
                        actual_engine = detected_engine
                    else:
                        actual_engine = args.engine

                r_cfg = await cfg_client.get("/api/config")
                if r_cfg.status_code == 200 and r_cfg.json().get("ok"):
                    old_cfg = r_cfg.json()["config"]
                    revert_config = {
                        "upstream_proxy": old_cfg.get("upstream_proxy", ""),
                        "direct_mode": old_cfg.get("direct_mode", False),
                        "verify_upstream_tls": old_cfg.get("verify_upstream_tls", True),
                    }
                await cfg_client.post(
                    "/api/config/apply",
                    json={
                        "upstream_proxy": f"http://127.0.0.1:{mock_port}",
                        "direct_mode": False,
                        "verify_upstream_tls": False,
                    },
                )

        # 3. Retrieve Root CA certificate from proxy over HTTP (Pure Black-Box)
        ca_cert_url = f"http://127.0.0.1:{args.proxy_port}/ca.crt"
        async with httpx.AsyncClient(trust_env=False) as bootstrap_client:
            ca_resp = await bootstrap_client.get(ca_cert_url)
            assert ca_resp.status_code == 200, f"Failed to download CA from {ca_cert_url}"
            ca_pem = ca_resp.text

        ca_ssl_ctx = ssl.create_default_context()
        ca_ssl_ctx.load_verify_locations(cadata=ca_pem)

        # 4. Initialize test clients and context
        async with httpx.AsyncClient(
            proxy=f"http://127.0.0.1:{args.proxy_port}",
            verify=ca_ssl_ctx,
            timeout=10.0,
            trust_env=False,
        ) as proxy_client, httpx.AsyncClient(
            base_url=f"http://127.0.0.1:{args.control_port}",
            timeout=6.0,
            trust_env=False,
        ) as ctrl_client, httpx.AsyncClient(
            base_url=f"http://127.0.0.1:{args.proxy_port}",
            timeout=6.0,
            trust_env=False,
        ) as direct_client:
            ctx = ConformanceContext(
                proxy_port=args.proxy_port,
                control_port=args.control_port,
                client=proxy_client,
                control_client=ctrl_client,
                direct_client=direct_client,
                mock_upstream=mock_upstream,
                ca_ssl_context=ca_ssl_ctx,
            )

            # 5. Filter cases
            selected_cases: List[TestCase] = []
            filter_re = re.compile(args.filter, re.IGNORECASE) if args.filter else None
            for case in ALL_CASES:
                if not filter_re or filter_re.search(case.name) or filter_re.search(case.category):
                    selected_cases.append(case)

            print(f"[*] Executing {len(selected_cases)} / {len(ALL_CASES)} contract test cases...\n")

            results = []
            passed_count = 0
            failed_count = 0

            # 6. Run test loop
            for idx, case in enumerate(selected_cases, 1):
                start_t = time.perf_counter()
                status = "PASS"
                err_msg: Optional[str] = None
                try:
                    await case.func(ctx)
                    passed_count += 1
                except Exception as e:
                    status = "FAIL"
                    failed_count += 1
                    err_msg = f"{type(e).__name__}: {e}"
                elapsed_ms = round((time.perf_counter() - start_t) * 1000, 2)

                results.append({
                    "name": case.name,
                    "category": case.category,
                    "status": status,
                    "duration_ms": elapsed_ms,
                    "description": case.description,
                    "error": err_msg,
                })

                prefix = f"[{status}]"
                cat_tag = f"[{case.category}]"
                print(f"  {prefix:6} ({idx:2}/{len(selected_cases):2}) {cat_tag:28} {case.name:40} ({elapsed_ms:6.1f}ms)")
                if err_msg:
                    print(f"         >>> ERROR: {err_msg}")

            # 7. Record Baseline if requested
            if args.record_baseline:
                baseline_path = Path(_repo_root) / "tests" / "conformance" / f"golden_baseline_{actual_engine}.json"
                baseline_data = {
                    "engine": actual_engine,
                    "timestamp": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    "total_cases": len(selected_cases),
                    "passed_cases": passed_count,
                    "failed_cases": failed_count,
                    "pass_rate_pct": round((passed_count / len(selected_cases)) * 100, 2) if selected_cases else 0.0,
                    "proxy_port": args.proxy_port,
                    "control_port": args.control_port,
                    "results": results,
                }
                baseline_path.write_text(json.dumps(baseline_data, indent=2, ensure_ascii=False), encoding="utf-8")
                print(f"\n[+] Golden baseline recorded -> {baseline_path.resolve()}")

            # 8. Summary Output
            print("\n" + "=" * 70)
            if failed_count == 0:
                print(f"   [+] ALL {passed_count} CONFORMANCE BEHAVIOR CONTRACTS PASSED (100.0%)")
            else:
                print(f"   [-] CONFORMANCE SUITE FAILED: {failed_count} / {len(selected_cases)} failed")
            print("=" * 70 + "\n")

            return 0 if failed_count == 0 else 1

    finally:
        # Revert config if attached to pre-existing instance
        if revert_config:
            try:
                async with httpx.AsyncClient(base_url=f"http://127.0.0.1:{args.control_port}", timeout=2.0, trust_env=False) as cfg_client:
                    await cfg_client.post("/api/config/apply", json=revert_config)
            except Exception:
                pass

        # Cleanup proxy subprocess if spawned
        if proxy_proc and spawned_proxy:
            proxy_proc.terminate()
            try:
                proxy_proc.wait(timeout=3.0)
            except Exception:
                proxy_proc.kill()

        # Stop mock upstream
        await mock_upstream.stop()

        # Restore repo config.json if it existed
        if saved_cfg_content is not None and repo_cfg_path.is_file():
            try:
                repo_cfg_path.write_text(saved_cfg_content, encoding="utf-8")
            except Exception:
                pass


def main():
    exit_code = asyncio.run(run_suite_async())
    sys.exit(exit_code)


if __name__ == "__main__":
    main()
