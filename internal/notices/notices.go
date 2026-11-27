// Package notices builds THIRD_PARTY_NOTICES, the copyright and license
// text of the third-party code the desktop app is built from. MIT, BSD, ISC
// and Apache licenses require that text to go with every copy, and the
// bundled page drops the license comments the packages carried, so the app
// ships this file instead and shows it from Settings.
package notices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Path is where the notices live, relative to the repository root. The
// frontend imports the file from there, so the page carries it.
const Path = "THIRD_PARTY_NOTICES"

// Component is one third-party module or package and the license text it
// ships.
type Component struct {
	Name    string
	Version string
	// License is the SPDX expression the package declares, when it declares
	// one. Go modules declare none; their text says what they are.
	License string
	Texts   []Text
}

// Text is one license file, verbatim.
type Text struct {
	File string
	Body string
}

// Target is one platform the app is built for. Each pulls in modules the
// others do not, such as go-webview2 on Windows and dbus on Linux.
type Target struct {
	GOOS, GOARCH string
	Tags         []string
}

// Targets are the platforms task package builds the app for, with the tags
// it builds them with.
var Targets = []Target{
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "amd64", Tags: []string{"webkit2_41"}},
	{GOOS: "windows", GOARCH: "amd64"},
}

// bundledTools are development dependencies whose own code ends up in the
// built page, so they ship even though npm counts them as dev only.
var bundledTools = map[string]string{
	"tailwindcss": "its base styles are in the page's stylesheet",
	"vite":        "its module preload polyfill is in the page's script",
}

// licenseFile matches the files a package keeps its license and notices
// in: LICENSE, LICENSE.md, LICENSE-MIT, COPYING, NOTICE, PATENTS and the
// like. Some packages also ship a script beside them, such as
// license-update.mjs, which licenseText leaves out by its extension.
var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents|unlicense)([.\-_].*)?$`)

// licenseText says whether name, a file licenseFile matches, holds text
// rather than code.
func licenseText(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case "", ".md", ".markdown", ".txt", ".rst":
		return true
	}
	return false
}

// Generate returns THIRD_PARTY_NOTICES for the repository at root: the Go
// standard library and every module the app's main package imports on
// targets, then every npm package the page is built from.
func Generate(root string, targets []Target) ([]byte, error) {
	goComponents, err := GoModules(root, targets)
	if err != nil {
		return nil, err
	}
	npmComponents, err := NPMPackages(filepath.Join(root, "frontend"))
	if err != nil {
		return nil, err
	}
	return Render(goComponents, npmComponents), nil
}

// GoModules lists the Go standard library and the modules the main package
// at root imports when built for any of targets, with their license files.
// A module without one is an error: shipping it would leave its license
// out.
func GoModules(root string, targets []Target) ([]Component, error) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("find go: %w", err)
	}
	type module struct {
		Path, Version, Dir string
		Main               bool
		Replace            *module
	}
	modules := map[string]module{}
	for _, target := range targets {
		args := []string{"list", "-e", "-deps", "-json=ImportPath,Module,Error", "-tags", strings.Join(target.Tags, ","), "."}
		command := exec.Command(goBinary, args...)
		command.Dir = root
		// The Wails packages are cgo on macOS and Linux; without cgo, go
		// list would leave them, and what they import, out.
		command.Env = append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=1")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("list the packages built for %s/%s: %w: %s", target.GOOS, target.GOARCH, err, stderr.String())
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var listed struct {
				ImportPath string
				Module     *module
				Error      *struct{ Err string }
			}
			if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("read the packages built for %s/%s: %w", target.GOOS, target.GOARCH, err)
			}
			if listed.Error != nil {
				return nil, fmt.Errorf("list %s for %s/%s: %s", listed.ImportPath, target.GOOS, target.GOARCH, listed.Error.Err)
			}
			if listed.Module == nil || listed.Module.Main {
				continue
			}
			found := *listed.Module
			if found.Replace != nil {
				found.Dir, found.Version = found.Replace.Dir, found.Replace.Version
			}
			modules[found.Path] = found
		}
	}

	goroot, err := exec.Command(goBinary, "env", "GOROOT").Output()
	if err != nil {
		return nil, fmt.Errorf("find GOROOT: %w", err)
	}
	standard, err := standardLicense(strings.TrimSpace(string(goroot)))
	if err != nil {
		return nil, fmt.Errorf("read the Go standard library's license: %w", err)
	}
	// No version: the toolchain building a release may be any patch of the
	// go.mod version, and the license is the same for all of them.
	components := []Component{{Name: "Go standard library", Texts: standard}}
	paths := make([]string, 0, len(modules))
	for modulePath := range modules {
		paths = append(paths, modulePath)
	}
	sort.Strings(paths)
	for _, modulePath := range paths {
		found := modules[modulePath]
		texts, err := licenseTexts(found.Dir)
		if err != nil {
			return nil, fmt.Errorf("read the license of %s %s: %w", found.Path, found.Version, err)
		}
		components = append(components, Component{Name: found.Path, Version: found.Version, Texts: texts})
	}
	return components, nil
}

// NPMPackages lists the packages package-lock.json in frontend installs for
// production, and bundledTools, with their license files from
// node_modules. A package that ships no license file but states its license
// in its README gets that section instead, as fastdom does.
func NPMPackages(frontend string) ([]Component, error) {
	data, err := os.ReadFile(filepath.Join(frontend, "package-lock.json"))
	if err != nil {
		return nil, fmt.Errorf("read the frontend's package lock: %w", err)
	}
	var lock struct {
		Packages map[string]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			License any    `json:"license"`
			Dev     bool   `json:"dev"`
			Link    bool   `json:"link"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("read the frontend's package lock: %w", err)
	}
	seen := map[string]bool{}
	var components []Component
	for location, entry := range lock.Packages {
		// "" is the frontend itself.
		if location == "" || entry.Link {
			continue
		}
		name := entry.Name
		if name == "" {
			name = location[strings.LastIndex(location, "node_modules/")+len("node_modules/"):]
		}
		// A bundled tool is the copy the frontend installs at the top, not
		// one some other tool depends on.
		if entry.Dev && (bundledTools[name] == "" || location != "node_modules/"+name) {
			continue
		}
		key := name + "@" + entry.Version
		if seen[key] {
			continue
		}
		seen[key] = true
		dir := filepath.Join(frontend, filepath.FromSlash(location))
		texts, err := licenseTexts(dir)
		if err != nil {
			texts, err = readmeLicense(dir)
		}
		if err != nil {
			return nil, fmt.Errorf("read the license of %s %s: %w", name, entry.Version, err)
		}
		components = append(components, Component{Name: name, Version: entry.Version, License: spdx(entry.License), Texts: texts})
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i].Name != components[j].Name {
			return components[i].Name < components[j].Name
		}
		return components[i].Version < components[j].Version
	})
	return components, nil
}

