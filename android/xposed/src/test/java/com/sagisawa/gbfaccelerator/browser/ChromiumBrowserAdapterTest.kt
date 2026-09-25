package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ChromiumBrowserAdapterTest {

    @Test
    fun testDefaultConstructor_matchesKnownChromiumPackages() {
        val adapter = ChromiumBrowserAdapter()
        assertEquals(ChromiumBrowserAdapter.ID, adapter.id)
        assertTrue(adapter.matchesPackage("com.android.chrome"))
        assertTrue(adapter.matchesPackage("com.kiwibrowser.browser"))
        assertTrue(adapter.matchesPackage("com.microsoft.emmx"))
        assertTrue(adapter.matchesPackage("com.brave.browser"))
        assertFalse(adapter.matchesPackage("com.dena.skyleap"))
        assertFalse(adapter.matchesPackage("mark.via"))

        assertTrue(adapter.matchesProcess("com.android.chrome"))
        assertTrue(adapter.matchesProcess("com.android.chrome:privileged_process0"))
        assertTrue(adapter.matchesProcess("com.kiwibrowser.browser:sandboxed_process1"))
    }

    @Test
    fun testPackageSpecificConstructor_matchesSpecificPackageOnly() {
        val adapter = ChromiumBrowserAdapter("com.kiwibrowser.browser.accelerated")
        assertEquals("chromium_com.kiwibrowser.browser.accelerated", adapter.id)
        assertTrue(adapter.matchesPackage("com.kiwibrowser.browser.accelerated"))
        assertFalse(adapter.matchesPackage("com.android.chrome"))
        assertFalse(adapter.matchesPackage("com.dena.skyleap"))

        assertTrue(adapter.matchesProcess("com.kiwibrowser.browser.accelerated"))
        assertTrue(adapter.matchesProcess("com.kiwibrowser.browser.accelerated:privileged_process0"))
        assertFalse(adapter.matchesProcess("com.android.chrome"))
    }

    @Test
    fun testRegistryFindsChromiumBrowser() {
        BrowserAdapterRegistry.resetToDefaults()
        BrowserAdapterRegistry.register(ChromiumBrowserAdapter())
        val chromeAdapter = BrowserAdapterRegistry.findAdapterByPackage("com.android.chrome")
        assertTrue(chromeAdapter is ChromiumBrowserAdapter)

        val kiwiAdapter = BrowserAdapterRegistry.findAdapterByPackage("com.kiwibrowser.browser")
        assertTrue(kiwiAdapter is ChromiumBrowserAdapter)

        BrowserAdapterRegistry.resetToDefaults()
    }
}
