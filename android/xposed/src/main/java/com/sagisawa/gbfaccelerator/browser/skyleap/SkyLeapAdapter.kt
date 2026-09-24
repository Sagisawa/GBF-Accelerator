package com.sagisawa.gbfaccelerator.browser.skyleap

import android.util.Log
import com.sagisawa.gbfaccelerator.browser.ProxyConfigurator
import com.sagisawa.gbfaccelerator.browser.WebViewBrowserAdapter

/**
 * Browser adapter for DeNA SkyLeap (official Granblue Fantasy browser).
 */
class SkyLeapAdapter(
    proxyConfigurator: ProxyConfigurator = ProxyConfigurator.DEFAULT
) : WebViewBrowserAdapter(
    id = ID,
    name = NAME,
    targetPackages = TARGET_PACKAGES,
    proxyConfigurator = proxyConfigurator
) {
    companion object {
        const val ID = "skyleap"
        const val NAME = "SkyLeap"
        val TARGET_PACKAGES: Set<String> = setOf(
            "com.dena.skyleap"
        )
    }

    override fun onPackageLoaded(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[GBF-ACC] SkyLeap package loaded: $packageName")
        }
    }

    override fun onPackageReady(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[GBF-ACC] SkyLeap package ready: $packageName")
        }
    }
}
