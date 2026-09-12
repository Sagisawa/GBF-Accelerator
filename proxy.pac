function FindProxyForURL(url, host) {
    // GBF 多人战实时长连接
    if (host === "ws.game.granbluefantasy.jp") {
        return "PROXY 127.0.0.1:8124; DIRECT";
    }

    // GBF 专属 Akamai CDN 静态资源域名 (如 prd-game-a-granbluefantasy.akamaized.net)
    if (host.indexOf("granbluefantasy.akamaized.net") !== -1 || host.indexOf("gbf.akamaized.net") !== -1) {
        return "PROXY 127.0.0.1:8124; DIRECT";
    }

    var gbf_domains = [
        "granbluefantasy.jp",
        "granbluefantasy.com",
        "mbga.jp"
    ];

    for (var i = 0; i < gbf_domains.length; i++) {
        var d = gbf_domains[i];
        if (host === d || (host.length > d.length && host.substr(host.length - d.length - 1) === "." + d)) {
            return "PROXY 127.0.0.1:8124; DIRECT";
        }
    }

    // 其它网站完全不走本地代理，保持直连/正常上网
    return "DIRECT";
}
