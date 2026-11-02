---
name: artifact
description: Make an HTML page, report, chart, diagram, dashboard or mockup the user will open in a browser (an "artifact"). In bx it is saved in this session's artifacts folder and served over Tailscale, never uploaded to claude.ai.
---

# Artifacts in bx

This session runs under bx. An artifact here is a local HTML file, not a claude.ai page; the
Artifact tool is turned off.

1. Load the `artifact-design` skill and follow its design guidance: tokens, both themes,
   phone width, typography, copy, charts drawn to scale.
2. Ignore what it says about publishing and about the claude.ai viewer. Instead:
   - Write a complete document: `<!doctype html>`, `<html lang="en">`, a `<head>` with
     `<meta charset="utf-8">` and
     `<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
     the `<title>`, then `<style>`, then `<body>`.
   - Add the reset the claude.ai skeleton would have added:
     `body{margin:0;font:14px/1.5 system-ui,sans-serif}`, `img{max-width:100%}`,
     `[hidden]{display:none!important}`, and pad `:root` top and bottom with
     `env(safe-area-inset-top, 0px)` and `env(safe-area-inset-bottom, 0px)`.
   - Mermaid is not rendered for you: load it from cdnjs at an exact version and call
     `mermaid.initialize({ startOnLoad: true })`.
   - There is no `window.claude`, no runtime capabilities, no ArtifactData or ArtifactComments
     and no preview tool. Don't use or mention them, and skip the artifact-capabilities skill.
   - The page is served with `Content-Security-Policy: sandbox allow-scripts …`, so it runs in
     an opaque origin: `localStorage` throws (keep the try/catch the design skill asks for).
3. Save the page in this session's artifacts folder, named in your system prompt (also in
   `$BX_ARTIFACTS`). Use a short kebab-case file name ending in `.html`. Put extra files
   (images, data, scripts) next to it and reference them by relative path. Never write the page
   into the repository unless the user asks to add it to the project.
4. To change a page, edit the same file; the user reloads the link.
5. Reply in one line with the link from your system prompt plus the file name. If there is no
   link, give the file's path.
