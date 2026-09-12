import asyncio
import httpx
import sys

async def run_test():
    print("[*] Testing GBF Speed Proxy using AsyncClient...")
    async with httpx.AsyncClient(
        proxy="http://127.0.0.1:8124",
        verify="d:/acgpower/gbf_speed_proxy/certs/ca.crt",
        timeout=10.0,
    ) as client:
        # Test 1: Local Cache Hit
        url_cache = "https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css"
        resp = await client.get(url_cache)
        print(f"Test 1 - Cache Hit: status={resp.status_code}, X-Proxy-Cache={resp.headers.get('x-proxy-cache')}, bytes={len(resp.content)}")
        assert resp.status_code == 200
        assert resp.headers.get("x-proxy-cache") == "HIT"

        # Test 2: Mock 200 Endpoint
        url_mock = "https://game.granbluefantasy.jp/rest/error/js"
        resp_mock = await client.get(url_mock)
        print(f"Test 2 - Mock Endpoint: status={resp_mock.status_code}, json={resp_mock.json()}")
        assert resp_mock.status_code == 200

        # Test 3: OPTIONS Preflight Mock
        resp_opt = await client.options("https://game.granbluefantasy.jp/rest/multiraid/start.json")
        print(f"Test 3 - OPTIONS Preflight: status={resp_opt.status_code}, cors={resp_opt.headers.get('access-control-allow-origin')}")
        assert resp_opt.status_code == 200

        # Test 4: Upstream Relay through Clash
        url_upstream = "https://granbluefantasy.jp/"
        resp_up = await client.get(url_upstream)
        print(f"Test 4 - Clash Upstream: status={resp_up.status_code}, location={resp_up.headers.get('location')}")
        assert resp_up.status_code in (200, 301, 302)

        # Test 5: Game portal upstream
        resp_game = await client.get("https://game.granbluefantasy.jp/", follow_redirects=True)
        print(f"Test 5 - Game Page Upstream: status={resp_game.status_code}, title_found={'グランブルーファンタジー' in resp_game.text}")
        assert resp_game.status_code == 200
        assert "グランブルーファンタジー" in resp_game.text

    print("\n[+] ALL TESTS (INCLUDING CLASH UPSTREAM) PASSED SUCCESSFULLY!")

if __name__ == "__main__":
    asyncio.run(run_test())
