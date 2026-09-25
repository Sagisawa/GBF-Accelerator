package com.sagisawa.gbfaccelerator.browser

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.io.ByteArrayInputStream
import java.lang.reflect.Method
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

class WebViewBrowserAdapterTest {

    companion object {
        private val certFactory: CertificateFactory by lazy { CertificateFactory.getInstance("X.509") }

        private const val REAL_CA_PEM = """-----BEGIN CERTIFICATE-----
MIIDTTCCAjWgAwIBAgIBATANBgkqhkiG9w0BAQsFADBIMR4wHAYDVQQKExVHQkYg
TG9jYWwgQWNjZWxlcmF0b3IxJjAkBgNVBAMTHUdCRiBMb2NhbCBBY2NlbGVyYXRv
ciBSb290IENBMB4XDTI2MDkyNDE3MTMxN1oXDTM2MDkyMjE3MTMxN1owSDEeMBwG
A1UEChMVR0JGIExvY2FsIEFjY2VsZXJhdG9yMSYwJAYDVQQDEx1HQkYgTG9jYWwg
QWNjZWxlcmF0b3IgUm9vdCBDQTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoC
ggEBAKj5DbN6QoV9FDPMadOoedhBKJSbmWAd0sdk960zKXavPFFahsYSNwKcheJM
4b7SEWboYn7rDBY4vGNtO6AlLFtaqQHdpqURdOYF2HtiO2mMkUwfsb71uOfXjlhH
NwaNmzLELcEtNhI7RvEAC2Ih+GnItnpARsNViYVlAD5IYRTdNE+iyedKtGdhoXUb
4GQGmCpemegFZhzJ5Pq+Q0obW7BWFhst3LOStNijySkdb1Sq1RTNITJUcE5He0jH
iZbKfXUo1zCRP96TSfqausytQ3A3NTKTBhV5VdmVjeGPEJo8udksZPPn/UfOE654
KQ0eeoC1MKWNwp9S7CkLZumHoZECAwEAAaNCMEAwDgYDVR0PAQH/BAQDAgKEMA8G
A1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFJMfwswl5kDZ2CsgMEW1hgpUOLIXMA0G
CSqGSIb3DQEBCwUAA4IBAQBkorTkc2OEkurLnRprbJY1oHsMy6hg2ihfTFHCMe9X
jLczaqWuTNtc5rhgWsg4JfIcACUaYVVDcn4w5MiaN0p885CRwtV5zI4lozmn62DB
Kio9YCuJyRXeTY5cbyOy2Mbwe29n/ZzwRecXZ4fq4tfTyJY3pQfpnJSTNfMYT2O8
iw/cZ7Wwgumcc7d7kA1o1A3aMqmdI6rC0tj+n3DQAwCpZF1SqCZzNZ9nUph2q1Oe
MQvvncnsuXltAypmsfepANak5WNS3HVR40SNUz6VKxp31j4cPx5a4QBgIf0B/MJl
Oa2OOmqgM9GacRVDP/CVg2j3fVKGSWIk/YiEMUFaTxM1
-----END CERTIFICATE-----"""

        private const val REAL_LEAF_PEM = """-----BEGIN CERTIFICATE-----
MIIDTTCCAjWgAwIBAgIBAjANBgkqhkiG9w0BAQsFADBIMR4wHAYDVQQKExVHQkYg
TG9jYWwgQWNjZWxlcmF0b3IxJjAkBgNVBAMTHUdCRiBMb2NhbCBBY2NlbGVyYXRv
ciBSb290IENBMB4XDTI2MDkyNDE3MTMxN1oXDTI3MDkyNTE3MTMxN1owQjEeMBwG
A1UEChMVR0JGIExvY2FsIEFjY2VsZXJhdG9yMSAwHgYDVQQDExdnYW1lLmdyYW5i
bHVlZmFudGFzeS5qcDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAMo8
L8rrFW/VSQDl1uvcSHVl+/ctrJzp2zOMskgvHiPo/JinxdSuOcPz7bMv/IJQcQsd
PxK9uQ4SEvftxLw7v1vekBXQw3mtf7QNXndgIu5LjYj8crK3ICGrrLTzdddyROlU
RZC6/YSzp1JyTC9+bN/NGtIM0wSUyNfDGzSXRnLf2VacM83aBeb+3cPz8oPSMkEy
8n1O0h9L1ki6le8qLv/mb9eabKJSnp2kjbn73wBGN/PPWai+qO6NBAzgU86Q9Bd6
ftRPlbhiSg/Pr6bKdDtAA+cjD2GrwD2Dgho1NxR5ZlRVFKP41Jsb8nHZ4XQqGU2F
ePt2neVb09EAETdk4kECAwEAAaNIMEYwDgYDVR0PAQH/BAQDAgeAMBMGA1UdJQQM
MAoGCCsGAQUFBwMBMB8GA1UdIwQYMBaAFJMfwswl5kDZ2CsgMEW1hgpUOLIXMA0G
CSqGSIb3DQEBCwUAA4IBAQBrEKJE3ow37KQzv9aAS9hXxkejCaCbQjjUMoL/p3g0
ZE39lN6+uMiQlNMxNJoH2BBq5N7zsZwhTog0dFy/y8Vsq83oQI55Z8uN5pZaI59U
wF5FUQLy4XnfE0lt4SzkaAuwPWIO310TPCN4KQWXOD/5nTQ87eInUqroIQtcCY2C
RKojETbRDuh8CEMkieaKIF7BVZr+C1y0y5+Ib+lv2f9MclutGuOOYYUnPefu+rak
xYH7j8tzGSUE55SW/CIkhXCTa+bBeXqOzam3DiM5e8UfIdkmWh/+GpSF/U+JGHGd
ryvQduRvLrJ03EyrAIpPBezb7Nm/FzBe9F7KGTOpvXjw
-----END CERTIFICATE-----"""

        private const val FAKE_LEAF_PEM = """-----BEGIN CERTIFICATE-----
MIIDTTCCAjWgAwIBAgIBAjANBgkqhkiG9w0BAQsFADBIMR4wHAYDVQQKExVHQkYg
TG9jYWwgQWNjZWxlcmF0b3IxJjAkBgNVBAMTHUdCRiBMb2NhbCBBY2NlbGVyYXRv
ciBSb290IENBMB4XDTI2MDkyNDE3MTMxN1oXDTI3MDkyNTE3MTMxN1owQjEeMBwG
A1UEChMVR0JGIExvY2FsIEFjY2VsZXJhdG9yMSAwHgYDVQQDExdnYW1lLmdyYW5i
bHVlZmFudGFzeS5qcDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBANDn
EDJo4E2Wb3wtib9/v6BNJVMYsMsdSsX1ZG5JUpJC+cC3ahNQatfseOtIjpj1fa2U
QH1Z9Rsus+KW7t+uONlARND2qWxP7GFe+GHo+vGmhx0EeX2YAGhXIQ7HBgPxfDdX
qUGGvd2TpLFekMAmUsEAZormEjTP5hjdGpU1/fBR0hgacDQ3QOPa8S2TmMJO/c1H
gcS7G0qVCRhOl5rN4j0+QMb1hBbsvmvHrDGJHlURzKV7hU8Fkd0Muhy3tpjEGmL+
/khr0LpnQE6eL83jvEPeK6svdL3GdpOnHXQEsV1SoN1cJIopfI8SpvgBDQuLeFkl
w6zScegBfHMOsdN0dmECAwEAAaNIMEYwDgYDVR0PAQH/BAQDAgeAMBMGA1UdJQQM
MAoGCCsGAQUFBwMBMB8GA1UdIwQYMBaAFGHE+dQySzhXSfe44QLSTx/9bSfTMA0G
CSqGSIb3DQEBCwUAA4IBAQBf3RfLTxeha99F1Eo7eG3pyq/ffqDuz5K/ITsVy79+
8SBps6VUvJ64+YKMlrTFpCnTIqeuq6B1z5p0UUPTm/eTI4mnRL6kOj1flUxcnROD
SgGFKq42RjRRpwcO5ldTzEc0YezDqNbIMIyYsbJ0xw6dvekjD4TVE202YR4msSqd
Gk+fGtE5q0PZmiQu2R4BmkQKhAK6eD8lS9PVobchNvyRlNTZL+PffbjJbT110NJ3
GeyTd0Gg5Ng4R4poSbZsewh4nrLcVJuXwH6HdRWhgXiWNPa9N6RgMw3ZoJhz6jax
aw0AL/PEnGKw0kXKYdHa7+9z1NIJgcXrSYa2crt4ot9p
-----END CERTIFICATE-----"""

        private const val UNK_LEAF_PEM = """-----BEGIN CERTIFICATE-----
MIIDJTCCAg2gAwIBAgIBAjANBgkqhkiG9w0BAQsFADAzMRcwFQYDVQQKEw5VbnRy
dXN0ZWQgQ29ycDEYMBYGA1UEAxMPVW5rbm93biBSb290IENBMB4XDTI2MDkyNDE3
MTMxN1oXDTI3MDkyNTE3MTMxN1owLzEXMBUGA1UEChMOVW50cnVzdGVkIENvcnAx
FDASBgNVBAMTC2V4YW1wbGUuY29tMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIB
CgKCAQEArg+0uLxyJyQuKj+WfrCJ9n0x6n9WPORuuSYgpkNhq88q+6GOFtEqxXNs
zqWZYOtR89LNIswpoTAw1mSZwv7HJkCalwqan61PUt2YNqtIZw0r+FiBKz8dRMUN
VZe1gCaEFclubZpTav8lRC15PvgMfVJ+rENBdmJeRj5HCc9V7UdcrzcLOlTAPfup
W0Ve7+SazsX2O8yG9zjEhNVrFaxAFg9eLiy6jtX4heEDq4gZp0MFhcFlr9WgahAe
s+VePb+mFjoD/isX2CSkh53AOITIAqWO8Was02We6au4V0O4Ou5HGt2WecsHECLG
lIL0Yjhs3R0WvvMadHl8rWReyDEGAQIDAQABo0gwRjAOBgNVHQ8BAf8EBAMCB4Aw
EwYDVR0lBAwwCgYIKwYBBQUHAwEwHwYDVR0jBBgwFoAUZPQs+1alTUhVhnaHrEJR
uAZaVE8wDQYJKoZIhvcNAQELBQADggEBAGdgFVHg29SfMvSWPdrNWArbllxyco3S
vz3zirVcexEnOS1fnjPh5JVJgsSN5wX++yxHx8vklsLnHAxsDh3W8INipd/xPJf9
fbgKvUzUqOmKwvzGmg+5xmXN2MRRoD274QnEjVA9iIHLOfD+ZZJh8N+QTA0jG6kC
Xh8iEZg9AS5fXY5ZcHUKMyY3sQC/2Op24Pmu9jTSo2IeBMijHfFDpu9fUI3cRQxG
NzQVHGEJptvSIbO7wQ+5F21H0C52R+n2ZRz5MoAQAEWg2q9QSNzuxXdz5gWFTM/5
L3kXclxwCWNhwcpBzPmsVo0xEUx47Vmqa80t8+LyG+ueA8A/XytDi7s=
-----END CERTIFICATE-----"""

        fun parseCert(pem: String): X509Certificate {
            return certFactory.generateCertificate(ByteArrayInputStream(pem.toByteArray())) as X509Certificate
        }
    }

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

