package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UniversalBrowserAdapterTest {

    @Test
    fun testDefaultConstructor_matchesWildcard() {
        val adapter = UniversalBrowserAdapter()
        assertEquals(UniversalBrowserAdapter.ID, adapter.id)
        assertTrue(adapter.matchesPackage("com.dena.skyleap"))
        assertTrue(adapter.matchesPackage("com.dena.skyleap.accelerated"))
        assertTrue(adapter.matchesPackage("com.android.chrome"))
        assertTrue(adapter.matchesPackage("com.microsoft.emmx"))

        assertTrue(adapter.matchesProcess("com.dena.skyleap"))
        assertTrue(adapter.matchesProcess("com.dena.skyleap:sandboxed_process0"))
        assertTrue(adapter.matchesProcess("com.android.chrome:privileged_process0"))
    }

    @Test
    fun testPackageSpecificConstructor_matchesSpecificPackageOnly() {
        val adapter = UniversalBrowserAdapter("com.dena.skyleap.accelerated")
        assertEquals("universal_com.dena.skyleap.accelerated", adapter.id)
        assertTrue(adapter.matchesPackage("com.dena.skyleap.accelerated"))
        assertFalse(adapter.matchesPackage("com.dena.skyleap"))
        assertFalse(adapter.matchesPackage("com.android.chrome"))

        assertTrue(adapter.matchesProcess("com.dena.skyleap.accelerated"))
        assertTrue(adapter.matchesProcess("com.dena.skyleap.accelerated:renderer"))
        assertFalse(adapter.matchesProcess("com.dena.skyleap"))
    }
}
