package analyzeverb

import "testing"

func TestMarkdownHTML(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"heading shifts two levels", "## Goal", "<h4>Goal</h4>"},
		{"heading caps at h5", "#### Deep", "<h5>Deep</h5>"},
		{"paragraph lines join", "one\ntwo\n\nthree", "<p>one two</p>\n<p>three</p>"},
		{"bullets", "- a\n* b", "<ul><li>a</li><li>b</li></ul>"},
		{"wrapped bullet joins its item", "- a long\n  bullet\n- next", "<ul><li>a long bullet</li><li>next</li></ul>"},
		{"bold is not a bullet", "**Issue:** x", "<p><strong>Issue:</strong> x</p>"},
		{"code", "run `go test`", "<p>run <code>go test</code></p>"},
		{"code keeps markup literal", "`**x** [a](https://b)`", "<p><code>**x** [a](https://b)</code></p>"},
		{"link", "[docs](https://example.com/a?b=1&c=2)", `<p><a href="https://example.com/a?b=1&amp;c=2" target="_blank" rel="noopener">docs</a></p>`},
		{"non-http link stays text", "[x](javascript:alert(1))", "<p>[x](javascript:alert(1))</p>"},
		{"html is escaped", `<script>alert("x")</script>`, "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p>"},
		{"html in code is escaped", "`<b>`", "<p><code>&lt;b&gt;</code></p>"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(markdownHTML(c.src)); got != c.want {
				t.Errorf("markdownHTML(%q)\n got %q\nwant %q", c.src, got, c.want)
			}
		})
	}
}
