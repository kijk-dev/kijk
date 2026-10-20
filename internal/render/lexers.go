package render

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// chroma has no lexer for these three formats. These color only the obvious parts, with token
// types that classFor maps. Words go in one token, so a long line is not lexed a character at a time.

var _ = lexers.Register(chroma.MustNewLexer(&chroma.Config{
	Name:      "AsciiDoc",
	Aliases:   []string{"asciidoc", "adoc"},
	Filenames: []string{"*.adoc", "*.asciidoc"},
	MimeTypes: []string{"text/asciidoc"},
}, func() chroma.Rules {
	return chroma.Rules{
		"root": {
			{Pattern: `^/{4,}$`, Type: chroma.Comment, Mutator: chroma.Push("comment")},
			{Pattern: `^//[^\n]*`, Type: chroma.Comment},
			{Pattern: "^(-{4,}|\\.{4,}|={4,}|\\*{4,}|_{4,}|\\+{4,}|--|\\|={3,}|```[^\\n]*)$", Type: chroma.KeywordType},
			{Pattern: `^(={1,6}|#{1,6})[ \t]+[^\n]*`, Type: chroma.GenericHeading},
			{Pattern: `^\.[^\s.][^\n]*`, Type: chroma.GenericSubheading},
			{Pattern: `^\[[^\n]*\]$`, Type: chroma.NameAttribute},
			{Pattern: `^(:!?\w[\w-]*!?:)([^\n]*)`, Type: chroma.ByGroups(chroma.NameAttribute, chroma.LiteralString)},
			{Pattern: `^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):`, Type: chroma.NameBuiltin},
			{Pattern: `^(ifn?def|ifeval|endif)::[^\n]*`, Type: chroma.NameFunction},
			{Pattern: `\b(image|include|xref|link|mailto|footnote|kbd|btn|menu|pass|toc)::?`, Type: chroma.NameFunction},
			{Pattern: `<<[^<>\n]*>>`, Type: chroma.NameFunction},
			{Pattern: `https?://[^\s\[\]<>]+`, Type: chroma.LiteralString},
			{Pattern: `\w+|\s+|.`, Type: chroma.Text},
		},
		"comment": {
			{Pattern: `^/{4,}$`, Type: chroma.Comment, Mutator: chroma.Pop(1)},
			{Pattern: `[^\n]+|\n`, Type: chroma.Comment},
		},
	}
}))

var _ = lexers.Register(chroma.MustNewLexer(&chroma.Config{
	Name:      "PlantUML",
	Aliases:   []string{"plantuml", "puml"},
	Filenames: []string{"*.puml", "*.plantuml", "*.pu", "*.wsd", "*.iuml"},
	MimeTypes: []string{"text/x-plantuml"},
}, func() chroma.Rules {
	return chroma.Rules{
		"root": {
			{Pattern: `^[ \t]*@(start|end)\w*`, Type: chroma.NameDecorator},
			{Pattern: `^[ \t]*'[^\n]*`, Type: chroma.Comment},
			{Pattern: `/'`, Type: chroma.Comment, Mutator: chroma.Push("comment")},
			{Pattern: `^[ \t]*![\w$]+`, Type: chroma.NameBuiltin},
			{Pattern: `"[^"\n]*"`, Type: chroma.LiteralString},
			{Pattern: `#[0-9a-fA-F]{3,8}\b|#[A-Za-z]\w*`, Type: chroma.LiteralNumberHex},
			{Pattern: `<<?\|?[-.=]+(\[[^\]\n]*\])?[-.=]*>?>?|[-.=]+(\[[^\]\n]*\])?[-.=]*\|?>>?|[-.=]{2,}`, Type: chroma.NameFunction},
			{Pattern: `\b(participant|actor|boundary|control|entity|database|collections|queue|class|interface|abstract|enum|` +
				`annotation|package|namespace|node|folder|frame|cloud|component|usecase|rectangle|state|object|map|json|` +
				`note|end|as|of|over|left|right|top|bottom|activate|deactivate|destroy|create|alt|else|opt|loop|par|break|` +
				`critical|group|ref|skinparam|title|header|footer|legend|caption|start|stop|if|then|elseif|endif|while|` +
				`endwhile|repeat|fork|partition|return|autonumber|hide|show|newpage|box|extends|implements)\b`, Type: chroma.Keyword},
			{Pattern: `\w+|\s+|.`, Type: chroma.Text},
		},
		"comment": {
			{Pattern: `'/`, Type: chroma.Comment, Mutator: chroma.Pop(1)},
			{Pattern: `[^']+|'`, Type: chroma.Comment},
		},
	}
}))

var _ = lexers.Register(chroma.MustNewLexer(&chroma.Config{
	Name:      "Mermaid",
	Aliases:   []string{"mermaid"},
	Filenames: []string{"*.mmd", "*.mermaid"},
	MimeTypes: []string{"text/x-mermaid"},
}, func() chroma.Rules {
	return chroma.Rules{
		"root": {
			{Pattern: `%%[^\n]*`, Type: chroma.Comment},
			{Pattern: `^[ \t]*(graph|flowchart|sequenceDiagram|classDiagram(-v2)?|stateDiagram(-v2)?|erDiagram|journey|gantt|pie|` +
				`quadrantChart|requirementDiagram|gitGraph|mindmap|timeline|sankey-beta|xychart-beta|block-beta|packet-beta|` +
				`kanban|architecture-beta|C4Context|C4Container|C4Component|C4Dynamic|C4Deployment|zenuml|radar-beta|treemap-beta)\b`, Type: chroma.KeywordType},
			{Pattern: `\b(TB|TD|BT|RL|LR)\b`, Type: chroma.NameBuiltin},
			{Pattern: `\b(subgraph|end|participant|actor|loop|alt|else|opt|par|and|rect|note|over|left of|right of|activate|` +
				`deactivate|class|classDef|style|linkStyle|click|direction|section|title|state|as|autonumber|critical|break|` +
				`accTitle|accDescr|dateFormat|axisFormat)\b`, Type: chroma.Keyword},
			{Pattern: `"[^"\n]*"`, Type: chroma.LiteralString},
			{Pattern: `<<?[-=.]+(>>?|x\b|o\b|\))?|[-=.]*[-=](>>?|x\b|o\b|\))|[-=]{2,}[-=.]*|-\.+-`, Type: chroma.NameFunction},
			{Pattern: `\w+|\s+|.`, Type: chroma.Text},
		},
	}
}))
