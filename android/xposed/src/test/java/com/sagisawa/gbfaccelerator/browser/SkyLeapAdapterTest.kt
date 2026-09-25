package com.sagisawa.gbfaccelerator.browser

import com.sagisawa.gbfaccelerator.browser.skyleap.SkyLeapAdapter
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SkyLeapAdapterTest {

    private lateinit var adapter: SkyLeapAdapter

    @Before
    fun setUp() {
        adapter = SkyLeapAdapter()
        BrowserAdapterRegistry.resetToDefaults()
    }

    @Test
    fun testAdapterInitialization() {
        assertEquals("skyleap", adapter.id)
        assertEquals("SkyLeap", adapter.name)
        assertTrue(adapter.targetPackages.contains("com.dena.skyleap"))
        assertFalse(adapter.targetPackages.contains("com.dena.skyleap2"))
        assertEquals(1, adapter.targetPackages.size)
    }

    @Test
    fun testPackageMatching() {
        assertTrue(adapter.matchesPackage("com.dena.skyleap"))
        // Explicitly assert third-party modded package is not accepted
        assertFalse(adapter.matchesPackage("com.dena.skyleap2"))
        assertFalse(adapter.matchesPackage("com.android.chrome"))
        assertFalse(adapter.matchesPackage("org.mozilla.firefox"))
        assertFalse(adapter.matchesPackage("com.google.android.webview"))
        assertFalse(adapter.matchesPackage(""))
    }

    @Test
    fun testProcessMatching() {
        assertTrue(adapter.matchesProcess("com.dena.skyleap"))
        assertTrue(adapter.matchesProcess("com.dena.skyleap:sandboxed_process0"))
        assertFalse(adapter.matchesProcess("com.dena.skyleap2"))
        assertFalse(adapter.matchesProcess("com.dena.skyleap2:privileged_process0"))
        assertFalse(adapter.matchesProcess("com.android.chrome"))
        assertFalse(adapter.matchesProcess("com.other.browser"))
    }

    @Test
    fun testRegistryLookup() {
        val foundByPackage = BrowserAdapterRegistry.findAdapterByPackage("com.dena.skyleap")
        assertNotNull(foundByPackage)
        assertEquals("skyleap", foundByPackage?.id)

        val notFoundThirdParty = BrowserAdapterRegistry.findAdapterByPackage("com.dena.skyleap2")
        assertNull("com.dena.skyleap2 must not be resolved in registry", notFoundThirdParty)

        val foundByProcess = BrowserAdapterRegistry.findAdapterByProcess("com.dena.skyleap:sandboxed_process0")
        assertNotNull(foundByProcess)
        assertEquals("skyleap", foundByProcess?.id)

        val notFound = BrowserAdapterRegistry.findAdapterByPackage("com.unknown.browser")
        assertNull(notFound)
    }

    @Test
    fun testRegistryCustomAdapterRegistration() {
        val customAdapter = object : BrowserAdapter {
            override val id = "mock_browser"
            override val name = "MockBrowser"
            override val targetPackages = setOf("com.mock.browser")
            override fun matchesPackage(packageName: String) = packageName in targetPackages
            override fun matchesProcess(processName: String) = processName.startsWith("com.mock.browser")
            override fun onModuleLoaded(processName: String, hookRegistry: HookRegistry) {}
            override fun onPackageLoaded(packageName: String) {}
            override fun onPackageReady(packageName: String) {}
        }

        BrowserAdapterRegistry.register(customAdapter)
        assertEquals(2, BrowserAdapterRegistry.getAllAdapters().size)
        assertEquals(customAdapter, BrowserAdapterRegistry.findAdapterByPackage("com.mock.browser"))

        BrowserAdapterRegistry.unregister("mock_browser")
        assertEquals(1, BrowserAdapterRegistry.getAllAdapters().size)
        assertNull(BrowserAdapterRegistry.findAdapterByPackage("com.mock.browser"))
    }
}
