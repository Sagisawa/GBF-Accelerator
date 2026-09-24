package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class GbfRoutingRulesTest {

    @Test
    fun testBypassRulesCompleteness() {
        val expectedRules = listOf(
            "*.granbluefantasy.jp",
            "granbluefantasy.jp",
            "*.granbluefantasy.akamaized.net",
            "*.granbluefantasy-steam.akamaized.net",
            "*.gbf.akamaized.net",
            "gbf.game.mbga.jp"
        )
        assertEquals("Canonical bypass rules must match exactly", expectedRules, GbfRoutingRules.GBF_BYPASS_RULES)
    }

    @Test
    fun testGbfDomainsEnterProxy() {
        val gbfDomains = listOf(
            "game.granbluefantasy.jp",
            "granbluefantasy.jp",
            "gbf.game.mbga.jp",
            "prd-game-a.gbf.akamaized.net",
            "prd-game-a1-gbf.granbluefantasy.akamaized.net",
            "prd-game-a-gbf.granbluefantasy-steam.akamaized.net",
            "assets.granbluefantasy.jp",
            "img.granbluefantasy.jp",
            "sub.gbf.akamaized.net"
        )

        for (domain in gbfDomains) {
            assertTrue(
                "Domain $domain should enter proxy (shouldProxy == true)",
                GbfRoutingRules.shouldProxy(domain)
            )
        }
    }

    @Test
    fun testNonGbfDomainsRemainDirect() {
        val nonGbfDomains = listOf(
            "connect.mobage.jp",
            "sp.mbga.jp",
            "mobage.jp",
            "mbga.jp",
            "www.mbga.jp",
            "login.mobage.jp",
            "www.google.com",
            "play.google.com",
            "api.twitter.com",
            "cdn.jsdelivr.net",
            "dena.com",
            "example.com"
        )

        for (domain in nonGbfDomains) {
            assertFalse(
                "Non-GBF domain $domain must remain DIRECT (shouldProxy == false)",
                GbfRoutingRules.shouldProxy(domain)
            )
        }
    }

    @Test
    fun testConnectMobageJpStrictlyNotProxied() {
        // Critical security contract: connect.mobage.jp authentication and payment flows must strictly bypass the proxy
        assertFalse(
            "connect.mobage.jp must NOT enter the proxy",
            GbfRoutingRules.shouldProxy("connect.mobage.jp")
        )
        assertFalse(
            "https://connect.mobage.jp/login must NOT enter the proxy",
            GbfRoutingRules.shouldProxy("https://connect.mobage.jp/login")
        )
    }

    @Test
    fun testExtractHostVariations() {
        assertEquals("game.granbluefantasy.jp", GbfRoutingRules.extractHost("https://game.granbluefantasy.jp/"))
        assertEquals("game.granbluefantasy.jp", GbfRoutingRules.extractHost("http://game.granbluefantasy.jp:8080/path?query=1"))
        assertEquals("gbf.game.mbga.jp", GbfRoutingRules.extractHost("GBF.GAME.MBGA.JP"))
        assertEquals("connect.mobage.jp", GbfRoutingRules.extractHost("  connect.mobage.jp  "))
        assertEquals("", GbfRoutingRules.extractHost(""))
    }

    @Test
    fun testProxyUrlDefaults() {
        assertEquals("http://127.0.0.1:8124", GbfRoutingRules.DEFAULT_PROXY_URL)
        assertEquals("http://127.0.0.1:8124", GbfRoutingRules.getProxyUrl())
        assertEquals("http://10.0.0.2:8888", GbfRoutingRules.getProxyUrl("10.0.0.2", 8888))
    }
}
