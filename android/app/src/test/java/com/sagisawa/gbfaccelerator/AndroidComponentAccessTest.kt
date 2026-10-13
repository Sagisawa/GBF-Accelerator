package com.sagisawa.gbfaccelerator

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Static verification of the component access boundary declared in AndroidManifest.xml.
 *
 * Only the launcher activity may be reachable from other apps. The core service and the control
 * receiver are driven exclusively by explicit in-app intents, so they must stay unexported.
 */
class AndroidComponentAccessTest {

    private val androidNs = "http://schemas.android.com/apk/res/android"

    private fun manifestFile(): File {
        val candidates = listOf(
            File("src/main/AndroidManifest.xml"),
            File("app/src/main/AndroidManifest.xml"),
        )
        return candidates.firstOrNull { it.isFile }
            ?: error("AndroidManifest.xml not found from ${File(".").absolutePath}")
    }

    private fun components(tag: String): List<Element> {
        val factory = DocumentBuilderFactory.newInstance().apply { isNamespaceAware = true }
        val doc = factory.newDocumentBuilder().parse(manifestFile())
        val nodes = doc.getElementsByTagName(tag)
        return (0 until nodes.length).map { nodes.item(it) as Element }
    }

    private fun attribute(element: Element, name: String): String? =
        element.getAttributeNS(androidNs, name).ifEmpty { null }

    private fun component(tag: String, name: String): Element {
        val matches = components(tag).filter { attribute(it, "name") == name }
        assertEquals("expected exactly one <$tag> named $name", 1, matches.size)
        return matches.single()
    }

    @Test
    fun coreServiceIsNotExported() {
        val service = component("service", "com.sagisawa.gbfaccelerator.core.CoreService")
        assertEquals(
            "CoreService must not be reachable from other apps",
            "false",
            attribute(service, "exported"),
        )
    }

    @Test
    fun coreControlReceiverIsNotExported() {
        val receiver = component("receiver", "com.sagisawa.gbfaccelerator.core.CoreControlReceiver")
        assertEquals(
            "CoreControlReceiver must not be reachable from other apps",
            "false",
            attribute(receiver, "exported"),
        )
    }

    @Test
    fun launcherActivityStaysExported() {
        val activity = component("activity", "com.sagisawa.gbfaccelerator.ui.MainActivity")
        assertEquals(
            "MainActivity must stay exported so the desktop launcher can open it",
            "true",
            attribute(activity, "exported"),
        )

        val actions = (0 until activity.getElementsByTagName("action").length)
            .map { activity.getElementsByTagName("action").item(it) as Element }
            .mapNotNull { attribute(it, "name") }
        assertTrue(
            "MainActivity must keep the MAIN/LAUNCHER entry point",
            actions.contains("android.intent.action.MAIN"),
        )
    }

    @Test
    fun launcherActivityDeclaresNoControlActionFilter() {
        val activity = component("activity", "com.sagisawa.gbfaccelerator.ui.MainActivity")
        val actions = (0 until activity.getElementsByTagName("action").length)
            .map { activity.getElementsByTagName("action").item(it) as Element }
            .mapNotNull { attribute(it, "name") }
            .toSet()
        assertEquals(
            "MainActivity must stay reachable only as the launcher entry point",
            setOf("android.intent.action.MAIN"),
            actions,
        )
    }

    @Test
    fun onlyTheLauncherActivityIsExported() {
        val exported = listOf("activity", "service", "receiver", "provider").flatMap { tag ->
            components(tag)
                .filter { attribute(it, "exported") == "true" }
                .map { "$tag:${attribute(it, "name")}" }
        }
        assertEquals(
            "only the launcher activity may be exported",
            listOf("activity:com.sagisawa.gbfaccelerator.ui.MainActivity"),
            exported,
        )
    }
}
