package main

// The interface is built for translation (D44), and these tests are what keep
// it that way.
//
// The rules are cheap to follow while writing a screen and expensive to
// retrofit across a finished product, which is exactly the kind of rule that
// gets skipped unless something fails loudly. So each one is a test:
//
//   - no user-facing text typed into markup or into the renderer
//   - no key used that the catalogue does not define, and none defined that
//     nothing uses
//   - every language carries the same keys, so a half-finished translation
//     cannot ship
//   - no hard-coded left or right, so Persian flips the layout on its own
//
// None of this ships Persian. It makes shipping Persian a file and a switch.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const uiDir = "ui"

// keyShape is what a string literal has to look like to be treated as a
// lookup key: lowercase words joined by dots. It deliberately does not match
// an API path ("/api/rooms/join"), a media type ("application/json") or an
// element id ("p-online"), so scanning the renderer for keys needs no parser.
var keyShape = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// catalogue loads one language file.
func catalogue(t *testing.T, lang string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(read(t, filepath.Join(uiDir, "strings", lang+".json"))), &m); err != nil {
		t.Fatalf("strings/%s.json is not valid JSON: %v", lang, err)
	}
	return m
}

func languages(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(uiDir, "strings", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no language files under %s/strings", uiDir)
	}
	var out []string
	for _, p := range paths {
		out = append(out, strings.TrimSuffix(filepath.Base(p), ".json"))
	}
	sort.Strings(out)
	return out
}

// --- what the interface asks for ----------------------------------------

var (
	dataT   = regexp.MustCompile(`data-t(?:-[a-z-]+)?="([^"]*)"`)
	jsQuote = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
)

// literals pulls every quoted string out of a source file, one line at a time.
//
// Line by line on purpose: a JavaScript string never spans a newline, and
// scanning the whole file at once lets the closing quote of one string pair
// with the opening quote of the next, which turns half the file into one
// enormous fictional literal.
func literals(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		for _, m := range jsQuote.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1]+m[2])
		}
	}
	return out
}

// keysUsed collects every key the markup and the renderer ask for.
func keysUsed(t *testing.T) map[string][]string {
	t.Helper()
	used := map[string][]string{}

	html := read(t, filepath.Join(uiDir, "index.html"))
	for _, m := range dataT.FindAllStringSubmatch(html, -1) {
		used[m[1]] = append(used[m[1]], "index.html")
	}

	// Comments are stripped first: this file's own header shows t("some.key")
	// as an example, and an example is not a use.
	for _, lit := range literals(renderSource(t)) {
		if keyShape.MatchString(lit) {
			used[lit] = append(used[lit], "app.js")
		}
	}
	return used
}

func TestEveryKeyTheInterfaceAsksForExists(t *testing.T) {
	en := catalogue(t, "en")
	for key, where := range keysUsed(t) {
		if _, ok := en[key]; !ok {
			t.Errorf("%s asks for %q, which strings/en.json does not define", strings.Join(where, ", "), key)
		}
	}
}

// A key nothing uses is a translator's wasted afternoon, and worse, it hides
// the one that is actually missing. Keys beginning with an underscore are
// notes to translators rather than text.
func TestTheCatalogueCarriesNothingUnused(t *testing.T) {
	used := keysUsed(t)
	for key := range catalogue(t, "en") {
		if strings.HasPrefix(key, "_") {
			continue
		}
		if _, ok := used[key]; !ok {
			t.Errorf("strings/en.json defines %q, which nothing uses", key)
		}
	}
}

func TestEveryLanguageCarriesTheSameKeys(t *testing.T) {
	langs := languages(t)
	en := catalogue(t, "en")
	for _, lang := range langs {
		if lang == "en" {
			continue
		}
		other := catalogue(t, lang)
		for key := range en {
			if _, ok := other[key]; !ok {
				t.Errorf("strings/%s.json is missing %q", lang, key)
			}
		}
		for key := range other {
			if _, ok := en[key]; !ok {
				t.Errorf("strings/%s.json defines %q, which English does not", lang, key)
			}
		}
	}
}

// A placeholder that does not survive translation produces a sentence with a
// hole in it, and the hole is only visible in the language nobody here reads.
func TestPlaceholdersSurviveTranslation(t *testing.T) {
	braces := regexp.MustCompile(`\{[a-z_]+\}`)
	en := catalogue(t, "en")
	for _, lang := range languages(t) {
		if lang == "en" {
			continue
		}
		for key, text := range catalogue(t, lang) {
			want := braces.FindAllString(en[key], -1)
			got := braces.FindAllString(text, -1)
			sort.Strings(want)
			sort.Strings(got)
			if strings.Join(want, ",") != strings.Join(got, ",") {
				t.Errorf("strings/%s.json %q has placeholders %v, English has %v", lang, key, got, want)
			}
		}
	}
}

