//go:build docs

package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

var (
	// The pymdown-extensions snippets syntax: an inline include line, the block form's opening and closing line, and
	// a section marker in an included file. A leading ';' escapes an include line.
	includeLine   = regexp.MustCompile(`^[ \t]*(;*)-+8<-+[ \t]+("(?:\\"|[^"\r\n])+?"|'(?:\\'|[^'\r\n])+?')\r?$`)
	blockLine     = regexp.MustCompile(`^[ \t]*-+8<-+\r?$`)
	sectionMarker = regexp.MustCompile(`(?i)-+8<-+[ \t]+\[[ \t]*(start|end)[ \t]*:[ \t]*([a-z][-_0-9a-z]*)[ \t]*\]`)
	sectionSuffix = regexp.MustCompile(`(?i)^(.*?):([a-z][-_0-9a-z]*)$`)
	linesSuffix   = regexp.MustCompile(`^(.*?)((?::[0-9]*(?::[0-9]*)?)(?:,[0-9]*(?::[0-9]*)?)*)$`)
	fenceOpen     = regexp.MustCompile("^([ \t]*)(`{3,}|~{3,})[ \t]*\\{?\\.?([A-Za-z0-9_-]*)")
	topAPIVersion = regexp.MustCompile(`(?m)^apiVersion:`)
	topKind       = regexp.MustCompile(`(?m)^kind:`)
	docSeparator  = regexp.MustCompile(`^---([ \t].*)?$`)
	versionTokens = strings.NewReplacer("@VERSION@", "0.0.0", "@TAG@", "v0.0.0")
)

// manifest is one YAML document with apiVersion and kind, and where a reader finds it.
type manifest struct {
	file string // repo-relative path
	line int    // 1-based line of the document's first key
	obj  map[string]any
}

func (m manifest) at() string { return fmt.Sprintf("%s:%d", m.file, m.line) }

// source is one place the docs show manifests: a file, a file section, or a fenced block in a page.
type source struct {
	name      string
	manifests []manifest
	errs      []string
}

// snippetRef is one --8<-- include: the page or file and line it sits on, and the target it names.
type snippetRef struct {
	from    string
	line    int
	target  string
	section string
	lines   string
}

// fence is a fenced yaml block of a docs page with snippet lines removed and its indentation stripped.
type fence struct {
	file string
	line int
	text string
}

type collection struct {
	examples, generated, includes, fences []source
	snippets                              []snippetRef
	yamlFences                            []fence
	pages                                 int
}

func (c collection) all() []source {
	out := append(append([]source{}, c.examples...), c.generated...)
	return append(append(out, c.includes...), c.fences...)
}

func collect(t *testing.T, root string) collection {
	t.Helper()
	var c collection
	c.examples = fileSources(t, root, "examples", "examples/live/")
	c.generated = fileSources(t, root, "examples-generated", "")
	covered := map[string]bool{}
	for _, s := range append(append([]source{}, c.examples...), c.generated...) {
		covered[s.name] = true
	}

	for _, page := range walk(t, root, "docs", ".md") {
		c.pages++
		text := readFile(t, root, page)
		c.snippets = append(c.snippets, snippetRefs(page, text)...)
		for _, f := range yamlFences(page, text) {
			c.yamlFences = append(c.yamlFences, f)
			if !topAPIVersion.MatchString(f.text) || !topKind.MatchString(f.text) {
				continue
			}
			s := decode(fmt.Sprintf("%s:%d", page, f.line), page, f.line-1, versionTokens.Replace(f.text))
			c.fences = append(c.fences, s)
		}
	}

	seen := map[string]bool{}
	for _, ref := range c.snippets {
		ext := path.Ext(ref.target)
		if (ext != ".yaml" && ext != ".yml") || ref.lines != "" || strings.HasPrefix(ref.target, "examples/live/") {
			continue
		}
		name := ref.target
		if ref.section != "" {
			name += ":" + ref.section
		}
		if covered[name] || seen[name] {
			continue
		}
		seen[name] = true
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref.target)))
		if err != nil {
			continue // TestSnippetReferences reports the missing file.
		}
		text, offset := string(data), 0
		if ref.section != "" {
			if text, offset, err = section(text, ref.section); err != nil {
				continue
			}
		}
		c.includes = append(c.includes, decode(name, ref.target, offset, versionTokens.Replace(text)))
	}
	return c
}

// fileSources decodes every YAML file below dir except kustomization files and paths under skip.
func fileSources(t *testing.T, root, dir, skip string) []source {
	t.Helper()
	var out []source
	for _, rel := range walk(t, root, dir, ".yaml", ".yml") {
		base := path.Base(rel)
		if (skip != "" && strings.HasPrefix(rel, skip)) || base == "kustomization.yaml" || base == "kustomization.yml" {
			continue
		}
		out = append(out, decode(rel, rel, 0, readFile(t, root, rel)))
	}
	return out
}