// spdx reads package.json's license, which old packages give as an object.
func spdx(license any) string {
	switch value := license.(type) {
	case string:
		return value
	case map[string]any:
		if kind, ok := value["type"].(string); ok {
			return kind
		}
	}
	return ""
}

// errNoLicense is returned for a directory without a license file.
var errNoLicense = errors.New("no license file")

// licenseTexts reads the license files at the top of dir, in name order.
func licenseTexts(dir string) ([]Text, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var texts []Text
	for _, entry := range entries {
		if entry.IsDir() || !licenseFile.MatchString(entry.Name()) || !licenseText(entry.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		texts = append(texts, Text{File: entry.Name(), Body: normalize(string(body))})
	}
	if len(texts) == 0 {
		return nil, errNoLicense
	}
	return texts, nil
}

// standardLicense reads the Go standard library's license texts from
// GOROOT. Homebrew moves a formula's LICENSE out of GOROOT (its libexec) to
// the directory above, leaving PATENTS, so a LICENSE missing from GOROOT is
// read from there, and the notices are the same whichever Go built them.
func standardLicense(goroot string) ([]Text, error) {
	texts, err := licenseTexts(goroot)
	if err != nil && !errors.Is(err, errNoLicense) {
		return nil, err
	}
	for _, text := range texts {
		if strings.HasPrefix(text.File, "LICENSE") {
			return texts, nil
		}
	}
	above, aboveErr := licenseTexts(filepath.Dir(goroot))
	for _, text := range above {
		if strings.HasPrefix(text.File, "LICENSE") {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		if aboveErr != nil {
			return nil, aboveErr
		}
		return nil, errNoLicense
	}
	sort.Slice(texts, func(i, j int) bool { return texts[i].File < texts[j].File })
	return texts, nil
}

// readmeLicense reads the License section of the README in dir: from a
// Markdown heading that names the license to the next heading as high.
func readmeLicense(dir string) ([]Text, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(strings.TrimSuffix(entry.Name(), path.Ext(entry.Name())), "readme") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if section := licenseSection(normalize(string(body))); section != "" {
			return []Text{{File: entry.Name() + " (License section)", Body: section}}, nil
		}
	}
	return nil, errNoLicense
}

var heading = regexp.MustCompile(`^(#+)\s+(.*)$`)

func licenseSection(readme string) string {
	lines := strings.Split(readme, "\n")
	for start, line := range lines {
		match := heading.FindStringSubmatch(line)
		if match == nil || !strings.HasPrefix(strings.ToLower(match[2]), "licen") {
			continue
		}
		end := len(lines)
		for next := start + 1; next < len(lines); next++ {
			if other := heading.FindStringSubmatch(lines[next]); other != nil && len(other[1]) <= len(match[1]) {
				end = next
				break
			}
		}
		section := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
		if section != "" {
			return section + "\n"
		}
	}
	return ""
}

// normalize gives every text LF line ends and one final newline, so the
// file is the same whichever system generated it.
func normalize(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, " \t\n") + "\n"
}

const rule = "================================================================================"

// Render writes the notices file for the Go and npm components.
func Render(goComponents, npmComponents []Component) []byte {
	var out strings.Builder
	out.WriteString("THIRD-PARTY NOTICES\n\n")
	out.WriteString("Inkuna is built from the third-party software below. Each entry gives the\n")
	out.WriteString("copyright and license text that the module or package ships, verbatim.\n\n")
	out.WriteString("This file is generated. After changing dependencies, run task notices.\n")
	section := func(title string, components []Component) {
		out.WriteString("\n" + rule + "\n" + title + "\n" + rule + "\n")
		for _, component := range components {
			name := component.Name
			if component.Version != "" {
				name += " " + component.Version
			}
			out.WriteString("\n" + strings.Repeat("-", len(rule)) + "\n" + name + "\n")
			if component.License != "" {
				out.WriteString("License: " + component.License + "\n")
			}
			for _, text := range component.Texts {
				out.WriteString("\n--- " + text.File + "\n\n" + text.Body)
			}
		}
	}
	section("Go modules, compiled into the app", goComponents)
	section("npm packages, bundled into the app's page", npmComponents)
	return []byte(out.String())
}
