package com.sagisawa.gbfaccelerator.patch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class BrowserSourceTest {

    @Test
    fun testInstalledPackage_properties() {
        val source = BrowserSource.InstalledPackage(
            packageName = "com.dena.skyleap",
            label = "SkyLeap"
        )
        assertEquals("com.dena.skyleap", source.packageName)
        val browserSource: BrowserSource = source
        assertTrue(browserSource is BrowserSource.InstalledPackage)
    }

    @Test
    fun testLocalApk_properties() {
        val source = BrowserSource.LocalApk(
            fileUri = "content://media/external/downloads/skyleap.apk",
            fileName = "skyleap.apk",
            fileSize = 18510466L,
            expectedPackageName = "com.dena.skyleap"
        )
        assertEquals("content://media/external/downloads/skyleap.apk", source.fileUri)
        assertEquals("skyleap.apk", source.fileName)
        assertEquals(18510466L, source.fileSize)
        assertEquals("com.dena.skyleap", source.expectedPackageName)
    }

    @Test
    fun testLocalApk_defaultOptionalParameters() {
        val source = BrowserSource.LocalApk(
            fileUri = "file:///sdcard/Download/browser.apk",
            fileName = "browser.apk"
        )
        assertEquals(0L, source.fileSize)
        assertNull(source.expectedPackageName)
    }
}
