package proxy

import "fmt"

func GetPAC(host string, port int) string {
	return fmt.Sprintf(`function FindProxyForURL(url, host) {
    if (
        shExpMatch(host, "*.granbluefantasy.jp") ||
        shExpMatch(host, "granbluefantasy.jp") ||
        shExpMatch(host, "*.granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.akamaized.net") ||
        shExpMatch(host, "*.granbluefantasy.akamaized.net") ||
        shExpMatch(host, "gbf.akamaized.net") ||
        shExpMatch(host, "*granbluefantasy.akamaized.net") ||
        shExpMatch(host, "*granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "*gbf.akamaized.net") ||
        shExpMatch(host, "prd-game-a-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a1-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a2-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a3-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a4-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a5-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a1-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a2-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a3-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a4-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a5-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "*.game.mbga.jp") ||
        shExpMatch(host, "gbf.game.mbga.jp") ||
        shExpMatch(host, "*.sp.pf.mbga.jp") ||
        shExpMatch(host, "*.pf.mbga.jp") ||
        shExpMatch(host, "*.sp.mbga.jp") ||
        shExpMatch(host, "sp.mbga.jp") ||
        shExpMatch(host, "*.mbga.jp") ||
        shExpMatch(host, "mbga.jp") ||
        shExpMatch(host, "*.connect.mobage.jp") ||
        shExpMatch(host, "connect.mobage.jp") ||
        shExpMatch(host, "*.game.mobage.jp") ||
        shExpMatch(host, "*.mobage.jp") ||
        shExpMatch(host, "mobage.jp")
    ) {
        return "PROXY %s:%d; DIRECT";
    }
    return "DIRECT";
}
`, host, port)
}

func GetLandingHTML(lanIP string, port int) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>GBF 加速器 局域网配置</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 24px; background: #f8f9fa; color: #212529; }
        .card { background: #ffffff; padding: 24px; border-radius: 12px; max-width: 620px; margin: auto; box-shadow: 0 4px 16px rgba(0,0,0,0.06); }
        h1 { color: #0d6efd; font-size: 18px; margin-top: 0; }
        .btn { display: inline-block; background: #0d6efd; color: #fff; padding: 8px 16px; border-radius: 4px; text-decoration: none; font-size: 14px; margin: 6px 0; }
        code { background: #e9ecef; padding: 2px 6px; border-radius: 4px; font-family: Consolas, monospace; word-break: break-all; }
        ol { padding-left: 20px; line-height: 1.7; }
        li { margin-bottom: 10px; }
    </style>
</head>
<body>
    <div class="card">
        <h1>GBF 加速器 局域网配置</h1>
        <p>移动设备（iOS / Android）配置指引：</p>
        <ol>
            <li><b>安装根证书：</b><br>
                <a href="/ca.crt" class="btn">下载根证书 (ca.crt)</a>
            </li>
            <li><b>配置 Wi-Fi 代理：</b><br>
                URL: <code>http://%s:%d/proxy.pac</code>
            </li>
        </ol>
    </div>
</body>
</html>`, lanIP, port)
}
