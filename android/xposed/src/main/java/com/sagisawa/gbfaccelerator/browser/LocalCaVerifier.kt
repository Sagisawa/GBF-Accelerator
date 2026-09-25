package com.sagisawa.gbfaccelerator.browser

import android.content.Context
import android.util.Log
import java.io.ByteArrayInputStream
import java.io.File
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

/**
 * Cryptographic verifier for ensuring SSL error certificates are authentically
 * issued by the local GBF-Accelerator Root CA.
 */
interface LocalCaVerifier {
    fun isLocalCaCertificate(error: Any?): Boolean
}

/**
 * Default implementation verifying leaf certificates cryptographically against local ca.crt.
 */
class DefaultLocalCaVerifier(
    private val caFileProvider: () -> File?
) : LocalCaVerifier {

    companion object {
        private const val TAG = "GBF-ACC"
        private val certFactory: CertificateFactory by lazy { CertificateFactory.getInstance("X.509") }

        fun fromContext(contextProvider: () -> Context?): DefaultLocalCaVerifier {
            return DefaultLocalCaVerifier {
                contextProvider()?.let { ctx ->
                    File(ctx.filesDir, "gbf_core/certs/ca.crt")
                }
            }
        }
    }

    @Volatile
    private var cachedCaCert: X509Certificate? = null
    @Volatile
    private var cachedFileTimestamp: Long = 0L

    fun getLocalCaCert(): X509Certificate? {
        val file = caFileProvider() ?: return cachedCaCert
        if (!file.exists() || !file.canRead()) return cachedCaCert

        val lastModified = file.lastModified()
        val current = cachedCaCert
        if (current != null && cachedFileTimestamp == lastModified) {
            return current
        }

        return try {
            file.inputStream().use { stream ->
                val cert = certFactory.generateCertificate(stream) as? X509Certificate
                cachedCaCert = cert
                cachedFileTimestamp = lastModified
                cert
            }
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Failed loading local ca.crt: ${e.message}")
            cachedCaCert
        }
    }

    override fun isLocalCaCertificate(error: Any?): Boolean {
        if (error == null) return false

        val leafCert = extractX509Certificate(error) ?: return false
        val caCert = getLocalCaCert() ?: return false

        return verifyLeafAgainstCa(leafCert, caCert)
    }

    internal fun extractX509Certificate(error: Any): X509Certificate? {
        if (error is X509Certificate) {
            return error
        }

        val sslCert = try {
            val getCertMethod = error.javaClass.getMethod("getCertificate")
            getCertMethod.invoke(error)
        } catch (_: Throwable) {
            error
        } ?: return null

        if (sslCert is X509Certificate) {
            return sslCert
        }

        // Method 1: Android API 29+ public getX509Certificate()
        try {
            val getX509 = sslCert.javaClass.getMethod("getX509Certificate")
            val x509 = getX509.invoke(sslCert) as? X509Certificate
            if (x509 != null) return x509
        } catch (_: Throwable) {
        }

        // Method 2: Reflection on private mX509Certificate field (all Android versions)
        try {
            var clazz: Class<*>? = sslCert.javaClass
            while (clazz != null && clazz != Any::class.java) {
                try {
                    val field = clazz.getDeclaredField("mX509Certificate")
                    field.isAccessible = true
                    val x509 = field.get(sslCert) as? X509Certificate
                    if (x509 != null) return x509
                } catch (_: NoSuchFieldException) {
                }
                clazz = clazz.superclass
            }
        } catch (_: Throwable) {
        }

        // Method 3: SslCertificate.saveState reflection
        try {
            val saveStateMethod = sslCert.javaClass.getMethod("saveState", sslCert.javaClass)
            val bundle = saveStateMethod.invoke(null, sslCert)
            val getByteArray = bundle?.javaClass?.getMethod("getByteArray", String::class.java)
            val bytes = getByteArray?.invoke(bundle, "x509-certificate") as? ByteArray
            if (bytes != null) {
                return certFactory.generateCertificate(ByteArrayInputStream(bytes)) as? X509Certificate
            }
        } catch (_: Throwable) {
        }

        return null
    }

    internal fun verifyLeafAgainstCa(leafCert: X509Certificate, caCert: X509Certificate): Boolean {
        // Fast-path: Check issuer name contains our signature organization
        val issuerName = leafCert.issuerX500Principal?.name ?: ""
        if (!issuerName.contains("GBF Local Accelerator")) {
            return false
        }

        // Exact match if the error presented is the root CA itself
        if (leafCert == caCert || leafCert.encoded.contentEquals(caCert.encoded)) {
            return true
        }

        // Cryptographic signature check: Leaf MUST be signed by caCert's private key
        return try {
            leafCert.verify(caCert.publicKey)
            true
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Certificate signature verification failed against local Root CA: ${e.message}")
            false
        }
    }
}
