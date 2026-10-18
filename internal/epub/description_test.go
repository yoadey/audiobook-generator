package epub

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Para one.\n\nPara   two.\n", "Para one.\n\nPara two."},
		{"if a<b then stop", "if a<b then stop"},
		{"<h2>T</h2>text<hr/>more", "T\n\ntext\n\nmore"},
		{"<table><tr><td>a</td><td>b</td></tr></table>", "a b"},
		{"<dl><dt>Term</dt><dd>Meaning</dd></dl>", "Term\n\nMeaning"},
		{strings.Repeat("<div>", 600) + "deep", ""},
	} {
		assert.Equal(t, tc.want, plainText(tc.in), tc.in)
	}
}

// dc:description holds plain text, escaped HTML, CDATA or raw XHTML.
func TestDescriptionSource(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<d>Plain &amp; simple.</d>", "Plain & simple."},
		{"<d>&lt;p&gt;Escaped &lt;i&gt;HTML&lt;/i&gt;&lt;/p&gt;</d>", "Escaped HTML"},
		{"<d><![CDATA[<p>In CDATA</p>]]></d>", "In CDATA"},
		{`<d><p xmlns="http://www.w3.org/1999/xhtml">Raw <i>xhtml</i></p><p>Two</p></d>`, "Raw xhtml\n\nTwo"},
	} {
		var d xmlText
		require.NoError(t, xml.Unmarshal([]byte(tc.in), &d))
		assert.Equal(t, tc.want, plainText(d.source()), tc.in)
	}
}
