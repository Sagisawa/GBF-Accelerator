package com.sagisawa.gbfaccelerator.browser

import java.net.URI
import java.util.Locale

/**
 * GBF-Accelerator canonical routing rules and domain matching contracts.
 * This is the Single Source of Truth (SSOT) for proxy rules across all browser adapters.
 *
 * In Reverse Bypass mode:
 * - Matching domains are routed to Go Core (127.0.0.1:8124) for local caching / transparent forwarding.
 * - Non-matching domains (e.g. connect.mobage.jp) remain DIRECT.
 */
object GbfRoutingRules {

    const val DEFAULT_PROXY_SCHEME = "http"
    const val DEFAULT_PROXY_HOST = "127.0.0.1"
    const val DEFAULT_PROXY_PORT = 8124
    const val DEFAULT_PROXY_URL = "$DEFAULT_PROXY_SCHEME://$DEFAULT_PROXY_HOST:$DEFAULT_PROXY_PORT"

    /**
     * Exact bypass rules configured in AndroidX WebKit ProxyController.
     * With Reverse Bypass enabled, these rules specify which traffic enters the proxy.
     */
    val GBF_BYPASS_RULES: List<String> = listOf(
        "*.granbluefantasy.jp",
        "granbluefantasy.jp",
        "*.granbluefantasy.akamaized.net",
        "*.granbluefantasy-steam.akamaized.net",
        "*.gbf.akamaized.net",
        "gbf.game.mbga.jp"
    )

    /**
     * Extracts the clean lowercase host from a host string or full URL.
     */
    fun extractHost(hostOrUrl: String): String {
        val trimmed = hostOrUrl.trim()
        if (trimmed.isEmpty()) return ""

        val rawHost = if (trimmed.contains("://")) {
            try {
                URI.create(trimmed).host ?: trimmed
            } catch (_: Throwable) {
                trimmed.substringAfter("://").substringBefore("/").substringBefore(":")
            }
        } else {
            trimmed.substringBefore("/").substringBefore(":")
        }

        return rawHost.lowercase(Locale.ROOT)
    }

    /**
     * Determines whether the given host or URL matches any of the canonical GBF proxy rules.
     * Returns true if traffic should enter the proxy, false if it should remain DIRECT.
     */
    fun shouldProxy(hostOrUrl: String): Boolean {
        val host = extractHost(hostOrUrl)
        if (host.isEmpty()) return false

        return GBF_BYPASS_RULES.any { pattern -> matchesPattern(pattern, host) }
    }

    /**
     * Checks if a host matches a single pattern (exact or wildcard).
     */
    fun matchesPattern(pattern: String, host: String): Boolean {
        val cleanPattern = pattern.trim().lowercase(Locale.ROOT)
        val cleanHost = host.trim().lowercase(Locale.ROOT)

        if (cleanPattern.startsWith("*.")) {
            val suffix = cleanPattern.substring(1) // e.g. ".granbluefantasy.jp"
            val domainWithoutWildcard = cleanPattern.substring(2) // e.g. "granbluefantasy.jp"
            return cleanHost.endsWith(suffix) || cleanHost == domainWithoutWildcard
        }

        return cleanHost == cleanPattern
    }

    fun getProxyUrl(host: String = DEFAULT_PROXY_HOST, port: Int = DEFAULT_PROXY_PORT): String {
        return "$DEFAULT_PROXY_SCHEME://$host:$port"
    }
}
