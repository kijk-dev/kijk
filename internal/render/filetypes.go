package render

import (
	"path"
	"strings"
	"unicode/utf8"
)

// FileTypes are the files kijk shows other than as colored source. The page gets this list from
// /api/filetypes and opens each file by it. A file of no type shows as source.
var FileTypes = []FileType{
	{Name: "png", Exts: []string{".png"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/png"},
	{Name: "jpeg", Exts: []string{".jpg", ".jpeg"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/jpeg"},
	{Name: "gif", Exts: []string{".gif"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/gif"},
	{Name: "webp", Exts: []string{".webp"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/webp"},
	{Name: "ico", Exts: []string{".ico"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/x-icon"},
	{Name: "bmp", Exts: []string{".bmp"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/bmp"},
	{Name: "avif", Exts: []string{".avif"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/avif"},
	{Name: "svg", Exts: []string{".svg"}, Show: ShowImage, Diff: DiffPictures, MIME: "image/svg+xml", Text: true},
	{Name: "markdown", Exts: []string{".md", ".markdown"}, Show: ShowRendered, Diff: DiffLines, MaxBytes: MaxSourceBytes,
		Render: Markdown},
	{Name: "asciidoc", Exts: []string{".adoc", ".asciidoc"}, Show: ShowRendered, Diff: DiffLines, MaxBytes: MaxSourceBytes,
		Render: AsciiDoc},
	{Name: "mermaid", Exts: []string{".mmd", ".mermaid"}, Fences: []string{"mermaid"}, Show: ShowDrawn, Diff: DiffPictures,
		MaxBytes: 4 * diagramMaxChars, MaxChars: diagramMaxChars, Dark: true,
		Engine: &Engine{Label: "Mermaid", Files: []Download{
			{"mermaid", "12.2.0", "https://cdn.jsdelivr.net/npm/mermaid@12.2.0/dist/mermaid.min.js",
				"a8a6a6f805b074a8261a2825c86f99c8054acb3b35f50bb7e24a4f9f2c3c3b9f"},
		}}},
	{Name: "plantuml", Exts: []string{".puml", ".plantuml", ".pu", ".wsd"}, Fences: []string{"plantuml", "puml"}, Show: ShowDrawn,
		Diff: DiffPictures, MaxBytes: 4 * diagramMaxChars, MaxChars: diagramMaxChars,
		Engine: &Engine{Label: "PlantUML", Includes: true, Files: []Download{
			{"plantuml", "1.2026.9", "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/plantuml.js",
				"21c049ce47d087eff61ae0836f9c62a2b0c0d2528a5f16c2247057544228fae7"},
			{"graphviz", "1.2026.9", "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/viz-global.js", // its layout engine
				"fc6ca2de83dd4e3fca967d5214122777890a5cd1ab101115a1f716fc84e6dc5e"},
			{"plantuml-themes", "1.2026.9", "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/themes.js",
				"d6f508704b276b66631cfff05291de3013ab7807972b7cb2b77a14d4beab2c2d"},
		}}},
	// Larger than the other diagrams, as pasted images live inside the drawing.
	{Name: "excalidraw", Exts: []string{".excalidraw"}, Show: ShowDrawn, Lexer: "json", Diff: DiffPictures, MaxBytes: 8 << 20, Dark: true,
		Engine: &Engine{Label: "Excalidraw", Files: []Download{
			// Excalidraw's export, without React; its fonts make it 14 MB.
			{"excalidraw", "0.1.6", "https://cdn.jsdelivr.net/npm/@excalidraw/utils@0.1.6/dist/prod/index.js",
				"70096399885cd5cb6b0ea9a2716e0a0413cfa15ed59cfd2bb50b42ec2b30c29c"},
		}}},
}

// diagramMaxChars is Mermaid's own default limit; kijk holds PlantUML to it too. A character takes
// at most 4 bytes, so MaxBytes is 4 times it.
const diagramMaxChars = 50000

// A FileType is how kijk shows one type of file.
type FileType struct {
	Name   string   `json:"name"`             // also the class of its diagram blocks, and its engine's file in web/engines
	Exts   []string `json:"exts,omitempty"`   // in lower case, with the dot; a file matches in any case
	Names  []string `json:"names,omitempty"`  // exact file names, such as Dockerfile
	Fences []string `json:"fences,omitempty"` // the names of its blocks inside markdown and AsciiDoc
	Show   Show     `json:"show"`
	Lexer  string   `json:"lexer,omitempty"` // chroma's lexer for the source view, where chroma's own match is wrong
	Diff   Diff     `json:"diff"`
	// MaxBytes is the largest file it renders or draws; a larger one shows as source.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	MaxChars int   `json:"maxChars,omitempty"` // the most characters it draws
	// Dark draws it in kijk's colors, also the dark ones. Without it, it keeps its author's colors on a light card.
	Dark bool `json:"dark,omitempty"`
	// MIME is an image's content type. It comes from the extension, never from the bytes: an SVG is
	// text, and a type guessed from the bytes is what nosniff exists to stop.
	MIME   string   `json:"mime,omitempty"`
	Text   bool     `json:"text,omitempty"` // an image that is text, so it has a source view and a line diff too
	Render Renderer `json:"-"`              // the HTML of a rendered type
	Engine *Engine  `json:"engine,omitempty"`
}

// Show is how a file of a type opens.
type Show string

const (
	ShowImage    Show = "image"
	ShowRendered Show = "rendered" // as the HTML of its Render, with a switch to the source
	ShowDrawn    Show = "drawn"    // as the picture its engine draws, with a switch to the source
)

// Diff is how a change to a file of a type shows.
type Diff string

const (
	DiffLines    Diff = "lines"
	DiffPictures Diff = "pictures" // before and after side by side; a type with a source view can switch to the lines
)

// A Renderer makes the HTML of a file. path is the file's path from the folder root, and read
// reads another file there. includes are the files it read, so the view redraws when one changes.
type Renderer func(src []byte, path string, read func(path string) ([]byte, error)) (html string, includes []string, err error)

// An Engine draws a type in the browser: web/engines/<type name>.js loads the files and draws.
// kijk downloads them on first use rather than embedding them, so kijk does not grow by their size.
type Engine struct {
	Label string `json:"label"`
	// Includes says a drawing reads other files, so it holds only for its file and until the file opens again.
	Includes bool       `json:"includes,omitempty"`
	Files    []Download `json:"-"`
}

// A Download is one file of an engine. The SHA-256 makes the source irrelevant.
type Download struct {
	Name, Version, URL, SHA256 string
}

// TooLarge tells whether src is more than the type renders or draws.
func (t *FileType) TooLarge(src []byte) bool {
	return int64(len(src)) > t.MaxBytes || (t.MaxChars > 0 && utf8.RuneCount(src) > t.MaxChars)
}

// The lookups are filled in init rather than by their declaration: a Render in the list uses
// FenceType, and Go would see an initialization cycle.
var byExt, byName, byFence = map[string]*FileType{}, map[string]*FileType{}, map[string]*FileType{}

func init() {
	for i := range FileTypes {
		t := &FileTypes[i]
		for _, e := range t.Exts {
			byExt[e] = t
		}
		for _, n := range t.Names {
			byName[n] = t
		}
		for _, f := range t.Fences {
			byFence[f] = t
		}
	}
}

// TypeOf returns the type of a slash-separated path, or nil. An exact name wins over an extension.
func TypeOf(p string) *FileType {
	if t := byName[path.Base(p)]; t != nil {
		return t
	}
	return byExt[strings.ToLower(path.Ext(p))]
}

// FenceType returns the type of a block in markdown or AsciiDoc, or nil.
func FenceType(name string) *FileType {
	return byFence[name]
}
