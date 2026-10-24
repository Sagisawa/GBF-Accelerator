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
        shExpMatch(host, "mobage.jp") ||
        shExpMatch(host, "*.dmm.com") ||
        shExpMatch(host, "dmm.com") ||
        shExpMatch(host, "*.dmm.co.jp") ||
        shExpMatch(host, "dmm.co.jp")
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
        .lang-bar {
            display: flex;
            justify-content: center;
            align-items: center;
            gap: 8px;
            margin-bottom: 16px;
        }
        .lang-btn {
            appearance: none;
            -webkit-appearance: none;
            background: #ffffff;
            border: 1px solid #cbd5e1;
            color: #475569;
            padding: 6px 14px;
            border-radius: 9999px;
            font-size: 13px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.15s ease;
        }
        .lang-btn:active {
            transform: scale(0.96);
        }
        .lang-btn.active {
            background: #0284c7;
            border-color: #0284c7;
            color: #ffffff;
            box-shadow: 0 1px 3px rgba(2, 132, 199, 0.3);
        }
        /* Multilingual element display control */
        body[data-lang="zh"] [lang]:not([lang="zh"]) { display: none !important; }
        body[data-lang="en"] [lang]:not([lang="en"]) { display: none !important; }
        body[data-lang="ja"] [lang]:not([lang="ja"]) { display: none !important; }

        .header {
            text-align: center;
            margin-bottom: 20px;
            padding: 4px 0;
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
<body data-lang="zh">
    <div class="container">
        <div class="lang-bar">
            <button type="button" class="lang-btn active" data-lang-btn="zh" onclick="setLang('zh')">简体中文</button>
            <button type="button" class="lang-btn" data-lang-btn="en" onclick="setLang('en')">English</button>
            <button type="button" class="lang-btn" data-lang-btn="ja" onclick="setLang('ja')">日本語</button>
        </div>

        <div class="header">
            <h1>
                <span lang="zh">GBF 加速器 局域网移动端配置指引</span>
                <span lang="en">GBF-Accelerator Mobile LAN Setup Guide</span>
                <span lang="ja">GBF-Accelerator モバイル LAN 接続ガイド</span>
            </h1>
            <p>
                <span lang="zh">在同一 Wi-Fi 局域网下免客户端享受加速</span>
                <span lang="en">Accelerate game traffic over Wi-Fi without installing client apps</span>
                <span lang="ja">同一 Wi-Fi 環境下でクライアント不要の高速化を実現</span>
            </p>
        </div>

        <div class="card">
            <div class="card-title">
                <span lang="zh">【第一步：安装并信任根证书（iOS 必需，Android 视情况）】</span>
                <span lang="en">[Step 1: Install & Trust Root CA (Required on iOS, Setup-dependent on Android)]</span>
                <span lang="ja">【ステップ 1：ルート証明書のインストールと信頼設定（iOS 必須 / Android は環境による）】</span>
            </div>
            <ol>
                <li>
                    <span lang="zh">确保手机与电脑连接在同一个 Wi-Fi 局域网下。</span>
                    <span lang="en">Ensure your mobile device and PC are connected to the same Wi-Fi network.</span>
                    <span lang="ja">スマホと PC が同一の Wi-Fi ネットワーク（LAN）に接続されていることを確認します。</span>
                </li>
                <li>
                    <span lang="zh">手机 Safari 访问：</span>
                    <span lang="en">Open in mobile Safari: </span>
                    <span lang="ja">スマホの Safari でアクセス：</span>
                    <code>http://%[1]s:%[2]d/ca.crt</code><br>
                    <a href="/ca.crt" class="btn-download">
                        <span lang="zh">点击下载根证书 (ca.crt)</span>
                        <span lang="en">Download Root CA (ca.crt)</span>
                        <span lang="ja">ルート証明書をダウンロード (ca.crt)</span>
                    </a><br>
                    <small style="color: #64748b;">
                        <span lang="zh">（或访问 <code>http://%[1]s:%[2]d/</code> 查看网页版指引）</span>
                        <span lang="en">(Or visit <code>http://%[1]s:%[2]d/</code> to view this guide)</span>
                        <span lang="ja">（または <code>http://%[1]s:%[2]d/</code> で本ガイドを表示）</span>
                    </small>
                </li>
                <li>
                    <span lang="zh">提示时点击【允许】下载描述文件。</span>
                    <span lang="en">Tap [Allow] when prompted to download the configuration profile.</span>
                    <span lang="ja">確認画面で「許可」をタップしてプロファイルをダウンロードします。</span>
                </li>
                <li>
                    <span lang="zh">打开手机系统【设置】-&gt;【已下载描述文件】-&gt; 点击【安装】。</span>
                    <span lang="en">Open iOS [Settings] -&gt; [Profile Downloaded] -&gt; tap [Install].</span>
                    <span lang="ja">iOS の「設定」-&gt;「プロファイルがダウンロード済み」を開き、「インストール」をタップします。</span>
                </li>
                <li>
                    <b>
                        <span lang="zh">系统信任证书：</span>
                        <span lang="en">Trust Certificate in System:</span>
                        <span lang="ja">ルート証明書の信頼設定：</span>
                    </b><br>
                    <span lang="zh">打开手机【设置】-&gt;【通用】-&gt;【关于本机】-&gt; 底部【证书信任设置】；<br>找到【GBF Local Accelerator Root CA】，打开信任开关。</span>
                    <span lang="en">Open [Settings] -&gt; [General] -&gt; [About] -&gt; bottom [Certificate Trust Settings];<br>Locate [GBF Local Accelerator Root CA] and turn ON the trust switch.</span>
                    <span lang="ja">「設定」-&gt;「一般」-&gt;「情報」-&gt; 画面一番下の「証明書信頼設定」を開きます。<br>「GBF Local Accelerator Root CA」を見つけ、信頼スイッチをオンにします。</span>
                </li>
            </ol>
        </div>

        <div class="card">
            <div class="card-title">
                <span lang="zh">【第二步：配置手机 Wi-Fi 代理】</span>
                <span lang="en">[Step 2: Configure Mobile Wi-Fi Proxy]</span>
                <span lang="ja">【ステップ 2：スマホの Wi-Fi プロキシ設定】</span>
            </div>
            <ol>
                <li>
                    <span lang="zh">打开手机系统【设置】-&gt;【无线局域网 (Wi-Fi)】。</span>
                    <span lang="en">Open phone [Settings] -&gt; [Wi-Fi].</span>
                    <span lang="ja">スマホの「設定」-&gt;「Wi-Fi」を開きます。</span>
                </li>
                <li>
                    <span lang="zh">点击当前已连接 Wi-Fi 右侧的 ⓘ 图标。</span>
                    <span lang="en">Tap the ⓘ icon next to your currently connected Wi-Fi network.</span>
                    <span lang="ja">接続中の Wi-Fi 名の右側にある ⓘ アイコンをタップします。</span>
                </li>
                <li>
                    <span lang="zh">滑动到底部，点击【配置代理】：</span>
                    <span lang="en">Scroll down to the bottom and tap [Configure Proxy]:</span>
                    <span lang="ja">最下部までスクロールし、「プロキシを構成」をタップします：</span>
                </li>
            </ol>

            <div class="sub-box">
                <b>
                    <span lang="zh">方式 1：自动分流</span>
                    <span lang="en">Method 1: Auto PAC Routing</span>
                    <span lang="ja">方法 1：PAC 自動分流</span>
                    <span class="badge">
                        <span lang="zh">推荐，仅游戏素材走代理</span>
                        <span lang="en">Recommended: Proxies game assets only</span>
                        <span lang="ja">推奨（ゲーム素材のみプロキシ経由）</span>
                    </span>
                </b>
                <div style="margin-top: 4px;">
                    <span lang="zh">• 选择【自动】</span>
                    <span lang="en">• Select [Automatic]</span>
                    <span lang="ja">• 「自動」を選択</span>
                </div>
                <div>
                    <span lang="zh">• URL 填入：</span>
                    <span lang="en">• URL:</span>
                    <span lang="ja">• URL に入力：</span>
                </div>
                <div class="url-box">http://%[1]s:%[2]d/proxy.pac</div>
                <div>
                    <span lang="zh">• 存储。</span>
                    <span lang="en">• Save.</span>
                    <span lang="ja">• 右上の「保存」をタップ。</span>
                </div>
            </div>

            <div class="sub-box">
                <b>
                    <span lang="zh">方式 2：手动代理</span>
                    <span lang="en">Method 2: Manual Proxy</span>
                    <span lang="ja">方法 2：手動プロキシ</span>
                </b>
                <div style="margin-top: 4px;">
                    <span lang="zh">• 选择【手动】</span>
                    <span lang="en">• Select [Manual]</span>
                    <span lang="ja">• 「手動」を選択</span>
                </div>
                <div>
                    <span lang="zh">• 服务器填入：</span>
                    <span lang="en">• Server: </span>
                    <span lang="ja">• サーバ：</span>
                    <code>%[1]s</code>
                </div>
                <div>
                    <span lang="zh">• 端口填入：</span>
                    <span lang="en">• Port: </span>
                    <span lang="ja">• ポート：</span>
                    <code>%[2]d</code>
                </div>
                <div>
                    <span lang="zh">• 存储。</span>
                    <span lang="en">• Save.</span>
                    <span lang="ja">• 右上の「保存」をタップ。</span>
                </div>
            </div>
        </div>

        <div class="card">
            <div class="card-title" style="color: #475569;">
                <span lang="zh">【常见问题】</span>
                <span lang="en">[Frequently Asked Questions]</span>
                <span lang="ja">【よくある質問（FAQ）】</span>
            </div>
            <div class="faq-item">
                <b>
                    <span lang="zh">• 游玩网址与客户端说明：</span>
                    <span lang="en">• Game URL & Client Instructions:</span>
                    <span lang="ja">• ゲーム URL とクライアントに関する説明：</span>
                </b>
                <p>
                    <span lang="zh">建议使用手机浏览器（Safari / Chrome）直接访问：<br></span>
                    <span lang="en">We recommend visiting directly via mobile browser (Safari / Chrome):<br></span>
                    <span lang="ja">スマホのブラウザ（Safari / Chrome 等）から直接以下を開くことを推奨します：<br></span>
                    <a href="https://game.granbluefantasy.jp" target="_blank" style="color: #0284c7; word-break: break-all;">https://game.granbluefantasy.jp</a>
                </p>
                <p style="color: #64748b; font-size: 12px; line-height: 1.5;">
                    <span lang="zh">说明：SkyLeap 内置使用的是 <code>gbf.game.mbga.jp</code>，该地址主要用于账号登录和跳转，不包含游戏静态素材，无法触发本地缓存加速；在手机浏览器中访问 <code>game.granbluefantasy.jp</code> 才能正常走本地缓存。</span>
                    <span lang="en">Note: SkyLeap uses <code>gbf.game.mbga.jp</code> internally, which is only used for authentication and redirection without game assets. Visiting <code>game.granbluefantasy.jp</code> in your mobile browser ensures game assets are accelerated via local cache.</span>
                    <span lang="ja">注意：SkyLeap アプリは内部で <code>gbf.game.mbga.jp</code> を使用しており、認証専用で素材を含まないためキャッシュによる高速化が機能しません。ブラウザから <code>game.granbluefantasy.jp</code> にアクセスすることでローカルキャッシュが正常に機能します。</span>
                </p>
            </div>
            <hr>
            <div class="faq-item">
                <b>
                    <span lang="zh">• 手机打不开网页或提示连接超时？</span>
                    <span lang="en">• Phone cannot connect or timed out?</span>
                    <span lang="ja">• 接続できない／タイムアウトになる場合：</span>
                </b>
                <p>
                    <span lang="zh">请检查电脑防火墙是否放行端口 <code>%[2]d</code>，并确认手机和电脑在同一个 Wi-Fi 网络。</span>
                    <span lang="en">Please check that your PC firewall allows inbound connections on port <code>%[2]d</code>, and ensure your phone and PC are on the same Wi-Fi network.</span>
                    <span lang="ja">PC のファイアウォールでポート <code>%[2]d</code> が許可されているか確認し、スマホと PC が同一 Wi-Fi に接続されているか確認してください。</span>
                </p>
            </div>
            <hr>
            <div class="faq-item">
                <b>
                    <span lang="zh">• iOS 提示证书不受信任或白屏？</span>
                    <span lang="en">• iOS shows untrusted certificate or blank screen?</span>
                    <span lang="ja">• iOS で証明書が信頼されない／画面が真っ白になる場合：</span>
                </b>
                <p>
                    <span lang="zh">请检查【关于本机】-&gt;【证书信任设置】中的完全信任开关是否已开启。</span>
                    <span lang="en">Please verify that [General] -&gt; [About] -&gt; [Certificate Trust Settings] has the full trust switch turned ON for GBF Local Accelerator Root CA.</span>
                    <span lang="ja">「一般」-&gt;「情報」-&gt;「証明書信頼設定」で、GBF Local Accelerator Root CA の信頼スイッチがオンになっているか確認してください。</span>
                </p>
            </div>
            <hr>
            <div class="faq-item">
                <b>
                    <span lang="zh">• 局域网 IP 变动？</span>
                    <span lang="en">• Local LAN IP address changed?</span>
                    <span lang="ja">• LAN IP アドレスが変わった場合：</span>
                </b>
                <p>
                    <span lang="zh">若电脑 IP 变化，请在此处查看最新 IP 并更新手机 Wi-Fi 代理设置。</span>
                    <span lang="en">If your PC's IP address changes, check the updated IP here and adjust your phone's Wi-Fi proxy setting.</span>
                    <span lang="ja">PC の IP アドレスが変わった場合は、本画面で最新 IP を確認しスマホ側のプロキシ設定を更新してください。</span>
                </p>
            </div>
        </div>
    </div>

    <script>
    function setLang(lang) {
        document.body.setAttribute('data-lang', lang);
        try { localStorage.setItem('gbf_landing_lang', lang); } catch (e) {}
        var btns = document.querySelectorAll('[data-lang-btn]');
        for (var i = 0; i < btns.length; i++) {
            if (btns[i].getAttribute('data-lang-btn') === lang) {
                btns[i].classList.add('active');
            } else {
                btns[i].classList.remove('active');
            }
        }
        var titles = {
            zh: 'GBF 加速器 局域网移动端配置指引',
            en: 'GBF-Accelerator Mobile LAN Setup Guide',
            ja: 'GBF-Accelerator モバイル LAN 接続ガイド'
        };
        if (titles[lang]) document.title = titles[lang];
    }
    (function() {
        var stored = null;
        try { stored = localStorage.getItem('gbf_landing_lang'); } catch (e) {}
        var urlLang = null;
        try {
            var params = new URLSearchParams(window.location.search);
            urlLang = params.get('lang');
        } catch (e) {}
        var navLang = (navigator.language || navigator.userLanguage || '').toLowerCase();
        var lang = urlLang || stored;
        if (!lang) {
            if (navLang.indexOf('ja') === 0) {
                lang = 'ja';
            } else if (navLang.indexOf('en') === 0) {
                lang = 'en';
            } else {
                lang = 'zh';
            }
        }
        if (['zh', 'en', 'ja'].indexOf(lang) === -1) {
            lang = 'zh';
        }
        setLang(lang);
    })();
    </script>
</body>
</html>`, lanIP, port)
}
