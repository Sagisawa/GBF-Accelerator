package com.sagisawa.gbfaccelerator.browser

/**
 * Universal browser adapter designed for all Android browsers built on standard Android WebView.
 * Dynamically supports official SkyLeap, cloned SkyLeap packages (e.g. com.dena.skyleap.accelerated),
 * and standard system WebView browsers.
 */
class UniversalBrowserAdapter(
    override val id: String = ID,
    override val name: String = NAME,
    override val targetPackages: Set<String> = TARGET_PACKAGES,
    proxyConfigurator: ProxyConfigurator = ProxyConfigurator.DEFAULT
) : WebViewBrowserAdapter(
    id = id,
    name = name,
    targetPackages = targetPackages,
    proxyConfigurator = proxyConfigurator
) {
    companion object {
        const val ID = "universal_webview"
        const val NAME = "Universal WebView Browser"
        val TARGET_PACKAGES: Set<String> = setOf("*")
    }

    constructor(packageName: String) : this(
        id = "universal_$packageName",
        name = "Browser ($packageName)",
        targetPackages = setOf(packageName)
    )
}