// --- no text typed into the markup --------------------------------------

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlScript  = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<title[^>]*>.*?</title>`)
	htmlTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	// An element marked aria-hidden is decoration, not content: a screen
	// reader is told to skip it, and a translator would have nothing to do
	// with it either. That is what the toolbar glyphs are.
	decoration = regexp.MustCompile(`(?s)<span[^>]*aria-hidden="true"[^>]*>[^<]*</span>`)
	textAttr   = regexp.MustCompile(`(?:^|\s)(placeholder|title|alt|aria-label)="([^"]*)"`)
)

// Text between tags is text a translator will never see, because it is not in
// the catalogue and nothing looks for it there. The only way to notice is to
// look, so this looks.
func TestNoUserFacingTextIsTypedIntoTheMarkup(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))
	stripped := htmlTag.ReplaceAllString(
		decoration.ReplaceAllString(
			htmlScript.ReplaceAllString(htmlComment.ReplaceAllString(html, ""), ""), ""), "")

	for _, line := range strings.Split(stripped, "\n") {
		if strings.TrimSpace(line) != "" {
			t.Errorf("index.html has text typed into it: %q - give it a key and a data-t attribute", strings.TrimSpace(line))
		}
	}
}

// The same for attributes players read. data-t-placeholder fills these at
// load; a literal one is text nobody can translate.
func TestNoUserFacingTextIsTypedIntoAttributes(t *testing.T) {
	html := htmlComment.ReplaceAllString(read(t, filepath.Join(uiDir, "index.html")), "")
	for _, m := range textAttr.FindAllStringSubmatch(html, -1) {
		t.Errorf("index.html sets %s=%q directly - use data-t-%s with a key instead", m[1], m[2], m[1])
	}
}

// --- no text typed into the renderer ------------------------------------

var (
	jsLineComment = regexp.MustCompile(`(?m)^\s*//.*$`)
	jsBlock       = regexp.MustCompile("(?s)`[^`]*`")
	jsSubst       = regexp.MustCompile(`(?s)\$\{[^{}]*\}`)
	writesText    = regexp.MustCompile(`\.(textContent|innerText|title|placeholder|ariaLabel)\s*=\s*(.+)$`)
	hasUpperWord  = regexp.MustCompile(`[A-Z][a-z]`)
	hasLetter     = regexp.MustCompile(`[A-Za-z]`)
)

// renderSource is app.js with its comments removed.
//
// i18n.js is deliberately not scanned: it is the machinery these rules are
// about, and the only strings in it are a console warning and a thrown error,
// which are read by whoever is debugging rather than by a player.
func renderSource(t *testing.T) string {
	t.Helper()
	return jsLineComment.ReplaceAllString(read(t, filepath.Join(uiDir, "app.js")), "")
}

// splitJS separates a renderer into the code it runs and the text it prints.
//
// Template literals are how this renderer builds markup, and they nest: a
// ${...} may hold another template literal, which may hold another ${...}. No
// regular expression can see that - pairing backticks across a nested one
// produces nonsense - so this walks the source a character at a time with a
// small stack, which is short enough to read and actually correct.
//
// Quotes and comments are not tracked. Comments are stripped before this is
// called, and no string in the renderer contains a brace or a backtick; if one
// ever does, this test says something strange rather than nothing, which is
// the failure mode to prefer.
func splitJS(src string) (code string, text []string) {
	var out strings.Builder
	stack := []byte{'c'}         // 'c': running code, 't': inside a template
	bufs := []*strings.Builder{} // one per template literal being read

	for i := 0; i < len(src); i++ {
		c := src[i]
		if stack[len(stack)-1] == 't' {
			buf := bufs[len(bufs)-1]
			switch {
			case c == '`':
				text = append(text, buf.String())
				bufs = bufs[:len(bufs)-1]
				stack = stack[:len(stack)-1]
			case c == '$' && i+1 < len(src) && src[i+1] == '{':
				// A substitution stands in for whatever it evaluates to. A
				// space keeps the tag around it intact without pretending to
				// know what the value is.
				buf.WriteByte(' ')
				stack = append(stack, 'c')
				i++
			default:
				buf.WriteByte(c)
			}
			continue
		}

		switch c {
		case '`':
			stack = append(stack, 't')
			bufs = append(bufs, &strings.Builder{})
		case '{':
			stack = append(stack, 'c') // a plain block, so its } pairs here
		case '}':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		default:
			out.WriteByte(c)
		}
	}
	return out.String(), text
}

