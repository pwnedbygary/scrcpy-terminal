package io.github.pwnedbygary.scterm.protocol

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import java.io.File

/** Loads the repository-level golden fixtures shared with the Go client. */
object Fixtures {
    private val dir: File by lazy {
        File(System.getProperty("scterm.fixtures") ?: error("scterm.fixtures system property not set"))
    }

    fun load(name: String): JsonObject = Json.parseToJsonElement(File(dir, name).readText()).jsonObject

    fun unhex(s: String): ByteArray = ByteArray(s.length / 2) { i -> s.substring(i * 2, i * 2 + 2).toInt(16).toByte() }

    fun JsonObject.int(key: String): Int = getValue(key).jsonPrimitive.int
    fun JsonObject.long(key: String): Long = getValue(key).jsonPrimitive.long
    fun JsonObject.bool(key: String): Boolean = getValue(key).jsonPrimitive.boolean
    fun JsonObject.str(key: String): String = getValue(key).jsonPrimitive.content
}
