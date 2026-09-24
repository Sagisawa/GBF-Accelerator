package com.sagisawa.gbfaccelerator.patch

import android.content.Context
import android.content.ContextWrapper
import com.sagisawa.gbfaccelerator.browser.BrowserAdapter
import com.sagisawa.gbfaccelerator.browser.HookRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BrowserLauncherTest {

    private class TestBrowserAdapter(
        override val id: String = "test_browser",
        override val name: String = "Test Browser",
        override val targetPackages: Set<String> = setOf("com.example.browser")
    ) : BrowserAdapter {
        override fun matchesPackage(packageName: String): Boolean = packageName in targetPackages
        override fun matchesProcess(processName: String): Boolean = processName in targetPackages
        override fun onModuleLoaded(processName: String, hookRegistry: HookRegistry) {}
        override fun onPackageLoaded(packageName: String) {}
        override fun onPackageReady(packageName: String) {}
    }

    private class FakeBrowserLauncher(
        private val installedPackages: Set<String>
    ) : BrowserLauncher {
        override fun getAppInfo(context: Context, adapter: BrowserAdapter): BrowserAppInfo {
            val pkg = adapter.targetPackages.firstOrNull { it in installedPackages }
            return if (pkg != null) {
                BrowserAppInfo(
                    packageName = pkg,
                    isInstalled = true,
                    versionName = "1.0.0",
                    versionCode = 100L,
                    label = adapter.name
                )
            } else {
                BrowserAppInfo(
                    packageName = adapter.targetPackages.firstOrNull() ?: "unknown",
                    isInstalled = false
                )
            }
        }

        override fun isInstalled(context: Context, adapter: BrowserAdapter): Boolean {
            return getAppInfo(context, adapter).isInstalled
        }

        override fun launch(context: Context, adapter: BrowserAdapter): LaunchResult {
            val pkg = adapter.targetPackages.firstOrNull { it in installedPackages }
            return if (pkg != null) {
                LaunchResult.Success
            } else {
                LaunchResult.NotInstalled(adapter.targetPackages.firstOrNull() ?: "unknown")
            }
        }
    }

    @Test
    fun testBrowserLauncher_whenInstalled() {
        val launcher = FakeBrowserLauncher(setOf("com.example.browser"))
        val adapter = TestBrowserAdapter()
        val dummyContext = ContextWrapper(null)

        val appInfo = launcher.getAppInfo(dummyContext, adapter)
        assertTrue(appInfo.isInstalled)
        assertEquals("com.example.browser", appInfo.packageName)
        assertEquals("1.0.0", appInfo.versionName)
        assertEquals(100L, appInfo.versionCode)

        val result = launcher.launch(dummyContext, adapter)
        assertTrue(result is LaunchResult.Success)
    }

    @Test
    fun testBrowserLauncher_whenNotInstalled() {
        val launcher = FakeBrowserLauncher(emptySet())
        val adapter = TestBrowserAdapter()
        val dummyContext = ContextWrapper(null)

        val appInfo = launcher.getAppInfo(dummyContext, adapter)
        assertFalse(appInfo.isInstalled)

        val result = launcher.launch(dummyContext, adapter)
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
