package com.homegrownsw.hammock.agentview

import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class MarkdownTest {
    @Test
    fun blocks() {
        val md = """
            # Title
            Some text
            on two lines.

            - one
            - [x] done
              continued
            1. first

            > quoted
            > more

            | a | b |
            |---|:-:|
            | 1 | 2 |

            ---
            ```swift
            let x = 1
            ```
        """.trimIndent()
        val b = Markdown.parse(md)
        assertEquals(MdBlock.Heading(1, "Title"), b[0])
        assertEquals(MdBlock.Paragraph("Some text\non two lines."), b[1])
        val list = b[2] as MdBlock.ListBlock
        assertEquals(listOf("•", "", "1."), list.items.map { it.marker })
        assertEquals(true, list.items[1].checked)
        assertEquals("done continued", list.items[1].text)
        assertEquals(MdBlock.Quote("quoted\nmore"), b[3])
        assertEquals(MdBlock.Table(listOf("a", "b"), listOf(listOf("1", "2"))), b[4])
        assertEquals(MdBlock.Rule, b[5])
        assertEquals(MdBlock.Code("swift", "let x = 1"), b[6])
        assertEquals(7, b.size)
    }

    @Test
    fun aLongerFenceKeepsShorterOnesInside() {
        val b = Markdown.parse("````md\n```go\nx\n```\n````\nafter")
        assertEquals(MdBlock.Code("md", "```go\nx\n```"), b[0])
        assertEquals(MdBlock.Paragraph("after"), b[1])
    }

    @Test
    fun anUnclosedFenceRunsToTheEnd() {
        assertEquals(listOf<MdBlock>(MdBlock.Code("", "a\nb")), Markdown.parse("```\na\nb"))
    }

    @Test
    fun notAHeadingWithoutASpace() {
        assertEquals(listOf<MdBlock>(MdBlock.Paragraph("#hashtag")), Markdown.parse("#hashtag"))
    }

    private fun styled(a: AnnotatedString, text: String) = a.spanStyles.filter { a.text.substring(it.start, it.end) == text }.map { it.item }

    @Test
    fun inlineEmphasisCodeAndLinks() {
        val a = Markdown.inline("Some **bold**, *it*, `code`, ~~gone~~ and [a link](https://example.com).")
        assertEquals("Some bold, it, code, gone and a link.", a.text)
        assertTrue(styled(a, "bold").any { it.fontWeight == FontWeight.SemiBold })
        assertTrue(styled(a, "it").any { it.fontStyle == FontStyle.Italic })
        assertTrue(styled(a, "code").any { it.fontFamily == FontFamily.Monospace })
        val links = a.getLinkAnnotations(0, a.length)
        assertEquals(1, links.size)
        assertEquals("https://example.com", (links[0].item as LinkAnnotation.Url).url)
        assertEquals("a link", a.text.substring(links[0].start, links[0].end))
    }

    @Test
    fun snakeCaseAndLoneMarkersStayAsTyped() {
        assertEquals("a snake_case_name and 2 * 3 * 4", Markdown.inline("a snake_case_name and 2 * 3 * 4").text)
        assertEquals("**unclosed", Markdown.inline("**unclosed").text)
        assertEquals("not *a* link: [x]", Markdown.inline("not \\*a\\* link: [x]").text)
    }

    @Test
    fun bareURLsAndPathsAreLinks() {
        val a = Markdown.inline("See https://example.com/a_b. And /Users/me/Developer/app/main.go, not `/tmp/x/y`.")
        val urls = a.getLinkAnnotations(0, a.length).map { (it.item as LinkAnnotation.Url).url }
        assertEquals(listOf("https://example.com/a_b", "file:///Users/me/Developer/app/main.go"), urls)
        assertTrue(a.text.contains("/tmp/x/y"))
    }

    @Test
    fun aLinksOwnTextIsntLinkedAgain() {
        val a = Markdown.inline("[https://example.com](https://example.com)")
        assertEquals(1, a.getLinkAnnotations(0, a.length).size)
    }

    @Test
    fun proseJoinsBlocksIntoOneText() {
        val p = Markdown.prose(Markdown.parse("# Hi\n\n- a\n- [ ] b\n\n> q"), MdStyle())
        assertEquals("Hi\n\n• a\n☐ b\n\n▍ q", p.text)
        val runs = Markdown.runs("Text\n\n```\ncode\n```\nMore", MdStyle())
        assertEquals(3, runs.size)
        assertTrue(runs[1] is Markdown.Run.Block)
    }

    @Test
    fun highlighting() {
        val style = MdStyle()
        val h = CodeHighlight.highlighted("let s = \"hi\" // note\nreturn 42", "swift", style)
        fun colour(t: String) = h.spanStyles.first { h.text.substring(it.start, it.end) == t }.item.color
        assertEquals(style.keyword, colour("let"))
        assertEquals(style.string, colour("\"hi\""))
        assertEquals(style.secondary, colour("// note"))
        assertEquals(style.number, colour("42"))
        // A # isn't a comment in Swift, and plain text isn't highlighted.
        assertTrue(CodeHighlight.highlighted("# not", "swift", style).spanStyles.none { it.item.color == style.secondary })
        assertTrue(CodeHighlight.highlighted("let x", "text", style).spanStyles.isEmpty())
    }
}