// decode splits text into YAML documents and keeps those with apiVersion or kind; offset is the line number of the
// text's first line in file minus one. A document with neither key is not a manifest, a document with one is broken.
func decode(name, file string, offset int, text string) source {
	s := source{name: name}
	lines := strings.Split(text, "\n")
	start := 0
	flush := func(end int) {
		chunk := strings.Join(lines[start:end], "\n")
		first := start
		for first < end && isBlankOrComment(lines[first]) {
			first++
		}
		if first == end {
			return
		}
		at := fmt.Sprintf("%s:%d", file, offset+first+1)
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(chunk), &obj); err != nil {
			s.errs = append(s.errs, fmt.Sprintf("%s: invalid YAML: %v", at, err))
			return
		}
		_, hasAPIVersion := obj["apiVersion"]
		_, hasKind := obj["kind"]
		switch {
		case !hasAPIVersion && !hasKind:
		case !hasAPIVersion || !hasKind:
			s.errs = append(s.errs, fmt.Sprintf("%s: a document needs both apiVersion and kind", at))
		default:
			s.manifests = append(s.manifests, manifest{file: file, line: offset + first + 1, obj: obj})
		}
	}
	for i, l := range lines {
		if docSeparator.MatchString(l) {
			flush(i)
			start = i + 1
		}
	}
	flush(len(lines))
	return s
}

func isBlankOrComment(l string) bool {
	l = strings.TrimSpace(l)
	return l == "" || strings.HasPrefix(l, "#")
}

// snippetRefs returns the --8<-- includes of text in the inline and the block form.
func snippetRefs(file, text string) []snippetRef {
	var out []snippetRef
	inBlock := false
	for i, l := range strings.Split(text, "\n") {
		if blockLine.MatchString(l) {
			inBlock = !inBlock
			continue
		}
		var target string
		switch {
		case inBlock:
			target = strings.TrimSpace(l)
			if target == "" || strings.HasPrefix(target, ";") {
				continue
			}
		default:
			m := includeLine.FindStringSubmatch(l)
			if m == nil || m[1] != "" {
				continue
			}
			target = m[2][1 : len(m[2])-1]
		}
		out = append(out, parseRef(file, i+1, target))
	}
	return out
}

func parseRef(from string, line int, target string) snippetRef {
	ref := snippetRef{from: from, line: line, target: target}
	if strings.Contains(target, "://") {
		return ref
	}
	if m := sectionSuffix.FindStringSubmatch(target); m != nil {
		ref.target, ref.section = m[1], m[2]
	} else if m := linesSuffix.FindStringSubmatch(target); m != nil && m[2] != "" {
		ref.target, ref.lines = m[1], m[2]
	}
	return ref
}

// section returns the lines between the start and end markers of name, without the marker lines, and the number of
// lines before the first returned line.
func section(text, name string) (string, int, error) {
	lines := strings.Split(text, "\n")
	start, end := -1, -1
	for i, l := range lines {
		m := sectionMarker.FindStringSubmatch(l)
		if m == nil || !strings.EqualFold(m[2], name) {
			continue
		}
		if strings.EqualFold(m[1], "start") && start < 0 {
			start = i
		} else if strings.EqualFold(m[1], "end") && start >= 0 && end < 0 {
			end = i
		}
	}
	switch {
	case start < 0:
		return "", 0, fmt.Errorf("no [start:%s] marker", name)
	case end < 0:
		return "", 0, fmt.Errorf("no [end:%s] marker after [start:%s]", name, name)
	}
	var kept []string
	for _, l := range lines[start+1 : end] {
		if !sectionMarker.MatchString(l) {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n"), start + 1, nil
}

// checkLines checks a pymdown line selection such as :3:9 or :1:4,8:10 against a file of n lines.
func checkLines(spec string, n int) error {
	for part := range strings.SplitSeq(strings.TrimPrefix(spec, ":"), ",") {
		for bound := range strings.SplitSeq(strings.TrimPrefix(part, ":"), ":") {
			if bound == "" {
				continue
			}
			v, err := strconv.Atoi(bound)
			if err != nil || v < 1 || v > n {
				return fmt.Errorf("line %q outside 1..%d", bound, n)
			}
		}
	}
	return nil
}

// yamlFences returns the fenced yaml blocks of a markdown page, including fences indented inside admonitions or tabs.
func yamlFences(file, text string) []fence {
	var out []fence
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent, marker, lang := m[1], m[2], strings.ToLower(m[3])
		closing := regexp.MustCompile("^[ \t]*" + regexp.QuoteMeta(marker[:1]) + "{" + strconv.Itoa(len(marker)) + ",}[ \t]*$")
		var body []string
		j := i + 1
		for ; j < len(lines) && !closing.MatchString(lines[j]); j++ {
			if includeLine.MatchString(lines[j]) {
				body = append(body, "")
				continue
			}
			body = append(body, strings.TrimPrefix(lines[j], indent))
		}
		if lang == "yaml" || lang == "yml" {
			out = append(out, fence{file: file, line: i + 2, text: strings.Join(body, "\n")})
		}
		i = j
	}
	return out
}

func walk(t *testing.T, root, dir string, exts ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		for _, ext := range exts {
			if strings.HasSuffix(p, ext) {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				out = append(out, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
