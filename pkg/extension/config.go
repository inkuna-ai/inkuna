package extension

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/inkuna-ai/inkuna/internal/yamlnode"
	"gopkg.in/yaml.v3"
)

// Configurable is an Extension that takes its own section of inkuna.yaml,
// the entry under extensions named by the extension:
//
//	extensions:
//	  example:
//	    endpoint: https://records.example.com/inkuna
//
// The rest of inkuna.yaml decodes strictly, so this section is the only place
// an extension's settings can go.
type Configurable interface {
	Extension
	// Configure receives the extension's section once, after every
	// extension has registered and inkuna.yaml has been read, and before the
	// app starts. With no section in the file it receives the zero Node, so
	// an extension can refuse to start unconfigured, though that also stops
	// a first launch before the setup wizard has written inkuna.yaml. An
	// error stops the app from starting, as an invalid inkuna.yaml does,
	// naming the file and the section. A changed section takes effect at
	// the next start.
	Configure(section yaml.Node) error
}

// DecodeConfig decodes a section strictly into out, which must be a pointer:
// a key out has no field for is an error, as it is everywhere else in
// inkuna.yaml. Errors give the section's lines in inkuna.yaml. The zero Node,
// and a section left empty, leave out as it is, so out can hold defaults.
func DecodeConfig(section yaml.Node, out any) error {
	if section.Kind == 0 {
		return nil
	}
	// yaml.Node.Decode cannot reject unknown keys; only a Decoder can, so
	// the section is encoded and decoded again. Comments go first, since
	// they would shift the lines a Decoder reports, and so do aliases, whose
	// anchors may lie outside the section.
	bare, err := yamlnode.Resolve(&section, true)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(bare)
	if err != nil {
		return fmt.Errorf("encode section: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	err = decoder.Decode(out)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	var typeError *yaml.TypeError
	if !errors.As(err, &typeError) {
		return err
	}
	// Re-encoding also drops blank lines, so each line the Decoder reports
	// is mapped back through the encoded copy to the line it came from.
	var encoded yaml.Node
	if yaml.Unmarshal(data, &encoded) != nil || len(encoded.Content) != 1 {
		return err
	}
	lines := map[int]int{}
	mapLines(encoded.Content[0], bare, lines)
	messages := make([]string, len(typeError.Errors))
	for index, message := range typeError.Errors {
		messages[index] = errorLine.ReplaceAllStringFunc(message, func(reference string) string {
			match := errorLine.FindStringSubmatch(reference)
			line, _ := strconv.Atoi(match[2])
			if source, ok := lines[line]; ok {
				line = source
			}
			return match[1] + strconv.Itoa(line) + match[3]
		})
	}
	return &yaml.TypeError{Errors: messages}
}

// errorLine matches the line references in a Decoder's messages: the one
// each starts with, and the earlier one a duplicate key names
// ("mapping key "x" already defined at line 3"). Text quoted from the
// section sits between them and never matches either form.
var errorLine = regexp.MustCompile(`(^line |already defined at line )(\d+)(:|$)`)

// mapLines records, for each node of encoded, the line its counterpart in
// source has. The two trees have one shape, since one was encoded from the
// other.
func mapLines(encoded, source *yaml.Node, lines map[int]int) {
	if _, ok := lines[encoded.Line]; !ok {
		lines[encoded.Line] = source.Line
	}
	for index := 0; index < len(encoded.Content) && index < len(source.Content); index++ {
		mapLines(encoded.Content[index], source.Content[index], lines)
	}
}
