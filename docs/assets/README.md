# docs/assets — rendered blueprint diagrams

These SVGs are **pre-rendered** so GitHub's mobile app (which renders
neither ` ```mermaid ` fenced blocks nor `$$...$$` math) shows them
correctly. README.md references the files here via `<img>`.

## Files

| File                  | Source                | Regenerate                                                                                                                              |
|-----------------------|-----------------------|-----------------------------------------------------------------------------------------------------------------------------------------|
| `envelope-flow.svg`   | `envelope-flow.mmd`   | `npx -y @mermaid-js/mermaid-cli -i envelope-flow.mmd -o envelope-flow.svg -c mermaid.config.json -p puppeteer.config.json -b "#0a1628" -w 1400 && node apply-blueprint.js envelope-flow.svg` |
| `lifecycle.svg`       | `lifecycle.mmd`       | `npx -y @mermaid-js/mermaid-cli -i lifecycle.mmd       -o lifecycle.svg       -c mermaid.config.json -p puppeteer.config.json -b "#0a1628" -w 1400 && node apply-blueprint.js lifecycle.svg`       |
| `slashing.svg`        | `slashing.mmd`        | `npx -y @mermaid-js/mermaid-cli -i slashing.mmd        -o slashing.svg        -c mermaid.config.json -p puppeteer.config.json -b "#0a1628" -w 1400 && node apply-blueprint.js slashing.svg`        |

The `apply-blueprint.js` step overlays an engineering-blueprint grid (navy
`#0a1628` paper, `#1e3a5f` major lines / `#16304f` minor lines) onto the
rendered diagram so it reads as a CAD drawing rather than a neutral dark
card. This is the same pass used by every sibling repo under
`@enchanter-ai`.

Run the commands from `docs/assets/` (paths are relative). The toolchain
(`node_modules/`, `package.json`, `package-lock.json`) is gitignored;
only the rendered SVGs and source files are committed.
