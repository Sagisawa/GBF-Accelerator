package com.sagisawa.gbfaccelerator.browser

import com.sagisawa.gbfaccelerator.browser.skyleap.SkyLeapAdapter
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class BrowserAdapterRegistryTest {

    private class CustomTestAdapter(
        override val id: String = "custom_test",
        override val name: String = "Custom Browser",
        override val targetPackages: Set<String> = setOf("com.custom.browser")
    ) : BrowserAdapter {
        override fun matchesPackage(packageName: String): Boolean = packageName in targetPackages
        override fun matchesProcess(processName: String): Boolean = processName in targetPackages
        override fun onModuleLoaded(processName: String, hookRegistry: HookRegistry) {}
        override fun onPackageLoaded(packageName: String) {}
        override fun onPackageReady(packageName: String) {}
    }

    @Before
    fun setUp() {
        BrowserAdapterRegistry.resetToDefaults()
    }

    @After
    fun tearDown() {
        BrowserAdapterRegistry.resetToDefaults()
    }

    @Test
    fun testDefaults_containsSkyLeapAdapterOnly() {
        val adapters = BrowserAdapterRegistry.getAllAdapters()
        assertEquals(1, adapters.size)
        assertTrue(adapters[0] is SkyLeapAdapter)
        assertEquals("skyleap", adapters[0].id)
        assertEquals("SkyLeap", adapters[0].name)
    }

    @Test
    fun testRegister_addsNewAdapterWithoutDuplicates() {
        val custom = CustomTestAdapter()
        BrowserAdapterRegistry.register(custom)

        val adapters = BrowserAdapterRegistry.getAllAdapters()
        assertEquals(2, adapters.size)
        assertEquals(custom, BrowserAdapterRegistry.findAdapterById("custom_test"))

        // Register duplicate id should be ignored
        BrowserAdapterRegistry.register(custom)
        assertEquals(2, BrowserAdapterRegistry.getAllAdapters().size)
    }

    @Test
    fun testUnregister_removesSpecifiedAdapter() {
        val custom = CustomTestAdapter()
        BrowserAdapterRegistry.register(custom)
        assertEquals(2, BrowserAdapterRegistry.getAllAdapters().size)

        BrowserAdapterRegistry.unregister("custom_test")
        assertEquals(1, BrowserAdapterRegistry.getAllAdapters().size)
        assertNull(BrowserAdapterRegistry.findAdapterById("custom_test"))
    }

    @Test
    fun testFindByPackage_matchesCorrectAdapter() {
        val adapter = BrowserAdapterRegistry.findAdapterByPackage("com.dena.skyleap")
        assertNotNull(adapter)
        assertEquals("skyleap", adapter?.id)

        // Skyleap2 should NOT match
        val modded = BrowserAdapterRegistry.findAdapterByPackage("com.dena.skyleap2")
        assertNull(modded)
    }

    @Test
    fun testFindByProcess_matchesCorrectAdapter() {
        val adapter = BrowserAdapterRegistry.findAdapterByProcess("com.dena.skyleap:sandboxed_process0")
        assertNotNull(adapter)
        assertEquals("skyleap", adapter?.id)
    }
}