// A sentence in the renderer never reaches the catalogue. Class names and
// element ids are lowercase and pass; anything with a capitalised word in it
// is prose.
func TestTheRendererWritesNoSentences(t *testing.T) {
	code, _ := splitJS(renderSource(t))
	for _, lit := range literals(code) {
		if strings.Contains(lit, " ") && hasUpperWord.MatchString(lit) {
			t.Errorf("app.js contains the literal %q - move it to strings/en.json and call t()", lit)
		}
	}
}

// The markup those templates build is fine. Loose words between the tags are
// text somebody typed where no translator will find it.
func TestTemplateLiteralsCarryNoText(t *testing.T) {
	_, text := splitJS(renderSource(t))
	for _, chunk := range text {
		if bare := htmlTag.ReplaceAllString(chunk, ""); hasLetter.MatchString(bare) {
			t.Errorf("app.js has text inside a template literal: %q - give it a key", strings.TrimSpace(bare))
		}
	}
}

// Assigning a literal to something a player reads is the most direct way to
// bypass the catalogue, and the easiest to do by accident.
func TestNothingPlayersReadIsAssignedALiteral(t *testing.T) {
	for _, line := range strings.Split(renderSource(t), "\n") {
		m := writesText.FindStringSubmatch(line)
		if m == nil || strings.Contains(m[2], "t(") {
			continue
		}
		for _, q := range jsQuote.FindAllStringSubmatch(m[2], -1) {
			if hasLetter.MatchString(q[1] + q[2]) {
				t.Errorf("app.js: %s - assign t(\"some.key\") rather than a literal", strings.TrimSpace(line))
			}
		}
	}
}

// --- no hard-coded left or right ----------------------------------------

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// physical lists the properties and values that pin a layout to one writing
// direction. Each has a logical equivalent that follows the document's dir
// attribute instead, which is why flipping to Persian later is a switch rather
// than a second stylesheet.
//
// Block-axis properties - top, bottom, margin-block, border-bottom - are
// absent on purpose. Right-to-left flips the inline axis only; the page still
// runs top to bottom, so those are not a hazard and banning them would be
// noise that teaches people to ignore the test.
var physical = []struct{ bad, use string }{
	{"margin-left", "margin-inline-start"},
	{"margin-right", "margin-inline-end"},
	{"padding-left", "padding-inline-start"},
	{"padding-right", "padding-inline-end"},
	{"border-left", "border-inline-start"},
	{"border-right", "border-inline-end"},
	{"text-align: left", "text-align: start"},
	{"text-align:left", "text-align: start"},
	{"text-align: right", "text-align: end"},
	{"text-align:right", "text-align: end"},
	{"float: left", "an inline-axis layout"},
	{"float: right", "an inline-axis layout"},
	{"float:left", "an inline-axis layout"},
	{"float:right", "an inline-axis layout"},
	{"border-top-left-radius", "border-start-start-radius"},
	{"border-top-right-radius", "border-start-end-radius"},
	{"border-bottom-left-radius", "border-end-start-radius"},
	{"border-bottom-right-radius", "border-end-end-radius"},
	{"left:", "inset-inline-start:"},
	{"right:", "inset-inline-end:"},
}

func TestTheLayoutHasNoHardCodedDirection(t *testing.T) {
	for _, name := range []string{"app.css", "index.html", "app.js"} {
		src := cssComment.ReplaceAllString(read(t, filepath.Join(uiDir, name)), "")
		if strings.HasSuffix(name, ".js") {
			src = jsLineComment.ReplaceAllString(src, "")
		}
		for i, line := range strings.Split(src, "\n") {
			for _, p := range physical {
				if strings.Contains(line, p.bad) {
					t.Errorf("%s:%d uses %q - use %s, so the layout flips with dir", name, i+1, p.bad, p.use)
				}
			}
		}
	}
}

// The boot document has to declare both, or the first paint has no direction
// and the page jumps once i18n.js sets them.
func TestTheDocumentDeclaresALanguageAndADirection(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))
	if !strings.Contains(html, "<html lang=") || !strings.Contains(html, "dir=") {
		t.Error("index.html must open with <html lang=... dir=...>")
	}
}

// Only English ships (D44). This is not a limit on the design - it is the
// point of it: the machinery is complete, so adding Persian is one file plus
// one line in i18n.js, and every test above starts guarding it the moment the
// file exists.
func TestOnlyEnglishShips(t *testing.T) {
	if got := languages(t); len(got) != 1 || got[0] != "en" {
		t.Logf("languages shipped: %v", got)
	}
}

