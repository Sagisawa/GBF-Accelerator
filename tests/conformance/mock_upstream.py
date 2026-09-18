"""
Lightweight Mock Upstream Server for GBF-Accelerator Conformance Test Suite.
Simulates edge case wire behaviors:
1. Multi-line Set-Cookie preservation
2. Chunked Transfer-Encoding POST bodies
3. Mid-stream disconnects for POST zero-retry contract
4. Safe retry once on connection drop for whitelisted read-only GET endpoints
5. SingleFlight coalescing with simulated upstream delay
6. SingleFlight failure recovery after upstream 502
7. Conditional GET with 304 Not Modified
8. Anti-cache 502/503 HTML error pages
9. Offline fallback and byte fidelity testing
10. Upstream CONNECT proxy tunneling with TLS interception
"""

import asyncio
import collections
import datetime
import ipaddress
import os
import ssl
import tempfile
import urllib.parse
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID


class MockUpstreamServer:
    def __init__(self, host: str = "127.0.0.1", port: int = 0):
        self.host = host
        self.requested_port = port
        self.port: int = 0
        self.server: Optional[asyncio.AbstractServer] = None
        self._temp_dir = tempfile.TemporaryDirectory()

        # Tracking state
        self.request_counts: Dict[str, int] = collections.defaultdict(int)
        self.request_history: List[Dict[str, Any]] = []
        self.scenarios: Dict[str, Dict[str, Any]] = {}
        self._lock = asyncio.Lock()

        # Generate self-signed TLS cert for CONNECT tunneling
        self.ssl_ctx = self._init_tls_context()

    def _init_tls_context(self) -> ssl.SSLContext:
        """Generate an ad-hoc certificate for GBF hosts to terminate TLS inside CONNECT tunnels."""
        key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        subject = x509.Name([
            x509.NameAttribute(NameOID.COMMON_NAME, "mock-upstream.conformance.local"),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, "GBF Conformance Mock"),
        ])
        sans = [
            x509.DNSName("game.granbluefantasy.jp"),
            x509.DNSName("granbluefantasy.jp"),
            x509.DNSName("granbluefantasy.com"),
            x509.DNSName("prd-game-a-granbluefantasy.akamaized.net"),
            x509.DNSName("prd-game-a1-granbluefantasy.akamaized.net"),
            x509.DNSName("gbf.game.mbga.jp"),
            x509.DNSName("localhost"),
            x509.IPAddress(ipaddress.ip_address("127.0.0.1")),
        ]
        cert = (
            x509.CertificateBuilder()
            .subject_name(subject)
            .issuer_name(subject)
            .public_key(key.public_key())
            .serial_number(1000)
            .not_valid_before(datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=1))
            .not_valid_after(datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=365))
            .add_extension(x509.SubjectAlternativeName(sans), critical=False)
            .sign(key, hashes.SHA256())
        )

        cert_path = Path(self._temp_dir.name) / "mock_cert.pem"
        key_path = Path(self._temp_dir.name) / "mock_key.pem"
        cert_path.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
        key_path.write_bytes(
            key.private_bytes(
                serialization.Encoding.PEM,
                serialization.PrivateFormat.PKCS8,
                serialization.NoEncryption(),
            )
        )

        ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
        ctx.load_cert_chain(str(cert_path), str(key_path))
        return ctx

    async def start(self) -> int:
        """Start the mock upstream server and bind to an available port."""
        self.server = await asyncio.start_server(
            self._handle_connection,
            self.host,
            self.requested_port,
        )
        self.port = self.server.sockets[0].getsockname()[1]
        return self.port

    async def stop(self):
        """Stop mock server and clean up resources."""
        if self.server:
            self.server.close()
            await self.server.wait_closed()
            self.server = None
        try:
            self._temp_dir.cleanup()
        except Exception:
            pass

    def get_port(self) -> int:
        return self.port

    def get_request_count(self, method: str, path: str) -> int:
        clean_path = path.split("?")[0].lower()
        if "://" in clean_path:
            clean_path = urllib.parse.urlsplit(clean_path).path.lower()
        if not clean_path.startswith("/"):
            clean_path = "/" + clean_path
        key = f"{method.upper()} {clean_path}"
        return self.request_counts.get(key, 0)

    def reset_counts(self):
        self.request_counts.clear()
        self.request_history.clear()

    def set_scenario(self, path: str, behavior: str, **kwargs):
        """Configure scenario behavior for a given path."""
        clean_path = path.split("?")[0].lower()
        if "://" in clean_path:
            clean_path = urllib.parse.urlsplit(clean_path).path.lower()
        if not clean_path.startswith("/"):
            clean_path = "/" + clean_path
        self.scenarios[clean_path] = {"behavior": behavior, **kwargs}

    def get_last_request(self, path: Optional[str] = None) -> Optional[Dict[str, Any]]:
        """Retrieve the most recent request recorded, optionally filtered by path."""
        clean_path = None
        if path is not None:
            clean_path = path.split("?")[0].lower()
            if "://" in clean_path:
                clean_path = urllib.parse.urlsplit(clean_path).path.lower()
            if not clean_path.startswith("/"):
                clean_path = "/" + clean_path

        for req in reversed(self.request_history):
            if clean_path is None or req["path"] == clean_path:
                return req
        return None

    async def _handle_connection(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        try:
            first_line = await reader.readline()
            if not first_line:
                writer.close()
                return

            line_str = first_line.decode("iso-8859-1").strip()
            parts = line_str.split()
            if len(parts) < 2:
                writer.close()
                return

            method, target = parts[0].upper(), parts[1]

            # 1. CONNECT Tunnel (Upstream proxy mode)
            if method == "CONNECT":
                # Read remaining CONNECT headers
                while True:
                    h = await reader.readline()
                    if not h or h in (b"\r\n", b"\n"):
                        break
                writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                await writer.drain()

                # Upgrade connection to TLS
                await writer.start_tls(self.ssl_ctx)

                # Loop to handle HTTP requests inside TLS tunnel
                while True:
                    keep_alive = await self._process_http_request(reader, writer, is_tls=True)
                    if not keep_alive:
                        break
            else:
                # 2. Direct plain HTTP request
                await self._process_http_request(reader, writer, is_tls=False, initial_line=line_str)
        except (asyncio.IncompleteReadError, ConnectionResetError, BrokenPipeError):
            pass
        except Exception:
            pass
        finally:
            try:
                writer.close()
                await writer.wait_closed()
            except Exception:
                pass

    async def _process_http_request(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
        is_tls: bool = False,
        initial_line: Optional[str] = None,
    ) -> bool:
        if initial_line:
            line_str = initial_line
        else:
            line_bytes = await reader.readline()
            if not line_bytes:
                return False
            line_str = line_bytes.decode("iso-8859-1").strip()

        parts = line_str.split()
        if len(parts) < 2:
            return False

        method, raw_url = parts[0].upper(), parts[1]

        # Read headers
        headers: Dict[str, str] = {}
        while True:
            h_line = await reader.readline()
            if not h_line or h_line in (b"\r\n", b"\n"):
                break
            try:
                h_str = h_line.decode("iso-8859-1").strip()
                if ":" in h_str:
                    k, v = h_str.split(":", 1)
                    headers[k.strip().lower()] = v.strip()
            except Exception:
                pass

        # Read body
        body = b""
        if headers.get("transfer-encoding", "").lower() == "chunked":
            chunks = []
            while True:
                size_line = await reader.readline()
                if not size_line:
                    break
                size_str = size_line.strip().split(b";")[0]
                try:
                    chunk_size = int(size_str, 16)
                except ValueError:
                    break
                if chunk_size == 0:
                    await reader.readline()  # consume trailing \r\n
                    break
                chunk_data = await reader.readexactly(chunk_size)
                chunks.append(chunk_data)
                await reader.readline()  # consume trailing \r\n
            body = b"".join(chunks)
        else:
            content_length = int(headers.get("content-length", 0))
            if content_length > 0:
                body = await reader.readexactly(content_length)

        # Parse normalized path
        parsed_url = urllib.parse.urlsplit(raw_url)
        norm_path = parsed_url.path.lower()
        if not norm_path.startswith("/"):
            norm_path = "/" + norm_path

        # Track request
        peer = writer.get_extra_info("peername")
        conn_id = id(writer)
        async with self._lock:
            key = f"{method} {norm_path}"
            self.request_counts[key] += 1
            cur_count = self.request_counts[key]
            self.request_history.append({
                "method": method,
                "path": norm_path,
                "headers": headers,
                "body_len": len(body),
                "body": body,
                "count": cur_count,
                "conn_id": conn_id,
                "peer": peer,
            })

        # Check for configured scenario
        scenario = self.scenarios.get(norm_path, {})
        behavior = scenario.get("behavior", "")

        # ---------------- Scenario: Mid-stream drop ----------------
        if behavior == "drop":
            writer.close()
            return False

        # ---------------- Scenario: Drop once, then succeed ----------------
        if behavior == "drop_once_then_ok":
            if cur_count == 1:
                writer.close()
                return False
            resp_body = b'{"status": "condition_ok"}'
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", "application/json"), ("Content-Length", str(len(resp_body)))],
                resp_body,
            )
            return True

        # ---------------- Scenario: Fail once with 502, then succeed ----------------
        if behavior == "fail_once_502":
            if cur_count == 1:
                err_body = b"<html><body><h1>502 Bad Gateway</h1></body></html>"
                await self._send_response(
                    writer,
                    502,
                    "Bad Gateway",
                    [("Content-Type", "text/html"), ("Content-Length", str(len(err_body)))],
                    err_body,
                )
                return True
            rec_body = b"\x89PNG\r\n\x1a\nrecovered_png"
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", "image/png"), ("Content-Length", str(len(rec_body)))],
                rec_body,
            )
            return True

        # ---------------- Scenario: Delay (SingleFlight coalescing) ----------------
        if behavior == "delay":
            delay = scenario.get("delay_sec", 0.15)
            await asyncio.sleep(delay)
            asset_body = scenario.get("content", b"\x89PNG\r\n\x1a\nslow_png_asset_data")
            await self._send_response(
                writer,
                200,
                "OK",
                [
                    ("Content-Type", "image/png"),
                    ("ETag", '"sf-png-etag"'),
                    ("Content-Length", str(len(asset_body))),
                ],
                asset_body,
            )
            return True

        # ---------------- Scenario: Multi Set-Cookie ----------------
        if behavior == "cookies" or norm_path.endswith("/multi_cookie"):
            cookie_body = b'{"status": "cookies_set"}'
            headers_list = [
                ("Content-Type", "application/json"),
                ("Content-Length", str(len(cookie_body))),
                ("Set-Cookie", "session_id=sess_m2_123; Path=/; HttpOnly"),
                ("Set-Cookie", "auth_token=tok_m2_456; Path=/; Secure"),
            ]
            await self._send_response(writer, 200, "OK", headers_list, cookie_body)
            return True

        # ---------------- Scenario: 502/503 HTML error ----------------
        if behavior in ("error_html", "502_error"):
            err_html = b"<html><body><h1>502 Bad Gateway</h1></body></html>"
            await self._send_response(
                writer,
                502,
                "Bad Gateway",
                [("Content-Type", "text/html"), ("Content-Length", str(len(err_html)))],
                err_html,
            )
            return True

        # ---------------- Scenario: Conditional GET 304 ETag ----------------
        if behavior == "304_etag":
            etag = scenario.get("etag", '"conformance-etag-css"')
            content = scenario.get("content", b"/* 304 test */\n.a { color: red; }")
            req_if_none_match = headers.get("if-none-match", "")
            if req_if_none_match and req_if_none_match == etag:
                await self._send_response(
                    writer,
                    304,
                    "Not Modified",
                    [("ETag", etag), ("Content-Length", "0")],
                    b"",
                )
                return True
            await self._send_response(
                writer,
                200,
                "OK",
                [
                    ("Content-Type", "text/css"),
                    ("ETag", etag),
                    ("Content-Length", str(len(content))),
                ],
                content,
            )
            return True

        # ---------------- Scenario: Custom content OK ----------------
        if behavior == "ok":
            content = scenario.get("content", b"conformance_ok")
            content_type = scenario.get("content_type", "application/octet-stream")
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", content_type), ("Content-Length", str(len(content)))],
                content,
            )
            return True

        # ---------------- Default GBF Endpoint Handlers ----------------
        if norm_path == "/ob/r":
            resp_body = b'{"result": "ok", "ob": true}'
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", "application/json"), ("Content-Length", str(len(resp_body)))],
                resp_body,
            )
            return True

        if norm_path == "/rest/error/js":
            resp_body = b'{"result": "ok"}'
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", "application/json"), ("Content-Length", str(len(resp_body)))],
                resp_body,
            )
            return True

        # Static assets
        static_exts = (".css", ".js", ".png", ".jpg", ".jpeg", ".webp", ".mp3", ".woff2")
        if any(norm_path.endswith(ext) for ext in static_exts):
            dummy_asset = f"/* mock asset {norm_path} */\n".encode("utf-8")
            ct = "text/css" if norm_path.endswith(".css") else ("application/javascript" if norm_path.endswith(".js") else "image/png")
            await self._send_response(
                writer,
                200,
                "OK",
                [
                    ("Content-Type", ct),
                    ("ETag", f'"{abs(hash(norm_path))}"'),
                    ("Content-Length", str(len(dummy_asset))),
                ],
                dummy_asset,
            )
            return True

        # Reject system file inspection paths
        if norm_path.endswith((".ini", ".passwd", "passwd")):
            err_body = b"404 Not Found"
            await self._send_response(
                writer,
                404,
                "Not Found",
                [("Content-Type", "text/plain"), ("Content-Length", str(len(err_body)))],
                err_body,
            )
            return True

        # Root HTML
        if norm_path in ("/", "/index.html"):
            html = b"<!DOCTYPE html><html><head><title>\xe3\x82\xb0\xe3\x83\xa9\xe3\x83\xb3\xe3\x83\x96\xe3\x83\xab\xe3\x83\xbc\xe3\x83\x95\xe3\x82\xa1\xe3\x83\xb3\xe3\x82\xbf\xe3\x82\xba\xe3\x83\xbc</title></head><body>GBF Mock</body></html>"
            await self._send_response(
                writer,
                200,
                "OK",
                [("Content-Type", "text/html; charset=utf-8"), ("Content-Length", str(len(html)))],
                html,
            )
            return True

        # Fallback JSON response
        fallback_body = b'{"mock": true, "path": "' + norm_path.encode("utf-8") + b'"}'
        await self._send_response(
            writer,
            200,
            "OK",
            [("Content-Type", "application/json"), ("Content-Length", str(len(fallback_body)))],
            fallback_body,
        )
        return True

    async def _send_response(
        self,
        writer: asyncio.StreamWriter,
        status_code: int,
        status_text: str,
        headers: List[Tuple[str, str]],
        body: bytes,
    ):
        res_lines = [f"HTTP/1.1 {status_code} {status_text}"]
        for k, v in headers:
            res_lines.append(f"{k}: {v}")
        res_lines.append("Connection: keep-alive")
        raw_header = ("\r\n".join(res_lines) + "\r\n\r\n").encode("iso-8859-1")
        writer.write(raw_header + body)
        await writer.drain()
