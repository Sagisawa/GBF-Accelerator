package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.IOException

class ProxyConfiguratorTest {

    private class MockProxyOverrideExecutor(
        var supported: Boolean = true,
        var reverseBypassSupported: Boolean = true,
        var shouldSucceed: Boolean = true,
        var simulatedException: Throwable? = null
    ) : ProxyOverrideExecutor {

        var applyCallCount: Int = 0
        var lastProxyUrl: String? = null
        var lastBypassRules: List<String>? = null
        var lastReverseBypass: Boolean? = null
        var clearedOverrideCount: Int = 0

        override fun isSupported(): Boolean = supported
        override fun isReverseBypassSupported(): Boolean = reverseBypassSupported

        override fun apply(
            proxyUrl: String,
            bypassRules: List<String>,
            reverseBypass: Boolean,
            onSuccess: () -> Unit,
            onError: (Throwable) -> Unit
        ) {
            applyCallCount++
            lastProxyUrl = proxyUrl
            lastBypassRules = bypassRules
            lastReverseBypass = reverseBypass

            if (!isSupported()) {
                onError(UnsupportedOperationException("PROXY_OVERRIDE is not supported"))
                return
            }

            if (reverseBypass && !isReverseBypassSupported()) {
                onError(UnsupportedOperationException("PROXY_OVERRIDE_REVERSE_BYPASS is not supported"))
                return
            }

            if (simulatedException != null) {
                onError(simulatedException!!)
                return
            }

            if (shouldSucceed) {
                onSuccess()
            } else {
                onError(IOException("Simulated WebKit ProxyController failure"))
            }
        }

        fun clearProxyOverride() {
            clearedOverrideCount++
        }
    }

    private lateinit var mockExecutor: MockProxyOverrideExecutor
    private lateinit var configurator: ProxyConfigurator

    @Before
    fun setUp() {
        mockExecutor = MockProxyOverrideExecutor()
        configurator = ProxyConfigurator(executor = mockExecutor)
    }

    @Test
    fun testSuccessfulProxyConfiguration() {
        var callbackSuccess: Boolean? = null
        var callbackError: Throwable? = null

        configurator.applyProxyConfig { success, error ->
            callbackSuccess = success
            callbackError = error
        }

        assertTrue("applyProxyConfig should report success", callbackSuccess == true)
        assertNull("Error should be null on success", callbackError)
        assertEquals(ProxyState.READY, configurator.currentState)
        assertTrue(configurator.isReady)
        assertFalse(configurator.isFailed)

        // Verify routing arguments passed to WebKit
        assertEquals("http://127.0.0.1:8124", mockExecutor.lastProxyUrl)
        assertEquals(true, mockExecutor.lastReverseBypass)
        assertEquals(GbfRoutingRules.GBF_BYPASS_RULES, mockExecutor.lastBypassRules)
        assertEquals(1, mockExecutor.applyCallCount)
        assertEquals("clearProxyOverride must NOT be called", 0, mockExecutor.clearedOverrideCount)
    }

    @Test
    fun testConfigurationFailureDoesNotSilentlyFallbackToDirect() {
        // Critical security contract: If ProxyController fails, DO NOT call clearProxyOverride()
        // or silently route traffic directly. The proxy must fail-closed to prevent leaks or inconsistency.
        mockExecutor.shouldSucceed = false

        var callbackSuccess: Boolean? = null
        var callbackError: Throwable? = null

        configurator.applyProxyConfig { success, error ->
            callbackSuccess = success
            callbackError = error
        }

        assertFalse("applyProxyConfig should report failure", callbackSuccess == true)
        assertNotNull("Failure error must be propagated", callbackError)
        assertEquals(ProxyState.FAILED, configurator.currentState)
        assertTrue(configurator.isFailed)
        assertFalse(configurator.isReady)
        assertEquals(callbackError, configurator.failureError)

        // Verify Fail-Closed guarantee: clearProxyOverride is never called to fall back to DIRECT
        assertEquals("clearProxyOverride must NEVER be invoked on failure (Fail-Closed)", 0, mockExecutor.clearedOverrideCount)
    }

    @Test
    fun testExceptionDuringConfigurationEnforcesFailClosed() {
        val rootCause = RuntimeException("Fatal native binder error in WebView service")
        mockExecutor.simulatedException = rootCause

        var callbackSuccess: Boolean? = null
        var callbackError: Throwable? = null

        configurator.applyProxyConfig { success, error ->
            callbackSuccess = success
            callbackError = error
        }

        assertFalse("applyProxyConfig must fail when exception occurs", callbackSuccess == true)
        assertEquals(rootCause, callbackError)
        assertEquals(ProxyState.FAILED, configurator.currentState)
        assertTrue(configurator.isFailed)
        assertEquals(0, mockExecutor.clearedOverrideCount)
    }

    @Test
    fun testUnsupportedFeatureEnforcesFailClosed() {
        mockExecutor.supported = false

        var callbackSuccess: Boolean? = null
        var callbackError: Throwable? = null

        configurator.applyProxyConfig { success, error ->
            callbackSuccess = success
            callbackError = error
        }

        assertFalse("applyProxyConfig must fail when PROXY_OVERRIDE is unsupported", callbackSuccess == true)
        assertNotNull(callbackError)
        assertTrue(callbackError is UnsupportedOperationException)
        assertEquals(ProxyState.FAILED, configurator.currentState)
        assertTrue(configurator.isFailed)
        assertEquals(0, mockExecutor.clearedOverrideCount)
    }

    @Test
    fun testUnsupportedReverseBypassEnforcesFailClosed() {
        mockExecutor.reverseBypassSupported = false

        var callbackSuccess: Boolean? = null
        var callbackError: Throwable? = null

        configurator.applyProxyConfig { success, error ->
            callbackSuccess = success
            callbackError = error
        }

        assertFalse("applyProxyConfig must fail when PROXY_OVERRIDE_REVERSE_BYPASS is unsupported", callbackSuccess == true)
        assertNotNull(callbackError)
        assertTrue("Error must be UnsupportedOperationException", callbackError is UnsupportedOperationException)
        assertEquals(ProxyState.FAILED, configurator.currentState)
        assertTrue(configurator.isFailed)
        assertFalse(configurator.isReady)
        // Executor apply was never called (early fail-closed) and no clearProxyOverride was invoked
        assertEquals("apply must not be invoked on unsupported reverse bypass", 0, mockExecutor.applyCallCount)
        assertEquals(0, mockExecutor.clearedOverrideCount)
    }

    @Test
    fun testIdempotentConfigurationCalls() {
        var callCount = 0
        configurator.applyProxyConfig { _, _ -> callCount++ }
        configurator.applyProxyConfig { _, _ -> callCount++ }
        configurator.applyProxyConfig { _, _ -> callCount++ }

        assertEquals("Underlying executor should only be invoked once", 1, mockExecutor.applyCallCount)
        assertEquals("All callbacks should receive completion", 3, callCount)
    }
}
