package serve

import (
	"bytes"
	"html"
	"html/template"
	"path/filepath"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// maxMarkdown is the largest .md file rendered as a page; a larger one is served as text.
const maxMarkdown = 4 << 20

// isMarkdown reports whether name is a Markdown file.
func isMarkdown(name string) bool { return strings.EqualFold(filepath.Ext(name), ".md") }

// markdown converts GitHub-flavoured Markdown: tables, task lists, strikethrough, autolinks.
// Raw HTML and javascript: links are left out (goldmark's default, without html.WithUnsafe),
// code is coloured by CSS class, and mermaid fences become <pre class="mermaid">.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM, highlighting.NewHighlighting(
		highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
	)),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(mermaidTransformer{}, 100)),
	),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(mermaidRenderer{}, 100))),
)

// codeCSS colours highlighted code: chroma's github style, github-dark in the dark theme.
var codeCSS = func() template.CSS {
	f := chromahtml.New(chromahtml.WithClasses(true))
	var light, dark bytes.Buffer
	_ = f.WriteCSS(&light, styles.Get("github"))
	_ = f.WriteCSS(&dark, styles.Get("github-dark"))
	return template.CSS(light.String() + "@media (prefers-color-scheme: dark){" + dark.String() + "}")
}()

// renderMarkdown is the page of Markdown file name: its body in mdPage.
func renderMarkdown(src []byte, name string) ([]byte, error) {
	doc := markdown.Parser().Parse(text.NewReader(src))
	var body bytes.Buffer
	if err := markdown.Renderer().Render(&body, src, doc); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err := mdPage.Execute(&out, mdPageData{
		Title: mdTitle(doc, src, name), Name: name, Body: template.HTML(body.String()), CodeCSS: codeCSS,
	})
	return out.Bytes(), err
}

// mdTitle is the text of the first level-1 heading, or the file name without .md.
func mdTitle(doc ast.Node, src []byte, name string) string {
	var b strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering || h.Level != 1 {
			return ast.WalkContinue, nil
		}
		_ = ast.Walk(h, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
			if t, ok := c.(*ast.Text); ok && entering {
				b.Write(t.Segment.Value(src))
			}
			return ast.WalkContinue, nil
		})
		return ast.WalkStop, nil
	})
	if s := strings.TrimSpace(b.String()); s != "" {
		return s
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// kindMermaid is a mermaid fence, taken out of the code highlighter's way.
var kindMermaid = ast.NewNodeKind("Mermaid")

type mermaidBlock struct {
	ast.BaseBlock
	code []byte
}

func (n *mermaidBlock) Kind() ast.NodeKind         { return kindMermaid }
func (n *mermaidBlock) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

// mermaidTransformer swaps each ```mermaid fence for a mermaidBlock.
type mermaidTransformer struct{}

func (mermaidTransformer) Transform(doc *ast.Document, r text.Reader, _ parser.Context) {
	src := r.Source()
	var fences []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if f, ok := n.(*ast.FencedCodeBlock); ok && entering && string(f.Language(src)) == "mermaid" {
			fences = append(fences, f)
		}
		return ast.WalkContinue, nil
	})
	for _, f := range fences {
		var code bytes.Buffer
		for i := 0; i < f.Lines().Len(); i++ {
			s := f.Lines().At(i)
			code.Write(s.Value(src))
		}
		f.Parent().ReplaceChild(f.Parent(), f, &mermaidBlock{code: code.Bytes()})
	}
}

// mermaidRenderer writes a mermaidBlock as <pre class="mermaid">, which mdPage's script draws.
type mermaidRenderer struct{}

func (mermaidRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaid, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString(`<pre class="mermaid">` + html.EscapeString(string(n.(*mermaidBlock).code)) + "</pre>\n")
		}
		return ast.WalkSkipChildren, nil
	})
}

type mdPageData struct {
	Title, Name string
	Body        template.HTML
	CodeCSS     template.CSS
}

// mdPage is the page around a rendered doc: both themes, phone width, readable line length,
// tables that scroll sideways, and mermaid from cdnjs only when the doc has a diagram.
var mdPage = template.Must(template.New("md").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>{{.Title}}</title>
<style>
:root{--bg:#fff;--fg:#1f2328;--muted:#59636e;--line:#d1d9e0;--soft:#f6f8fa;--link:#0969da;color-scheme:light dark}
@media (prefers-color-scheme: dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#9198a1;--line:#3d444d;--soft:#151b23;--link:#4493f8}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,-apple-system,sans-serif;-webkit-text-size-adjust:100%}
main{max-width:46rem;margin:0 auto;padding:calc(16px + env(safe-area-inset-top,0px)) 16px calc(48px + env(safe-area-inset-bottom,0px))}
nav{display:flex;justify-content:flex-end;font-size:13px}
a{color:var(--link)}
h1,h2,h3,h4{line-height:1.25;margin:1.6em 0 .6em}
h1{font-size:1.9em;margin-top:.4em}
h2{font-size:1.4em;padding-bottom:.3em;border-bottom:1px solid var(--line)}
img{max-width:100%}
code{font:.875em/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--soft);padding:.15em .35em;border-radius:4px}
pre{background:var(--soft);padding:12px 14px;border-radius:6px;overflow-x:auto}
pre code{background:none;padding:0}
pre.mermaid{background:none;text-align:center}
blockquote{margin:0;padding:0 1em;color:var(--muted);border-left:3px solid var(--line)}
table{display:block;max-width:100%;overflow-x:auto;border-collapse:collapse}
th,td{border:1px solid var(--line);padding:6px 12px;text-align:left;vertical-align:top}
th{background:var(--soft)}
hr{border:0;border-top:1px solid var(--line);margin:2em 0}
{{.CodeCSS}}
</style>
</head>
<body>
<main>
<nav><a href="{{.Name}}?raw">source</a></nav>
{{.Body}}
</main>
<script>
if (document.querySelector('pre.mermaid')) {
  const s = document.createElement('script');
  s.src = 'https://cdnjs.cloudflare.com/ajax/libs/mermaid/11.4.1/mermaid.min.js';
  s.onload = () => {
    mermaid.initialize({ startOnLoad: false, theme: matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'default' });
    mermaid.run();
  };
  document.head.append(s);
}
</script>
</body>
</html>
`))
