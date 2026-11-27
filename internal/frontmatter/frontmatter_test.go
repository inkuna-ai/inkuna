package frontmatter

import (
	"errors"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string]struct {
		content      string
		header, body string
		err          error
	}{
		"lf":            {content: "---\nname: a\n---\nbody\n", header: "name: a\n", body: "body\n"},
		"crlf":          {content: "---\r\nname: a\r\n---\r\nbody\r\n", header: "name: a\n", body: "body\r\n"},
		"empty body":    {content: "---\nname: a\n---\n", header: "name: a\n", body: ""},
		"no final line": {content: "---\nname: a\n---", header: "name: a\n", body: ""},
		"empty header":  {content: "---\n---\nbody", header: "", body: "body"},
		"missing":       {content: "just text", err: ErrMissing},
		"empty file":    {content: "", err: ErrMissing},
		"unclosed":      {content: "---\nname: a\n", err: ErrUnclosed},
	}
	for name, tc := range cases {
		header, body, err := Split(tc.content)
		if !errors.Is(err, tc.err) {
			t.Fatalf("%s: err = %v, want %v", name, err, tc.err)
		}
		if header != tc.header || body != tc.body {
			t.Fatalf("%s: got header %q body %q, want %q %q", name, header, body, tc.header, tc.body)
		}
	}
}
