package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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

        fun triggerHook(methodName: String, thisObject: Any? = null, args: List<Any?> = emptyList()): Boolean {
            return hookedMethods[methodName]?.onInvoked(thisObject, args) ?: false
        }
    }

    private class FakeSslErrorHandler {
        var proceedCalled = false
        var cancelCalled = false

        fun proceed() {
            proceedCalled = true
        }

        fun cancel() {
            cancelCalled = true
        }
    }

    private class FakeDName(
        private val oName: String? = null,
        private val cName: String? = null
    ) {
        fun getOName(): String? = oName
        fun getCName(): String? = cName
    }

    private class FakeSslCertificate(
        private val issuedBy: FakeDName? = null,
        private val certString: String = ""
    ) {
        fun getIssuedBy(): FakeDName? = issuedBy
        override fun toString(): String = certString
    }

    private class FakeSslError(
        private val url: String? = null,
        private val cert: FakeSslCertificate? = null
    ) {
        fun getUrl(): String? = url
        fun getCertificate(): FakeSslCertificate? = cert
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

        assertNotNull("onReceivedSslError hook must be registered", mockRegistry.hookedMethods["onReceivedSslError"])
        assertNotNull("setWebViewClient hook must be registered", mockRegistry.hookedMethods["setWebViewClient"])
        assertNotNull("loadUrl hook must be registered", mockRegistry.hookedMethods["loadUrl"])
        assertEquals(3, mockRegistry.hookedMethods.size)
    }

    @Test
    fun testTriggeringHookAppliesProxyConfiguration() {
        adapter.installWebViewHooks(mockRegistry)
        assertEquals(0, mockExecutor.callCount)

        // Simulate WebView triggering setWebViewClient
        mockRegistry.triggerHook("setWebViewClient", thisObject = Any(), args = listOf(Any()))

        assertEquals("Proxy configuration should be triggered on first WebView interaction", 1, mockExecutor.callCount)
        assertTrue(configurator.isReady)

        // Simulate secondary trigger from loadUrl
        mockRegistry.triggerHook("loadUrl", thisObject = Any(), args = listOf("https://game.granbluefantasy.jp"))

        // Must remain idempotent (still 1)
        assertEquals("Subsequent WebView interactions must not re-trigger configuration", 1, mockExecutor.callCount)
    }

    @Test
    fun testSslErrorAutoProceedsForLocalCaCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val localCaCert = FakeSslCertificate(
            issuedBy = FakeDName(oName = "GBF Local Accelerator", cName = "GBF Local Accelerator Root CA"),
            certString = "GBF Local Accelerator Root CA"
        )
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", localCaCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertTrue("Cert issued by GBF Local Accelerator Root CA must be approved", intercepted)
        assertTrue("handler.proceed() must be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorAutoProceedsForLocalCaCertOnAkamai() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val localCaCert = FakeSslCertificate(
            issuedBy = FakeDName(oName = "GBF Local Accelerator", cName = "GBF Local Accelerator Root CA"),
            certString = "GBF Local Accelerator Root CA"
        )
        val error = FakeSslError("https://prd-game-a1-gbf.granbluefantasy.akamaized.net/assets/app.js", localCaCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertTrue("Akamai static assets signed by local CA must be auto-approved", intercepted)
        assertTrue("handler.proceed() must be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorEmptyUrlWithUnknownCertRejected() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val unknownCert = FakeSslCertificate(
            issuedBy = FakeDName(oName = "Untrusted Authority", cName = "Rogue Root CA"),
            certString = "Rogue Root CA"
        )
        val error = FakeSslError("", unknownCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Empty URL with unknown cert must NOT be auto-approved (Fail-Closed)", intercepted)
        assertFalse("handler.proceed() must NOT be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorGbfDomainWithUnknownCertRejected() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val rogueCert = FakeSslCertificate(
            issuedBy = FakeDName(oName = "Public Rogue CA", cName = "Fake Granblue CA"),
            certString = "Fake Granblue CA"
        )
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", rogueCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("GBF domain with unknown external cert must NOT be approved (anti-MITM)", intercepted)
        assertFalse("handler.proceed() must NOT be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorNotInterceptedForNonGbfDomains() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val error = FakeSslError("https://example.com/untrusted", null)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Non-GBF domains must not be auto-approved", intercepted)
        assertFalse("handler.proceed() must not be called", handler.proceedCalled)
    }
}