    private class FakeSslError(
        private val url: String? = null,
        private val cert: Any? = null
    ) {
        fun getUrl(): String? = url
        fun getCertificate(): Any? = cert
    }

    private class TestWebViewBrowserAdapter(
        proxyConfigurator: ProxyConfigurator,
        caVerifier: LocalCaVerifier
    ) : WebViewBrowserAdapter(
        id = "test_browser",
        name = "TestBrowser",
        targetPackages = setOf("com.test.browser"),
        proxyConfigurator = proxyConfigurator,
        caVerifier = caVerifier
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
    private lateinit var caVerifier: DefaultLocalCaVerifier
    private lateinit var adapter: TestWebViewBrowserAdapter

    private val realCaCert: X509Certificate by lazy { parseCert(REAL_CA_PEM) }
    private val realLeafCert: X509Certificate by lazy { parseCert(REAL_LEAF_PEM) }
    private val fakeLeafCert: X509Certificate by lazy { parseCert(FAKE_LEAF_PEM) }
    private val unkLeafCert: X509Certificate by lazy { parseCert(UNK_LEAF_PEM) }

    @Before
    fun setUp() {
        mockRegistry = MockHookRegistry()
        mockExecutor = MockProxyOverrideExecutor()
        configurator = ProxyConfigurator(executor = mockExecutor)
        caVerifier = DefaultLocalCaVerifier { null }
        // Inject real CA cert into verifier
        val cachedField = DefaultLocalCaVerifier::class.java.getDeclaredField("cachedCaCert")
        cachedField.isAccessible = true
        cachedField.set(caVerifier, realCaCert)

        adapter = TestWebViewBrowserAdapter(configurator, caVerifier)
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
    fun testSslErrorAutoProceedsForRealLocalCaCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", realLeafCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertTrue("Cert genuinely issued by local Root CA must be approved", intercepted)
        assertTrue("handler.proceed() must be called", handler.proceedCalled)
        assertFalse("handler.cancel() must not be called", handler.cancelCalled)
    }

    @Test
    fun testSslErrorRejectsSameNameFakeCaCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        // fakeLeafCert has the exact same Subject & Issuer names as real CA, but a different private key
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", fakeLeafCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Cert with matching name but unauthorized key signature MUST be rejected", intercepted)
        assertFalse("handler.proceed() must NOT be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorRejectsUnknownCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", unkLeafCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Completely unknown certificate must be rejected", intercepted)
        assertFalse("handler.proceed() must NOT be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorRejectsEmptyUrlWithFakeOrUnknownCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val error = FakeSslError("", unkLeafCert)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Empty URL with unknown cert must NOT be auto-approved (Fail-Closed)", intercepted)
        assertFalse("handler.proceed() must NOT be called", handler.proceedCalled)
    }

    @Test
    fun testSslErrorNotInterceptedForNullCert() {
        adapter.installWebViewHooks(mockRegistry)

        val handler = FakeSslErrorHandler()
        val error = FakeSslError("https://game.granbluefantasy.jp/#mypage", null)

        val intercepted = adapter.handleSslError(listOf(null, handler, error))

        assertFalse("Null cert must not be auto-approved", intercepted)
        assertFalse("handler.proceed() must not be called", handler.proceedCalled)
    }
}
