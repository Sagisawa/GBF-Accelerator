package com.sagisawa.gbfaccelerator.patch

import android.content.Context
import android.content.ContextWrapper
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BrowserLauncherTest {

    private class FakeBrowserLauncher(
        private val installedPackages: Set<String>
    ) : BrowserLauncher {
        override fun getAppInfo(context: Context, target: BrowserTarget): BrowserAppInfo {
            val pkg = target.targetPackages.firstOrNull { it in installedPackages }
            return if (pkg != null) {
                BrowserAppInfo(
                    packageName = pkg,
                    isInstalled = true,
                    versionName = "1.0.0",
                    versionCode = 100L,
                    label = target.name
                )
            } else {
                BrowserAppInfo(
                    packageName = target.targetPackages.firstOrNull() ?: "unknown",
                    isInstalled = false
                )
            }
        }

        override fun isInstalled(context: Context, target: BrowserTarget): Boolean {
            return getAppInfo(context, target).isInstalled
        }

        override fun launch(context: Context, target: BrowserTarget): LaunchResult {
            val pkg = target.targetPackages.firstOrNull { it in installedPackages }
            return if (pkg != null) {
                LaunchResult.Success
            } else {
                LaunchResult.NotInstalled(target.targetPackages.firstOrNull() ?: "unknown")
            }
        }
    }

    @Test
    fun testBrowserLauncher_whenInstalled() {
        val launcher = FakeBrowserLauncher(setOf("com.example.browser"))
        val target = BrowserTarget(id = "test", name = "Test Browser", targetPackages = setOf("com.example.browser"))
        val dummyContext = ContextWrapper(null)

        val appInfo = launcher.getAppInfo(dummyContext, target)
        assertTrue(appInfo.isInstalled)
        assertEquals("com.example.browser", appInfo.packageName)
        assertEquals("1.0.0", appInfo.versionName)
        assertEquals(100L, appInfo.versionCode)

        val result = launcher.launch(dummyContext, target)
        assertTrue(result is LaunchResult.Success)
    }

    @Test
    fun testBrowserLauncher_whenNotInstalled() {
        val launcher = FakeBrowserLauncher(emptySet())
        val target = BrowserTarget(id = "test", name = "Test Browser", targetPackages = setOf("com.example.browser"))
        val dummyContext = ContextWrapper(null)

        val appInfo = launcher.getAppInfo(dummyContext, target)
        assertFalse(appInfo.isInstalled)

        val result = launcher.launch(dummyContext, target)
        assertTrue(result is LaunchResult.NotInstalled)
        assertEquals("com.example.browser", (result as LaunchResult.NotInstalled).packageName)
    }

    @Test
    fun testLaunchResult_failureVariant() {
        val ex = RuntimeException("Activity not found")
        val failure = LaunchResult.Failure("Intent failed", ex)
        assertEquals("Intent failed", failure.reason)
        assertEquals(ex, failure.cause)
    }
}
