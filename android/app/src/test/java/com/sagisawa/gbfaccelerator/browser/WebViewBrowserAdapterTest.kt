package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.lang.reflect.Method

class WebViewBrowserAdapterTest {

    private class MockHookRegistry : HookRegistry {
        val hookedMethods = mutableMapOf<String, HookInvocationCallback>()

        override fun hookMethod(method: Method, callback: HookInvocationCallback): Boolean {
            hookedMethods[method.name] = callback
            return true
        }

        fun triggerHook(methodName: String, thisObject: Any? = null, args: List<Any?> = emptyList()) {
            hookedMethods[methodName]?.onInvoked(thisObject, args)
        }
    }

    private class TestWebViewBrowserAdapter(
        proxyConfigurator: ProxyConfigurator
    ) : WebViewBrowserAdapter(
        id = "test_browser",
        name = "TestBrowser",
        targetPackages = setOf("com.test.browser"),
        proxyConfigurator = proxyConfigurator
    )

    private class MockProxyOverrideExecutor : ProxyOverrideExecutor {
        var callCount = 0
        override fun isSupported() = true
        override fun isReverseBypassSupported() = true
        override fun apply(
            proxyUrl: String,
            bypassRules: List<String>,
            reverseBypass: Boolean,
            onSuccess: () -> Unit,
            onError: (Throwable) -> Unit
        ) {
            callCount++
            onSuccess()
        }
    }

    private lateinit var mockRegistry: MockHookRegistry
    private lateinit var mockExecutor: MockProxyOverrideExecutor
    private lateinit var configurator: ProxyConfigurator
    private lateinit var adapter: TestWebViewBrowserAdapter

    @Before
    fun setUp() {
        mockRegistry = MockHookRegistry()
        mockExecutor = MockProxyOverrideExecutor()
        configurator = ProxyConfigurator(executor = mockExecutor)
        adapter = TestWebViewBrowserAdapter(configurator)
    }

    @Test
    fun testInstallWebViewHooksRegistersExpectedMethods() {
        adapter.installWebViewHooks(mockRegistry)

        assertNotNull("setWebViewClient hook must be registered", mockRegistry.hookedMethods["setWebViewClient"])
        assertNotNull("loadUrl hook must be registered", mockRegistry.hookedMethods["loadUrl"])
        assertEquals(2, mockRegistry.hookedMethods.size)
    }

    @Test
    fun testTriggeringHookAppliesProxyConfiguration() {
        adapter.installWebViewHooks(mockRegistry)
        assertEquals(0, mockExecutor.callCount)

        // Simulate WebView triggering setWebViewClient
        mockRegistry.triggerHook("setWebViewClient", thisObject = Any())

        assertEquals("Proxy configuration should be triggered on first WebView interaction", 1, mockExecutor.callCount)
        assertTrue(configurator.isReady)

        // Simulate secondary trigger from loadUrl
        mockRegistry.triggerHook("loadUrl", thisObject = Any(), args = listOf("https://game.granbluefantasy.jp"))

        // Must remain idempotent (still 1)
        assertEquals("Subsequent WebView interactions must not re-trigger configuration", 1, mockExecutor.callCount)
    }
}
