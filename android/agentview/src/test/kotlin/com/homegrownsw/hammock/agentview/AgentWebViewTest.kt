package com.homegrownsw.hammock.agentview

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.ChevronRight
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class AgentWebViewTest {
    @Test
    fun iconsBecomeSvgPaths() {
        val svg = AgentWebBridge.svg(Icons.Outlined.Check)
        assertTrue(svg.startsWith("<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 24.0 24.0\">"), svg)
        assertTrue(svg.contains("<path d=\"M"), svg)
        assertTrue(svg.endsWith("</svg>"), svg)
        // Every node written: no class name in place of a command.
        val path = AgentWebBridge.svg(Icons.Outlined.ChevronRight).substringAfter("d=\"").substringBefore("\"")
        assertTrue(path.all { it.isDigit() || it in " .-ZzMmLlHhVvCcSsQqTtAaE" }, path)
    }

    @Test
    fun filesAreKnownByTheirFirstBytes() {
        fun bytes(vararg b: Int) = ByteArray(b.size) { b[it].toByte() }
        fun box(brand: String) = byteArrayOf(0, 0, 0, 24) + "ftyp$brand".toByteArray()
        assertEquals("image/png", AgentWebBridge.mediaType(bytes(0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A)))
        assertEquals("image/jpeg", AgentWebBridge.mediaType(bytes(0xFF, 0xD8, 0xFF, 0xE0)))
        // An ISO box file is a HEIF picture or a video, by its brand.
        assertEquals("image/heic", AgentWebBridge.mediaType(box("heic")))
        assertEquals("video/mp4", AgentWebBridge.mediaType(box("isom")))
        assertEquals("video/quicktime", AgentWebBridge.mediaType(box("qt  ")))
        assertEquals("", AgentWebBridge.mediaType(bytes(1, 2, 3)))
    }
}
