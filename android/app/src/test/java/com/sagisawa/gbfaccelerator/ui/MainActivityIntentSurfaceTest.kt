package com.sagisawa.gbfaccelerator.ui

import org.junit.Assert.assertFalse
import org.junit.Test
import java.io.File

/**
 * MainActivity is exported for the launcher entry point, so everything it reads out of its Intent
 * is an externally reachable control surface. The activity must therefore not act on Intent extras
 * at all, and must not verify a caller identity with an API newer than minSdk.
 *
 * This is a source level guard: the project has no Robolectric or instrumentation setup, and the
 * property under test is the absence of a code path rather than a runtime behaviour.
 */
class MainActivityIntentSurfaceTest {

    private fun mainActivitySource(): String {
        val candidates = listOf(
            File("src/main/java/com/sagisawa/gbfaccelerator/ui/MainActivity.kt"),
            File("app/src/main/java/com/sagisawa/gbfaccelerator/ui/MainActivity.kt"),
        )
        val file = candidates.firstOrNull { it.isFile }
            ?: error("MainActivity.kt not found from ${File(".").absolutePath}")
        return stripComments(file.readText())
    }

    private fun stripComments(source: String): String = source
        .replace(Regex("/\\*.*?\\*/", RegexOption.DOT_MATCHES_ALL), " ")
        .replace(Regex("//[^\n]*"), " ")

    @Test
    fun activityDoesNotActOnControlExtras() {
        assertFalse(
            "MainActivity must not act on an externally supplied control action extra",
            mainActivitySource().contains("getStringExtra(\"action\")"),
        )
    }

    @Test
    fun activityDoesNotCheckCallerIdentity() {
        val source = mainActivitySource()
        assertFalse(
            "Activity#getLaunchedFromPackage is API 34 while minSdk is 26",
            source.contains("launchedFromPackage"),
        )
    }
}
