import asyncio
import traceback
import httpx
from gbf_proxy import client_handler, init_http_client, close_http_client
from cert_manager import get_server_ssl_context

async def safe_handler(r, w, ssl_ctx):
    try:
        await client_handler(r, w, ssl_ctx)
    except Exception:
        print("[!] Exception in handler:")
        traceback.print_exc()

async def test():
    await init_http_client()
    ssl_ctx = get_server_ssl_context()
    srv = await asyncio.start_server(lambda r, w: safe_handler(r, w, ssl_ctx), "127.0.0.1", 8125)
    print("Server ready on 8125")

    try:
        with httpx.Client(proxy="http://127.0.0.1:8125", verify="d:/acgpower/gbf_speed_proxy/certs/ca.crt", timeout=5.0) as c:
            resp = c.get("https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css")
            print("Response:", resp.status_code, resp.headers.get("x-proxy-cache"))
    except Exception:
        print("[!] Exception in client:")
        traceback.print_exc()
    finally:
        srv.close()
        await srv.wait_closed()
        await close_http_client()

if __name__ == "__main__":
    asyncio.run(test())
