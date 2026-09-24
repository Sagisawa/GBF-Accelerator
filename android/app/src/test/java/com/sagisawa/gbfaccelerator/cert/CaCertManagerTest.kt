package com.sagisawa.gbfaccelerator.cert

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.File

class CaCertManagerTest {

    private val sampleCertPem = """
        -----BEGIN CERTIFICATE-----
        MIIDYDCCAkigAwIBAgIUevpUbg1QLXZ9VB5Oz3kB1aWl3VYwDQYJKoZIhvcNAQEL
        BQAwSDEmMCQGA1UEAwwdR0JGIExvY2FsIEFjY2VsZXJhdG9yIFJvb3QgQ0ExHjAc
        BgNVBAoMFUdCRiBMb2NhbCBBY2NlbGVyYXRvcjAeFw0yNjA5MTExMjQ4NDZaFw0z
        NjA5MDkxMjQ4NDZaMEgxJjAkBgNVBAMMHUdCRiBMb2NhbCBBY2NlbGVyYXRvciBS
        b290IENBMR4wHAYDVQQKDBVHQkYgTG9jYWwgQWNjZWxlcmF0b3IwggEiMA0GCSqG
        SIb3DQEBAQUAA4IBDwAwggEKAoIBAQCjazggItoN2Ui9liEHRFoX2+yoYzkdVLYf
        wI4D4zOBcplTzQYJOWHu2GCynGRlh/ruW5Z33ybuOEclwGV1ayeyb8YWRnYuSnzc
        q/Twb30HmxXhqrngmltWsx0Ue3lT4/026CHrVGUw9715ZNf1e2xIpkCFwF08UZPS
        Al4849SKOxEANQCdVQbuSXJq6+QOdtZFq2+bDpFSKxLmjZQO+lAvZAuW9pOKKZ6n
        b9JzFdFJFX73nRdhnDC/t3r5MZHAeJxY0C8e1kZfn+sruL97XFKNHIZNDO+Pe1ba
        R5IqrSYcljD+fjwdEU8YFGK4Ljr/FtMbSRzPrjGivar1Fal09oNtAgMBAAGjQjBA
        MA8GA1UdEwEB/wQFMAMBAf8wDgYDVR0PAQH/BAQDAgGGMB0GA1UdDgQWBBTetN4a
        sMw81oz7rx9pfACgklccNTANBgkqhkiG9w0BAQsFAAOCAQEAJbL9YEfcD3oZeaKw
        nfIymkYuZbMRy74ixj/R6bc82FHNCQxLL1z++XixhT49Q/3k9Ni6jUxKqsqteSBZ
        9f3paeoe4PPnloPv8ueuWR3Sos1JziKArRD3sR35jpTm/Uv79KIWIfC1RCj3ttiC
        0lqsUTKb6BfN46rWvGSx6o6Rcn4iE+oJ8Yazjpt8OuT11L4gFbpgxYRmY7Z9q6Hv
        +QcDn618+b9np+wmTGGV2cUCcP+/2e0c2LXy1MrcQyD75UPjNyea79ic6eAUYkLe
        x6IOy70tdbCbE2Cdpi08cOfddT1Msnw2vN12C5HE2J0Iu6JRO1gTPYgBz8WV3+HL
        KuzUnQ==
        -----END CERTIFICATE-----
    """.trimIndent()

    @Test
    fun testParseCertificate_validPem() {
        val stream = ByteArrayInputStream(sampleCertPem.toByteArray(Charsets.UTF_8))
        val cert = CaCertManager.parseCertificate(stream)
        assertTrue(cert.subjectX500Principal.name.contains("GBF Local Accelerator Root CA"))
        assertTrue(cert.issuerX500Principal.name.contains("GBF Local Accelerator"))
    }

    @Test
    fun testCalculateSha256Fingerprint() {
        val stream = ByteArrayInputStream(sampleCertPem.toByteArray(Charsets.UTF_8))
        val cert = CaCertManager.parseCertificate(stream)
        val fp = CaCertManager.calculateSha256Fingerprint(cert)
        // Format should be 32 colon-separated hex bytes (e.g. AA:BB:...:ZZ)
        val regex = Regex("^([0-9A-F]{2}:){31}[0-9A-F]{2}$")
        assertTrue("Fingerprint $fp must match format", regex.matches(fp))
    }

    @Test
    fun testGetCertInfoFromFile_nonExistent() {
        val file = File(System.getProperty("java.io.tmpdir"), "non_existent_ca_${System.currentTimeMillis()}.crt")
        val info = CaCertManager.getCertInfoFromFile(file)
        assertFalse(info.exists)
        assertEquals("", info.subject)
    }

    @Test
    fun testGetCertInfoFromFile_validFile() {
        val tempFile = File.createTempFile("ca_test_", ".crt")
        try {
            tempFile.writeText(sampleCertPem)
            val info = CaCertManager.getCertInfoFromFile(tempFile)
            assertTrue(info.exists)
            assertTrue(info.subject.contains("GBF Local Accelerator Root CA"))
            assertTrue(info.issuer.contains("GBF Local Accelerator"))
            assertTrue(info.sha256Fingerprint.isNotEmpty())
            assertFalse(info.isExpired)
            assertTrue(info.fileSizeBytes > 0)
        } finally {
            tempFile.delete()
        }
    }
}
