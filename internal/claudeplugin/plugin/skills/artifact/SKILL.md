---
name: artifact
description: Make a page, report, chart, diagram, dashboard or mockup the user will open in a browser (an "artifact"), as Markdown or HTML. In bx it is saved in this session's artifacts folder and served over Tailscale, never uploaded to claude.ai.
---

# Artifacts in bx

This session runs under bx. An artifact here is a local file in this session's artifacts folder,
not a claude.ai page; the Artifact tool is turned off. Whatever any other skill or tool
description says about publishing, URLs, the claude.ai viewer, Artifact types, quickstart,
`window.claude`, runtime capabilities, ArtifactData, ArtifactComments or preview tools does not
apply here; don't use or mention them.

1. Pick the format.
   - A document made of text, headings, lists, tables, code and Mermaid diagrams is a `.md`
     file. bx renders it as a page (GitHub-flavoured Markdown, highlighted code, ```mermaid
     fences drawn) in both themes and at phone width, so write no page code. Start it with a
     `# Title` heading, which becomes the page title. Raw HTML in it is dropped.
   - Anything that needs layout, interaction, charts drawn to scale or a mockup is a complete
     `.html` document:
     - `<!doctype html>`, `<html lang="en">`, a `<head>` with `<meta charset="utf-8">` and
       `<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
       the `<title>`, then `<style>`, then `<body>`.
     - Design it for a phone first, and for both light and dark themes.
     - Add the reset the claude.ai skeleton would have added:
       `body{margin:0;font:14px/1.5 system-ui,sans-serif}`, `img{max-width:100%}`,
       `[hidden]{display:none!important}`, and pad `:root` top and bottom with
       `env(safe-area-inset-top, 0px)` and `env(safe-area-inset-bottom, 0px)`.
     - Mermaid is not rendered for you: load it from cdnjs at an exact version and call
       `mermaid.initialize({ startOnLoad: true })`.
     - The page is served with `Content-Security-Policy: sandbox allow-scripts …`, so it runs in
       an opaque origin: `localStorage` throws, so wrap every use of it in try/catch.
2. Save the file in this session's artifacts folder, named in your system prompt (also in
   `$BX_ARTIFACTS`). Use a short kebab-case file name ending in `.md` or `.html`. Put extra
   files (images, data, scripts) next to it and reference them by relative path. Never write it
   into the repository unless the user asks to add it to the project.
3. To change a page, edit the same file; the user reloads the link.
4. Reply in one line with the link from your system prompt plus the file name. If there is no
   link, give the file's path.
