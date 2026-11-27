package report

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"

	"github.com/inkuna-ai/inkuna/internal/diff"
)

//go:embed page.html
var pageTemplate string

//go:embed page.css
var pageStyle string

//go:embed page.js
var pageScript string

var funcs = template.FuncMap{
	"plural": func(count int, one, many string) string {
		if count == 1 {
			return fmt.Sprintf("%d %s", count, one)
		}
		return fmt.Sprintf("%d %s", count, many)
	},
	"mark": func(status string) string {
		switch status {
		case "passed":
			return "✓"
		case "failed":
			return "✗"
		case "cancelled":
			return "⊘"
		case "running":
			return "●"
		}
		return "–"
	},
	"tone": func(failed, passed, total int) string {
		switch {
		case failed > 0:
			return "tone-failed"
		case passed == total:
			return "tone-ok"
		}
		return ""
	},
}

var compiled = template.Must(template.New("page").Funcs(funcs).Parse(pageTemplate))

// style is the page's stylesheet: the diff model's, which the app shares,
// then the page's own.
var style = diff.Stylesheet() + pageStyle

// policy lets the page run only its own inline style and script, named by
// their hashes, and load nothing at all, so even text that escaped its
// escaping could not run or reach the network.
var policy = fmt.Sprintf("default-src 'none'; style-src '%s'; script-src '%s'; img-src 'none'; base-uri 'none'; form-action 'none'", sha256Source(style), sha256Source(pageScript))

func sha256Source(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// Render writes a page as one HTML file. Everything from the run, its
// paths, lines, subjects, and logs, goes through html/template's escaping.
func Render(w io.Writer, view Page) error {
	return compiled.Execute(w, struct {
		Page   Page
		Policy string
		Style  template.CSS
		Script template.JS
	}{Page: view, Policy: policy, Style: template.CSS(style), Script: template.JS(pageScript)})
}
