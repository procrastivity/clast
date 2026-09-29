package analyzeverb

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// markdownHTML renders the small markdown subset clast entries and retro
// summaries use — "#" headings, "-"/"*" bullets (a wrapped bullet's
// continuation lines join it), paragraphs, **bold**, `code` and
// [text](http(s) url) links — as HTML. Every piece of source text is
// escaped before any tag is added, so the result is safe to mark as
// template.HTML. Anything outside the subset reads as plain paragraph
// text, never as markup.
func markdownHTML(src string) template.HTML {
	var out, para, items []string

	flushPara := func() {
		if len(para) > 0 {
			out = append(out, "<p>"+inlineHTML(strings.Join(para, " "))+"</p>")
			para = para[:0]
		}
	}
	flushItems := func() {
		if len(items) > 0 {
			var b strings.Builder
			b.WriteString("<ul>")
			for _, it := range items {
				b.WriteString("<li>" + inlineHTML(it) + "</li>")
			}
			b.WriteString("</ul>")
			out = append(out, b.String())
			items = items[:0]
		}
	}

	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flushPara()
			flushItems()
		case headingRE.MatchString(line):
			flushPara()
			flushItems()
			m := headingRE.FindStringSubmatch(line)
			// An entry's "##" sections sit under the page's own h2/h3,
			// so heading levels shift down two, capped at h5.
			level := string(rune('0' + min(len(m[1])+2, 5)))
			out = append(out, "<h"+level+">"+inlineHTML(m[2])+"</h"+level+">")
		case bulletRE.MatchString(line):
			flushPara()
			items = append(items, bulletRE.ReplaceAllString(line, ""))
		case len(items) > 0:
			items[len(items)-1] += " " + line
		default:
			para = append(para, line)
		}
	}
	flushPara()
	flushItems()
	return template.HTML(strings.Join(out, "\n"))
}

var (
	headingRE = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	bulletRE  = regexp.MustCompile(`^[-*]\s+`)
	codeRE    = regexp.MustCompile("`([^`]+)`")
	boldRE    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	linkRE    = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
)

// inlineHTML escapes s and then adds its inline markup. Code spans are
// cut out first, so "**" or a link inside backticks stays literal.
func inlineHTML(s string) string {
	var b strings.Builder
	for {
		loc := codeRE.FindStringSubmatchIndex(s)
		if loc == nil {
			b.WriteString(emphasisHTML(s))
			return b.String()
		}
		b.WriteString(emphasisHTML(s[:loc[0]]))
		b.WriteString("<code>" + html.EscapeString(s[loc[2]:loc[3]]) + "</code>")
		s = s[loc[1]:]
	}
}

func emphasisHTML(s string) string {
	s = html.EscapeString(s)
	s = boldRE.ReplaceAllString(s, "<strong>$1</strong>")
	return linkRE.ReplaceAllString(s, `<a href="$2" target="_blank" rel="noopener">$1</a>`)
}
