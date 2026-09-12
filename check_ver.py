import httpx
import re

with httpx.Client(proxy="http://127.0.0.1:7897", timeout=10.0, follow_redirects=True) as c:
    resp = c.get("https://game.granbluefantasy.jp/")
    print("URL:", resp.url)
    print("Status:", resp.status_code)
    matches = re.findall(r'/assets/(\d+)/', resp.text)
    print("Asset timestamps found in HTML:", set(matches))
    for line in resp.text.splitlines():
        if "version" in line.lower() or "manifest" in line.lower():
            print("Line:", line.strip())
