package io.github.pwnedbygary.scterm.viewer

import io.github.pwnedbygary.scterm.R
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

/**
 * Holds the viewer's action bar to the action catalog every frontend shares
 * (protocol/fixtures/actions.json), reading the bar's strings from
 * res/values/strings.xml, so no Android runtime is needed.
 */
class ViewerBarTest {
    private val catalog: JsonObject = run {
        val dir = System.getProperty("scterm.fixtures") ?: error("scterm.fixtures system property not set")
        Json.parseToJsonElement(File(dir, "actions.json").readText()).jsonObject
    }

    private fun actions(group: String): Map<String, JsonObject> =
        catalog.getValue(group).jsonArray.map { it.jsonObject }.associateBy { it.field("id") }

    private val strings: Map<String, String> = run {
        val file = System.getProperty("scterm.strings") ?: error("scterm.strings system property not set")
        val nodes = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(File(file)).getElementsByTagName("string")
        (0 until nodes.length).map { nodes.item(it) as Element }.associate { it.getAttribute("name") to compiled(it.textContent) }
    }

    /** The bar names its strings by R id; the generated R class maps them back to resource names. */
    private val names: Map<Int, String> = R.string::class.java.fields.let { fields ->
        fields.associate { it.getInt(null) to it.name }.also { check(it.size == fields.size) { "R.string ids are not distinct" } }
    }

    private fun string(id: Int): String {
        val name = assertNotNull(names[id], "no string resource has id $id")
        return assertNotNull(strings[name], "$name is not in strings.xml")
    }

    @Test
    fun everyButtonIsACatalogActionShownAndReadOutByItsNames() {
        val known = actions("device") + actions("local")
        val problems = VIEWER_BAR.flatMap { button ->
            val action = known[button.id] ?: return@flatMap listOf("${button.id} is not an action in actions.json")
            val label = action.field("label")
            val short = action.field("short")
            val text = string(button.text)
            val description = string(button.description)
            listOfNotNull(
                "${button.id} shows \"$text\", neither its short name \"$short\" nor its label \"$label\"".takeIf { text != short && text != label },
                "${button.id} is read out as \"$description\", not by its label \"$label\"".takeIf { description != label },
            )
        }
        assertEquals(emptyList(), problems)
    }

    @Test
    fun everyDeviceActionIsOnTheBarOnce() {
        val ids = VIEWER_BAR.map { it.id }
        assertEquals(ids.distinct(), ids, "an action is on the bar twice")
        assertEquals(emptySet(), actions("device").keys - ids.toSet(), "device actions missing from the bar")
    }

    private fun JsonObject.field(key: String): String = getValue(key).jsonPrimitive.content

    /** A plain string resource as aapt2 compiles it: whitespace collapsed, backslash escapes resolved. */
    private fun compiled(raw: String): String =
        raw.trim().replace(Regex("\\s+"), " ").replace(Regex("""\\(.)""")) {
            when (val c = it.groupValues[1]) {
                "n" -> "\n"
                "t" -> "\t"
                else -> c
            }
        }
}
