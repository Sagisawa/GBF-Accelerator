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
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
    <title>GBF 加速器 局域网移动端配置指引</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            margin: 0;
            padding: 16px;
            background: #f8fafc;
            color: #1e293b;
            line-height: 1.6;
        }
        .container {
            max-width: 640px;
            margin: 0 auto;
        }
        .header {
            text-align: center;
            margin-bottom: 20px;
            padding: 8px 0;
        }
        .header h1 {
            font-size: 20px;
            color: #0f172a;
            margin: 0 0 6px 0;
        }
        .header p {
            font-size: 13px;
            color: #64748b;
            margin: 0;
        }
        .card {
            background: #ffffff;
            border-radius: 12px;
            padding: 18px 20px;
            margin-bottom: 16px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.05);
            border: 1px solid #e2e8f0;
        }
        .card-title {
            font-size: 15px;
            font-weight: 700;
            color: #0284c7;
            margin-top: 0;
            margin-bottom: 12px;
        }
        ol {
            margin: 0;
            padding-left: 20px;
        }
        li {
            margin-bottom: 10px;
            font-size: 13.5px;
        }
        .btn-download {
            display: inline-block;
            background: #0284c7;
            color: #ffffff;
            font-weight: 600;
            padding: 10px 18px;
            border-radius: 8px;
            text-decoration: none;
            font-size: 14px;
            margin: 8px 0;
            text-align: center;
        }
        .url-box {
            background: #f1f5f9;
            border: 1px solid #cbd5e1;
            padding: 8px 12px;
            border-radius: 6px;
            font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
            font-size: 13px;
            color: #2563eb;
            word-break: break-all;
            margin: 6px 0;
            user-select: all;
            -webkit-user-select: all;
        }
        .sub-box {
            background: #f8fafc;
            border-left: 3px solid #0284c7;
            padding: 10px 14px;
            margin: 10px 0;
            border-radius: 0 8px 8px 0;
            font-size: 13px;
        }
        .sub-box b {
            color: #0f172a;
        }
        .faq-item {
            margin-bottom: 12px;
            font-size: 13px;
        }
        .faq-item b {
            color: #0f172a;
            display: block;
            margin-bottom: 3px;
        }
        .faq-item p {
            margin: 0 0 6px 0;
            color: #475569;
        }
        .badge {
            display: inline-block;
            background: #e0f2fe;
            color: #0369a1;
            font-size: 11px;
            font-weight: 600;
            padding: 2px 6px;
            border-radius: 4px;
            margin-left: 4px;
        }
        hr {
            border: 0;
            border-top: 1px dashed #e2e8f0;
            margin: 14px 0;
        }
        code {
            background: #f1f5f9;
            padding: 2px 6px;
            border-radius: 4px;
            font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
            font-size: 12px;
            color: #0f172a;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>GBF 加速器 局域网移动端配置指引</h1>
            <p>在同一 Wi-Fi 局域网下免客户端享受加速</p>
        </div>

        <div class="card">
            <div class="card-title">【第一步：安装并信任根证书（iOS 必需，Android 视情况）】</div>
            <ol>
                <li>确保手机与电脑连接在同一个 Wi-Fi 局域网下。</li>
                <li>
                    手机 Safari 访问：<code>http://%s:%d/ca.crt</code><br>
                    <a href="/ca.crt" class="btn-download">点击下载根证书 (ca.crt)</a><br>
                    <small style="color: #64748b;">（或访问 <code>http://%s:%d/</code> 查看网页版指引）</small>
                </li>
                <li>提示时点击【允许】下载描述文件。</li>
                <li>打开手机系统【设置】-&gt;【已下载描述文件】-&gt; 点击【安装】。</li>
                <li>
                    <b>系统信任证书：</b><br>
                    打开手机【设置】-&gt;【通用】-&gt;【关于本机】-&gt; 底部【证书信任设置】；<br>
                    找到【GBF Local Accelerator Root CA】，打开信任开关。
                </li>
            </ol>
        </div>

        <div class="card">
            <div class="card-title">【第二步：配置手机 Wi-Fi 代理】</div>
            <ol>
                <li>打开手机系统【设置】-&gt;【无线局域网 (Wi-Fi)】。</li>
                <li>点击当前已连接 Wi-Fi 右侧的 ⓘ 图标。</li>
                <li>滑动到底部，点击【配置代理】：</li>
            </ol>

            <div class="sub-box">
                <b>方式 1：自动分流 <span class="badge">推荐，仅游戏素材走代理</span></b>
                <div style="margin-top: 4px;">• 选择【自动】</div>
                <div>• URL 填入：</div>
                <div class="url-box">http://%s:%d/proxy.pac</div>
                <div>• 存储。</div>
            </div>

            <div class="sub-box">
                <b>方式 2：手动代理</b>
                <div style="margin-top: 4px;">• 选择【手动】</div>
                <div>• 服务器填入：<code>%s</code></div>
                <div>• 端口填入：<code>%d</code></div>
                <div>• 存储。</div>
            </div>
        </div>

        <div class="card">
            <div class="card-title" style="color: #475569;">【常见问题】</div>
            <div class="faq-item">
                <b>• 游玩网址与客户端说明：</b>
                <p>建议使用手机浏览器（Safari / Chrome）直接访问：<br>
                <a href="https://game.granbluefantasy.jp" target="_blank" style="color: #0284c7; word-break: break-all;">https://game.granbluefantasy.jp</a></p>
                <p style="color: #64748b; font-size: 12px; line-height: 1.5;">
                说明：SkyLeap 内置使用的是 <code>gbf.game.mbga.jp</code>，该地址主要用于账号登录和跳转，不包含游戏静态素材，无法触发本地缓存加速；在手机浏览器中访问 <code>game.granbluefantasy.jp</code> 才能正常走本地缓存。
                </p>
            </div>
            <hr>
            <div class="faq-item">
                <b>• 手机打不开网页或提示连接超时？</b>
                <p>请检查电脑防火墙是否放行端口 <code>%d</code>，并确认手机和电脑在同一个 Wi-Fi 网络。</p>
            </div>
            <hr>
            <div class="faq-item">
                <b>• iOS 提示证书不受信任或白屏？</b>
                <p>请检查【关于本机】-&gt;【证书信任设置】中的完全信任开关是否已开启。</p>
            </div>
            <hr>
            <div class="faq-item">
                <b>• 局域网 IP 变动？</b>
                <p>若电脑 IP 变化，请在此处查看最新 IP 并更新手机 Wi-Fi 代理设置。</p>
            </div>
        </div>
    </div>
</body>
</html>`, lanIP, port, lanIP, port, lanIP, port, lanIP, port, port)
}
