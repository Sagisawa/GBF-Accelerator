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
            "*granbluefantasy.akamaized.net",
            "*granbluefantasy-steam.akamaized.net",
            "*gbf.akamaized.net"
        )
        assertEquals("Canonical bypass rules must match exactly", expectedRules, GbfRoutingRules.GBF_BYPASS_RULES)
        assertEquals("Must contain exactly 5 canonical rules", 5, GbfRoutingRules.GBF_BYPASS_RULES.size)
    }

    @Test
    fun testExplicitCanonicalRulesCoverage() {
        // 1. *.granbluefantasy.jp
        assertTrue("game.granbluefantasy.jp must enter proxy", GbfRoutingRules.shouldProxy("game.granbluefantasy.jp"))
        assertTrue("assets.granbluefantasy.jp must enter proxy", GbfRoutingRules.shouldProxy("assets.granbluefantasy.jp"))
        assertTrue("img.granbluefantasy.jp must enter proxy", GbfRoutingRules.shouldProxy("https://img.granbluefantasy.jp/rest/path"))

        // 2. granbluefantasy.jp
        assertTrue("granbluefantasy.jp must enter proxy", GbfRoutingRules.shouldProxy("granbluefantasy.jp"))
        assertTrue("https://granbluefantasy.jp/ must enter proxy", GbfRoutingRules.shouldProxy("https://granbluefantasy.jp/"))

        // 3. *granbluefantasy.akamaized.net (real Akamai CDN shards use hyphens, e.g. prd-game-a)
        assertTrue("prd-game-a-granbluefantasy.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a-granbluefantasy.akamaized.net"))
        assertTrue("prd-game-a1-granbluefantasy.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a1-granbluefantasy.akamaized.net"))
        assertTrue("prd-game-a5-granbluefantasy.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a5-granbluefantasy.akamaized.net"))
        assertTrue("prd-game-a1-gbf.granbluefantasy.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a1-gbf.granbluefantasy.akamaized.net"))

        // 4. *granbluefantasy-steam.akamaized.net
        assertTrue("prd-game-a-granbluefantasy-steam.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a-granbluefantasy-steam.akamaized.net"))
        assertTrue("prd-game-a-gbf.granbluefantasy-steam.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a-gbf.granbluefantasy-steam.akamaized.net"))

        // 5. *gbf.akamaized.net
        assertTrue("prd-game-a-gbf.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a-gbf.akamaized.net"))
        assertTrue("prd-game-a.gbf.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("prd-game-a.gbf.akamaized.net"))
        assertTrue("sub.gbf.akamaized.net must enter proxy", GbfRoutingRules.shouldProxy("sub.gbf.akamaized.net"))
    }

    @Test
    fun testExplicitExcludedMobageDomainsDirect() {
        // Explicit non-proxy verification for sensitive Mobage / payment / authentication / legacy shard domains
        val excludedMobageDomains = listOf(
            "gbf.game.mbga.jp",
            "connect.mobage.jp",
            "sp.mbga.jp",
            "gbf.game-a.mbga.jp",
            "gbf.game-a1.mbga.jp",
            "gbf.game-a2.mbga.jp",
            "gbf.game-a.sp.mbga.jp",
            "gbf.game-a1.sp.mbga.jp",
            "mobage.jp",
            "mbga.jp",
            "www.mbga.jp",
            "login.mobage.jp"
        )

        for (domain in excludedMobageDomains) {
            assertFalse(
                "Mobage domain $domain must NOT enter proxy (must remain DIRECT)",
                GbfRoutingRules.shouldProxy(domain)
            )
            assertFalse(
                "URL with domain $domain must NOT enter proxy",
                GbfRoutingRules.shouldProxy("https://$domain/index.html")
            )
        }
    }

    @Test
    fun testNonGbfDomainsRemainDirect() {
        val nonGbfDomains = listOf(
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
