package diff

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// classes maps chroma's token types to the few classes the stylesheet
// colours. A type not listed takes its subcategory's class, then its
// category's, and otherwise none, so it shows in the text colour.
var classes = map[chroma.TokenType]string{
	chroma.Keyword:           "tok-keyword",
	chroma.KeywordConstant:   "tok-constant",
	chroma.KeywordType:       "tok-type",
	chroma.OperatorWord:      "tok-keyword",
	chroma.NameFunction:      "tok-function",
	chroma.NameFunctionMagic: "tok-function",
	chroma.NameClass:         "tok-type",
	chroma.NameException:     "tok-type",
	chroma.NameNamespace:     "tok-type",
	chroma.NameBuiltin:       "tok-builtin",
	chroma.NameConstant:      "tok-constant",
	chroma.NameDecorator:     "tok-meta",
	chroma.NameTag:           "tok-tag",
	chroma.NameAttribute:     "tok-attribute",
	chroma.LiteralString:     "tok-string",
	chroma.LiteralNumber:     "tok-number",
	chroma.Comment:           "tok-comment",
	chroma.CommentPreproc:    "tok-meta",
	chroma.Operator:          "tok-operator",
	chroma.GenericHeading:    "tok-heading",
	chroma.GenericSubheading: "tok-heading",
	chroma.GenericInserted:   "tok-inserted",
	chroma.GenericDeleted:    "tok-deleted",
}

func classFor(tokenType chroma.TokenType) string {
	for _, candidate := range []chroma.TokenType{tokenType, tokenType.SubCategory(), tokenType.Category()} {
		if class, ok := classes[candidate]; ok {
			return class
		}
	}
	return ""
}

// lexerFor picks a lexer by the file's name alone.
func lexerFor(path string) chroma.Lexer {
	if path == "" {
		return nil
	}
	return lexers.Match(path)
}

// highlight tokenizes one version of a file whole, so a construct that
// spans lines, such as a block comment, is coloured where a hunk starts
// inside it. It returns each line's pieces, indexed by line number less
// one.
func highlight(lexer chroma.Lexer, text string) [][]Span {
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return nil
	}
	lines := [][]Span{nil}
	for token := iterator(); token != chroma.EOF; token = iterator() {
		class := classFor(token.Type)
		for index, part := range strings.Split(token.Value, "\n") {
			if index > 0 {
				lines = append(lines, nil)
			}
			if part != "" {
				last := len(lines) - 1
				lines[last] = appendSpan(lines[last], Span{Text: part, Class: class})
			}
		}
	}
	for index, line := range lines {
		if last := len(line) - 1; last >= 0 {
			line[last].Text = strings.TrimSuffix(line[last].Text, "\r")
			if line[last].Text == "" {
				lines[index] = line[:last]
			}
		}
	}
	return lines
}

// appendSpan adds a span, joining it to the last one when they look the
// same.
func appendSpan(spans []Span, next Span) []Span {
	if last := len(spans) - 1; last >= 0 && spans[last].Class == next.Class && spans[last].Changed == next.Changed {
		spans[last].Text += next.Text
		return spans
	}
	return append(spans, next)
}

// lineSpans splits a line's text by its highlighting pieces and the ranges
// that changed within it. Pieces that do not join to the line's text, as
// when the file was not read whole, are dropped rather than trusted. A line
// with neither gets no spans.
func lineSpans(text string, pieces []Span, changed []span) []Span {
	var joined strings.Builder
	classed := false
	for _, piece := range pieces {
		joined.WriteString(piece.Text)
		classed = classed || piece.Class != ""
	}
	if !classed || joined.String() != text {
		pieces, classed = []Span{{Text: text}}, false
	}
	if !classed && len(changed) == 0 {
		return nil
	}
	var spans []Span
	offset := 0
	for _, piece := range pieces {
		for start := 0; start < len(piece.Text); {
			at := offset + start
			isChanged, until := changedAt(changed, at)
			end := min(len(piece.Text), until-offset)
			spans = appendSpan(spans, Span{Text: piece.Text[start:end], Class: piece.Class, Changed: isChanged})
			start = end
		}
		offset += len(piece.Text)
	}
	return spans
}

// changedAt reports whether the byte at offset is in a changed range, and
// where that stops being true.
func changedAt(changed []span, offset int) (bool, int) {
	for _, current := range changed {
		if offset < current.start {
			return false, current.start
		}
		if offset < current.end {
			return true, current.end
		}
	}
	return false, int(^uint(0) >> 1)
}
