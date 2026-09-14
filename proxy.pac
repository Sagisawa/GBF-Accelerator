function FindProxyForURL(url, host) {
    if (
        shExpMatch(host, "*.granbluefantasy.jp") ||
        shExpMatch(host, "granbluefantasy.jp") ||
        shExpMatch(host, "*.granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.akamaized.net") ||
        shExpMatch(host, "*.granbluefantasy.akamaized.net") ||
        shExpMatch(host, "gbf.akamaized.net") ||
        shExpMatch(host, "*.gbf.akamaized.net") ||
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
        shExpMatch(host, "gbf.game.mobage.jp") ||
        shExpMatch(host, "*.mobage.jp") ||
        shExpMatch(host, "mobage.jp") ||
        shExpMatch(host, "rcv.a-i-ad.com") ||
        shExpMatch(host, "*.smbeat.jp") ||
        shExpMatch(host, "*.smrtbeat.com") ||
        shExpMatch(host, "datadoghq-browser-agent") ||
        shExpMatch(host, "*.datadoghq-browser-agent") ||
        shExpMatch(host, "google-analytics.com") ||
        shExpMatch(host, "*.google-analytics.com") ||
        shExpMatch(host, "googletagmanager.com") ||
        shExpMatch(host, "*.googletagmanager.com")
    ) {
        return "PROXY 127.0.0.1:8124; DIRECT";
    }
    return "DIRECT";
}
