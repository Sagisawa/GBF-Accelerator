package com.sagisawa.gbfaccelerator.patch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class PatchEngineTest {

    @Test
    fun testStubPatchEngine_properties() {
        val engine = StubPatchEngine()
        assertEquals("stub", engine.id)
        assertEquals("Standby Patch Engine", engine.name)
        assertFalse(engine.isAvailable)
    }

    @Test
    fun testStubPatchEngine_returnsFailureWithProgress() {
        val engine = StubPatchEngine()
        val source = BrowserSource.InstalledPackage("com.dena.skyleap", "SkyLeap")
        val outputDir = File(System.getProperty("java.io.tmpdir"), "patch_test_out")

        val progressUpdates = mutableListOf<PatchProgress>()
        var finalResult: PatchResult? = null

        engine.patch(
            source = source,
            outputDirectory = outputDir,
            progressListener = { progressUpdates.add(it) },
            completion = { finalResult = it }
        )

        assertEquals(1, progressUpdates.size)
        assertEquals("Init", progressUpdates[0].step)
        assertEquals(0, progressUpdates[0].progressPercent)

        assertTrue(finalResult is PatchResult.Failure)
        val failure = finalResult as PatchResult.Failure
        assertEquals("ENGINE_NOT_IMPLEMENTED", failure.errorCode)
        assertTrue(failure.message.contains("scheduled for future releases"))
    }

    @Test
    fun testPatchOptions_defaults() {
        val options = PatchOptions()
        assertTrue(options.embedModule)
        assertNull(options.customKeystorePath)
    }

    @Test
    fun testPatchResult_successProperties() {
        val file = File("/tmp/test.apk")
        val success = PatchResult.Success(file, "com.dena.skyleap", "Patched successfully")
        assertEquals(file, success.outputApkFile)
        assertEquals("com.dena.skyleap", success.packageName)
        assertEquals("Patched successfully", success.summary)
    }
}