// The catalogue has to be inside the binary, not merely beside it.
//
// The installed app serves its interface from an embedded filesystem, and
// go:embed skips whole classes of file quietly. A missing strings file there
// is not a missing word - i18n.js cannot load, nothing draws, and the app
// opens to an empty window. That failure would only appear on an installed
// copy, which is the worst place to find it.
func TestTheCatalogueIsInsideTheBinary(t *testing.T) {
	for _, lang := range languages(t) {
		if _, err := uiFiles.ReadFile("ui/strings/" + lang + ".json"); err != nil {
			t.Errorf("ui/strings/%s.json is not embedded: %v", lang, err)
		}
	}
	for _, name := range []string{"ui/index.html", "ui/app.js", "ui/i18n.js", "ui/app.css"} {
		if _, err := uiFiles.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}

// --- the renderer and the markup have to agree --------------------------

var (
	byID     = regexp.MustCompile(`\$\("([a-zA-Z0-9_-]+)"\)`)
	htmlID   = regexp.MustCompile(`\bid="([^"]+)"`)
	byClass  = regexp.MustCompile(`querySelectorAll\("\.([a-zA-Z0-9_-]+)`)
	htmlCls  = regexp.MustCompile(`class="([^"]*)"`)
	htmlData = regexp.MustCompile(`\bdata-([a-z]+)="`)
	jsData   = regexp.MustCompile(`\.dataset\.([a-zA-Z]+)`)

	// What the renderer makes for itself. Both of the tests below ask
	// whether a name the script uses also exists in the markup, and that is
	// the right question only for names the markup is supposed to supply.
	// A class the renderer creates, and an attribute it stamps on a node it
	// created, are answered by the script rather than by the document - so
	// they are gathered from the script and added to what the markup offers.
	//
	// The guard survives: a class or an attribute that is neither in the
	// markup nor made anywhere in the script is still a selector that will
	// never match, which is the failure both tests exist to catch.
	jsClass  = regexp.MustCompile(`(?:className\s*=\s*|classList\.(?:add|remove|toggle)\(|el\("[a-zA-Z0-9]+", )"([^"]*)"`)
	jsWrites = regexp.MustCompile(`\.dataset\.([a-zA-Z]+)\s*(?:=[^=]|\])|delete [a-zA-Z.]+\.dataset\.([a-zA-Z]+)`)
)

// Every $("something") in the renderer must be an id that exists.
//
// This is the failure this interface is most exposed to: it draws by reaching
// into the document by name, so a renamed element does not raise an error, it
// silently stops being filled in - and the first sign is a screen with a blank
// space where the room list should be. That is expensive to notice and cheap
// to prevent.
func TestTheRendererOnlyReachesForElementsThatExist(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))
	ids := map[string]bool{}
	for _, m := range htmlID.FindAllStringSubmatch(html, -1) {
		ids[m[1]] = true
	}
	for _, m := range byID.FindAllStringSubmatch(renderSource(t), -1) {
		if !ids[m[1]] {
			t.Errorf("app.js reaches for #%s, which index.html does not contain", m[1])
		}
	}
}

// The same for the classes it selects on, plus the ones it makes itself.
func TestTheRendererOnlySelectsClassesThatExist(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))
	js := renderSource(t)
	classes := map[string]bool{}
	for _, m := range htmlCls.FindAllStringSubmatch(html, -1) {
		for _, c := range strings.Fields(m[1]) {
			classes[c] = true
		}
	}
	for _, m := range jsClass.FindAllStringSubmatch(js, -1) {
		for _, c := range strings.Fields(m[1]) {
			classes[c] = true
		}
	}
	for _, m := range byClass.FindAllStringSubmatch(js, -1) {
		if !classes[m[1]] {
			t.Errorf("app.js selects .%s, which index.html does not contain", m[1])
		}
	}
}

// A data attribute read in the renderer must be written in the markup.
// element.dataset.screen reads data-screen; a mismatch is undefined at
// runtime and silently does nothing, which is the same trap as a missing id.
func TestTheRendererOnlyReadsDataAttributesThatExist(t *testing.T) {
	html := read(t, filepath.Join(uiDir, "index.html"))
	js := renderSource(t)
	attrs := map[string]bool{}
	for _, m := range htmlData.FindAllStringSubmatch(html, -1) {
		attrs[m[1]] = true
	}
	// The renderer's own bookkeeping: what it last drew (`sig`), which room a
	// row is (`room`), which seat a card is (`seat`), which sentence a strip
	// is showing (`msg`). Each is written by the script onto a node the
	// script created, so the markup never carries it and never should.
	for _, m := range jsWrites.FindAllStringSubmatch(js, -1) {
		for _, name := range m[1:] {
			if name != "" {
				attrs[strings.ToLower(name)] = true
			}
		}
	}
	for _, m := range jsData.FindAllStringSubmatch(js, -1) {
		name := strings.ToLower(m[1])
		if !attrs[name] {
			t.Errorf("app.js reads dataset.%s, so index.html needs a data-%s attribute", m[1], name)
		}
	}
}
