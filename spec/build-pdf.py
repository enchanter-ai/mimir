"""Build the Tool-Call Provenance Envelope spec PDF from index-v2.1.mdx.

Enchanter Labs house style — v2 renderer.

Pipeline:
  1. Preflight: verify required tools and libraries; HALT on any absence (F22).
  2. Read source MDX.
  3. Strip YAML frontmatter.
  4. Replace Mermaid blocks with hand-authored SVG references.
  5. Convert MDX components (<Info>, <Warning>, <Note>, <Example>).
  6. Normalize citation refs [[KEY]] → styled links.
  7. Convert Markdown → HTML via markdown-it-py.
  8. Build TOC.
  9. Render cover page (full A4, geometric illustration, metadata).
  10. Wrap in HTML template with upgraded CSS.
  11. Write <src_name>-v2.html.
  12. Invoke Chrome headless --print-to-pdf → <src_name>-v2.pdf.

Diagram substitution map (Mermaid block index → SVG file):
  Block 1 → assets/diagrams/producer-flow.svg   (§ 8 producer flow)
  Block 2 → assets/diagrams/verifier-flow.svg   (§ 8 verifier flow)
  Block 3 → assets/diagrams/registry-query.svg  (§ 8 registry query)
  Any extra blocks fall back to the existing generated SVGs or plaintext.
"""

from __future__ import annotations

import re
import shutil
import subprocess
import sys
from datetime import date
from pathlib import Path

# ── Preflight ──────────────────────────────────────────────────────────────────

def preflight() -> None:
    """Verify all required tools and Python libraries.  HALT (F22) if any are missing."""
    errors: list[str] = []

    # Python libraries
    try:
        from markdown_it import MarkdownIt  # noqa: F401
    except ImportError:
        errors.append("Python library 'markdown-it-py' not found. Install with: pip install markdown-it-py")
    try:
        from mdit_py_plugins.anchors import anchors_plugin  # noqa: F401
    except ImportError:
        errors.append("Python library 'mdit-py-plugins' not found. Install with: pip install mdit-py-plugins")
    try:
        from mdit_py_plugins.footnote import footnote_plugin  # noqa: F401
    except ImportError:
        errors.append("Python library 'mdit-py-plugins[footnote]' not found.")

    # Chrome
    chrome_candidates = [
        r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
        "/usr/bin/google-chrome",
        "/usr/bin/chromium-browser",
        "/usr/bin/chromium",
    ]
    chrome_path = None
    for c in chrome_candidates:
        if Path(c).exists():
            chrome_path = c
            break
    if chrome_path is None:
        # Try shutil.which for PATH-based installs
        for name in ("google-chrome", "chromium-browser", "chromium", "chrome"):
            found = shutil.which(name)
            if found:
                chrome_path = found
                break
    if chrome_path is None:
        errors.append(
            "Chrome/Chromium not found. Required for --print-to-pdf. "
            "Install Chrome or set CHROME_PATH env var."
        )

    if errors:
        print("\n[F22] HALT — preflight failed:\n", file=sys.stderr)
        for e in errors:
            print(f"  • {e}", file=sys.stderr)
        sys.exit(1)

    return chrome_path  # type: ignore[return-value]


# ── Paths ──────────────────────────────────────────────────────────────────────

HERE = Path(__file__).parent
src_arg = sys.argv[1] if len(sys.argv) > 1 else "index-v2.1.mdx"
SRC = HERE / src_arg
src_stem = SRC.stem  # e.g. "index-v2.1"

ASSETS = HERE / "assets"
DIAGRAMS = ASSETS / "diagrams"
SPEC_HTML = HERE / f"{src_stem}.html"
SPEC_PDF  = HERE / f"{src_stem}.pdf"

DIAGRAMS.mkdir(parents=True, exist_ok=True)

# Hand-authored SVG substitution map: Mermaid block index (1-based) → SVG path
DIAGRAM_MAP: dict[int, Path] = {
    1: DIAGRAMS / "producer-flow.svg",
    2: DIAGRAMS / "verifier-flow.svg",
    3: DIAGRAMS / "registry-query.svg",
}
DIAGRAM_CAPTIONS: dict[int, str] = {
    1: "Figure 1 — Producer Flow (§ 8)",
    2: "Figure 2 — Verifier Flow (§ 8)",
    3: "Figure 3 — Registry Query (§ 8)",
}


# ── Parsing helpers ────────────────────────────────────────────────────────────

def strip_frontmatter(text: str) -> tuple[dict, str]:
    m = re.match(r"^---\n(.*?)\n---\n", text, re.DOTALL)
    if not m:
        return {}, text
    meta: dict[str, str] = {}
    for line in m.group(1).split("\n"):
        if ":" in line:
            k, _, v = line.partition(":")
            meta[k.strip()] = v.strip().strip('"')
    return meta, text[m.end():]


def replace_mermaid_with_svgs(text: str) -> str:
    """Replace ```mermaid``` blocks with hand-authored SVG <figure> tags.

    Falls back to a styled <pre> if the target SVG file does not exist.
    """
    pattern = re.compile(r"```mermaid[^\n]*\n(.*?)\n```", re.DOTALL)
    counter = [0]

    def replace(match: re.Match) -> str:
        counter[0] += 1
        idx = counter[0]
        svg_path = DIAGRAM_MAP.get(idx)

        if svg_path and svg_path.exists():
            rel = svg_path.relative_to(HERE).as_posix()
            caption = DIAGRAM_CAPTIONS.get(idx, f"Figure {idx}")
            return (
                f"<figure class='diagram' id='fig-{idx}'>"
                f"<img src='{rel}' alt='{caption}' />"
                f"<figcaption>{caption}</figcaption>"
                f"</figure>"
            )
        else:
            # Graceful fallback — show diagram source as pre-formatted block
            print(f"  [warn] no SVG for block {idx}; falling back to code block", file=sys.stderr)
            src = match.group(1)
            return f"<pre class='diagram-fallback'>{src}</pre>"

    return pattern.sub(replace, text)


def convert_mdx_components(text: str) -> str:
    text = re.sub(r'<div id="enable-section-numbers"\s*/>\s*', "", text)
    text = re.sub(r"<Info>\s*",    '<div class="callout callout-info">\n\n', text)
    text = re.sub(r"\s*</Info>",   "\n\n</div>", text)
    text = re.sub(r"<Warning>\s*", '<div class="callout callout-warning">\n\n', text)
    text = re.sub(r"\s*</Warning>","\n\n</div>", text)
    text = re.sub(r"<Note>\s*",    '<div class="callout callout-note">\n\n', text)
    text = re.sub(r"\s*</Note>",   "\n\n</div>", text)
    text = re.sub(r"<Example>\s*", '<div class="callout callout-example">\n\n', text)
    text = re.sub(r"\s*</Example>","\n\n</div>", text)
    return text


def normalize_citation_refs(text: str) -> str:
    def linkify(match: re.Match) -> str:
        key = match.group(1)
        slug = re.sub(r"[^a-z0-9]+", "-", key.lower()).strip("-")
        return f"<a class='ref' href='#ref-{slug}'>[{key}]</a>"
    return re.sub(r"\[\[([A-Za-z0-9-]+)\]\]", linkify, text)


def build_toc_from_html(html: str) -> str:
    pattern = re.compile(r'<h([23]) id="([^"]+)">(.*?)</h\1>', re.DOTALL)
    items = []
    h2_count = h3_count = 0
    for m in pattern.finditer(html):
        level = int(m.group(1))
        anchor = m.group(2)
        title = re.sub(r"<[^>]+>", "", m.group(3)).strip()
        if level == 2:
            h2_count += 1; h3_count = 0
            number = f"{h2_count}"
        else:
            h3_count += 1
            number = f"{h2_count}.{h3_count}"
        items.append((level, number, anchor, title))

    lines = ['<nav class="toc" id="toc"><h2 class="toc-title">Contents</h2><ol>']
    cur = 2
    for level, number, anchor, title in items:
        if level == 2:
            if cur == 3:
                lines.append("</ol></li>")
                cur = 2
            lines.append(f'<li class="toc-l2"><a href="#{anchor}"><span class="toc-num">{number}</span> {title}</a>')
        else:
            if cur == 2:
                lines.append("<ol class='toc-sub'>")
                cur = 3
            lines.append(f'<li class="toc-l3"><a href="#{anchor}"><span class="toc-num">{number}</span> {title}</a></li>')
    if cur == 3:
        lines.append("</ol></li>")
    lines.append("</ol></nav>")
    return "\n".join(lines)


# ── Cover page ─────────────────────────────────────────────────────────────────

def build_cover_page(meta: dict) -> str:
    """Return the HTML for the full-A4 cover page (injected as page 1)."""
    title       = meta.get("title", "Tool-Call Provenance Envelope")
    status      = meta.get("status", "Draft")
    created     = meta.get("created", str(date.today()))
    author      = meta.get("author", "Enchanter Labs")
    version_tag = "v2.1"

    cover_svg_path = (ASSETS / "cover.svg").relative_to(HERE).as_posix()

    # Real Enchanter Labs org logomark (github.com/enchanter-ai org avatar)
    logo_src = HERE / "assets" / "logo.png"
    logo_dest = ASSETS / "enchanter-labs-logo.png"
    if logo_src.exists() and not logo_dest.exists():
        import shutil
        shutil.copy(logo_src, logo_dest)
    logo_path = logo_dest.relative_to(HERE).as_posix() if logo_dest.exists() else ""

    # Status pill color by status value
    status_colors = {
        "Draft":              ("#fef3c7", "#b45309", "#92400e"),
        "Proposed Standard":  ("#eff6ff", "#1d4ed8", "#1e3a8a"),
        "Final":              ("#f0fdf4", "#15803d", "#14532d"),
        "Retired":            ("#f3f4f6", "#6b7280", "#374151"),
    }
    bg, fg, border = status_colors.get(status, ("#f3f4f6", "#6b7280", "#374151"))

    return f"""
<div class="cover-page">
  <!-- Geometric illustration: top-right quadrant -->
  <div class="cover-illustration">
    <img src="{cover_svg_path}" alt="Provenance Merkle tree geometric illustration" />
  </div>

  <!-- Real Enchanter Labs org logomark (github.com/enchanter-ai) -->
  <div class="cover-logomark">
    <img src="{logo_path}" alt="Enchanter Labs" width="56" height="56" style="border-radius:8px;display:block;" />
  </div>

  <!-- Brand name -->
  <div class="cover-brand">Enchanter Labs</div>

  <!-- Document type label -->
  <div class="cover-type-label">Standards Track Specification</div>

  <!-- Title block -->
  <h1 class="cover-title">{title}</h1>
  <div class="cover-version">{version_tag}</div>

  <!-- Status pill -->
  <div class="cover-status-pill" style="background:{bg};color:{fg};border:1px solid {border};">
    {status}
  </div>

  <!-- Metadata footer -->
  <div class="cover-meta">
    <div class="cover-meta-row">
      <span class="cover-meta-key">Author</span>
      <span class="cover-meta-val">{author}</span>
    </div>
    <div class="cover-meta-row">
      <span class="cover-meta-key">Created</span>
      <span class="cover-meta-val">{created}</span>
    </div>
    <div class="cover-meta-row">
      <span class="cover-meta-key">Category</span>
      <span class="cover-meta-val">{meta.get("category", "server-feature")}</span>
    </div>
    <div class="cover-meta-row">
      <span class="cover-meta-key">Last-call deadline</span>
      <span class="cover-meta-val">{meta.get("last-call-deadline", "")}</span>
    </div>
  </div>

  <!-- Bottom divider with accent dot -->
  <div class="cover-rule">
    <div class="cover-rule-line"></div>
    <div class="cover-rule-dot"></div>
    <div class="cover-rule-line"></div>
  </div>
</div>
"""


# ── CSS ────────────────────────────────────────────────────────────────────────
#
# Design tokens
#   --accent      #1d3a8e  deep ink-blue
#   --text        #1a1a1a  charcoal black
#   --border      #d6d4cc  neutral warm gray
#   --bg-subtle   #f7f7f5  near-white
#   --code-bg     #f4f4f1

CSS = """
/* ── Design tokens ──────────────────────────────────────────── */
:root {
  --accent:       #1d3a8e;
  --accent-light: #eef1fa;
  --text:         #1a1a1a;
  --text-muted:   #555;
  --border:       #d6d4cc;
  --bg-subtle:    #f7f7f5;
  --bg-code:      #f4f4f1;
  --font-serif:   'Charter', 'Source Serif Pro', 'Georgia', serif;
  --font-sans:    'Inter', 'Source Sans Pro', 'Segoe UI', system-ui, sans-serif;
  --font-mono:    'JetBrains Mono', 'SF Mono', 'Consolas', 'Source Code Pro', monospace;
}

/* ── Page layout ─────────────────────────────────────────────── */
@page {
  size: A4;
  margin: 22mm 18mm 22mm 18mm;
  @bottom-left {
    content: "Enchanter Labs";
    font-family: var(--font-sans);
    font-size: 8pt;
    color: #aaa;
  }
  @bottom-right {
    content: counter(page) " / " counter(pages);
    font-family: var(--font-sans);
    font-size: 8pt;
    color: #aaa;
  }
}

@page :first {
  margin: 0;
  @bottom-left  { content: none; }
  @bottom-right { content: none; }
}

* { box-sizing: border-box; }

html { font-size: 10.5pt; }

body {
  font-family: var(--font-serif);
  line-height: 1.58;
  color: var(--text);
  max-width: 840px;
  margin: 0 auto;
  padding: 0 16px 80px;
  counter-reset: h2;
  font-feature-settings: "kern", "liga", "onum";
}

/* ── Cover page ──────────────────────────────────────────────── */
.cover-page {
  width: 210mm;
  height: 297mm;
  page-break-after: always;
  position: relative;
  background: white;
  margin: 0;
  padding: 0;
  overflow: hidden;
}

.cover-illustration {
  position: absolute;
  top: 0;
  right: 0;
  width: 260px;
  height: 260px;
  opacity: 0.72;
}
.cover-illustration img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.cover-logomark {
  position: absolute;
  top: 52mm;
  left: 22mm;
}

.cover-brand {
  position: absolute;
  top: 52mm;
  left: 36mm;
  font-family: var(--font-sans);
  font-size: 13pt;
  font-weight: 600;
  color: var(--accent);
  letter-spacing: 0.03em;
  line-height: 48px; /* align with logomark */
}

.cover-type-label {
  position: absolute;
  top: 65mm;
  left: 22mm;
  font-family: var(--font-sans);
  font-size: 9pt;
  color: #888;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.cover-title {
  position: absolute;
  top: 80mm;
  left: 22mm;
  right: 22mm;
  font-family: var(--font-serif);
  font-size: 28pt;
  font-weight: 700;
  line-height: 1.15;
  color: var(--text);
  letter-spacing: -0.02em;
  margin: 0;
  border: none;
  counter-increment: none;
}
.cover-title::before { content: none; }

.cover-version {
  position: absolute;
  top: 118mm;
  left: 22mm;
  font-family: var(--font-sans);
  font-size: 11pt;
  color: #888;
  font-weight: 400;
}

.cover-status-pill {
  position: absolute;
  top: 127mm;
  left: 22mm;
  font-family: var(--font-sans);
  font-size: 8.5pt;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.07em;
  padding: 3px 10px;
  border-radius: 3px;
  display: inline-block;
}

/* Thin rule below title block */
.cover-rule {
  position: absolute;
  top: 144mm;
  left: 22mm;
  right: 22mm;
  display: flex;
  align-items: center;
  gap: 10px;
}
.cover-rule-line {
  flex: 1;
  height: 1px;
  background: var(--border);
}
.cover-rule-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  flex-shrink: 0;
}

.cover-meta {
  position: absolute;
  top: 156mm;
  left: 22mm;
  right: 22mm;
  font-family: var(--font-sans);
  font-size: 9pt;
}
.cover-meta-row {
  display: flex;
  gap: 16px;
  margin: 4px 0;
}
.cover-meta-key {
  color: #888;
  min-width: 120px;
  font-weight: 500;
}
.cover-meta-val {
  color: var(--text);
}

/* ── Typography ──────────────────────────────────────────────── */
h1, h2, h3, h4, h5, h6 {
  font-family: var(--font-sans);
  color: #0a0a0a;
  font-weight: 600;
  line-height: 1.2;
  margin-top: 1.8em;
  margin-bottom: 0.55em;
  page-break-after: avoid;
}

h1 {
  font-size: 24pt;
  font-weight: 700;
  border-bottom: 2px solid #0a0a0a;
  padding-bottom: 10px;
  margin-bottom: 6px;
  letter-spacing: -0.02em;
}

h2 {
  font-size: 16pt;
  counter-reset: h3;
  padding-bottom: 5px;
  page-break-before: always;
  /* Section divider: thin rule with centered bullet */
}
h2::before {
  counter-increment: h2;
  content: counter(h2) ". ";
  color: #999;
  font-weight: 500;
  margin-right: 0.3em;
}

/* Section rule divider above h2 */
h2 {
  position: relative;
}

h3 {
  font-size: 12.5pt;
  color: #222;
}
h3::before {
  counter-increment: h3;
  content: counter(h2) "." counter(h3) ". ";
  color: #999;
  font-weight: 500;
  margin-right: 0.3em;
}

h4 { font-size: 11pt; color: #333; }

p { margin: 0.55em 0 0.75em; orphans: 3; widows: 3; }

em.non-normative {
  display: block;
  font-style: italic;
  color: #666;
  font-size: 0.95em;
  margin: 0 0 0.6em;
}

strong { font-weight: 700; color: #000; }

a {
  color: var(--accent);
  text-decoration: none;
  border-bottom: 1px solid rgba(29, 58, 142, 0.25);
}
a:hover { border-bottom-color: var(--accent); }

a.ref {
  font-family: var(--font-sans);
  font-size: 0.84em;
  white-space: nowrap;
  border-bottom: none;
  color: #0f766e;
  background: #f0fdfa;
  padding: 0 4px;
  border-radius: 3px;
}

/* ── Code ────────────────────────────────────────────────────── */
code {
  font-family: var(--font-mono);
  font-size: 0.87em;
  background: var(--bg-code);
  color: #be185d;
  padding: 1px 4px;
  border-radius: 3px;
  border: 1px solid #ebe9e3;
}

pre {
  font-family: var(--font-mono);
  font-size: 8.5pt;
  line-height: 1.42;
  background: #fafaf7;
  border: 1px solid #e5e3da;
  border-left: 3px solid var(--accent);
  border-radius: 3px;
  padding: 10px 14px;
  overflow-x: auto;
  margin: 0.8em 0 1em;
  page-break-inside: avoid;
}
pre code {
  background: transparent;
  color: var(--text);
  padding: 0;
  border: none;
  font-size: inherit;
}

/* ── Spec metadata block ─────────────────────────────────────── */
.spec-meta {
  font-family: var(--font-sans);
  font-size: 9.5pt;
  color: var(--text-muted);
  margin: 16px 0 32px;
  padding: 14px 16px;
  background: var(--bg-subtle);
  border-left: 3px solid var(--accent);
  border-radius: 2px;
}
.spec-meta dl {
  margin: 0;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 20px;
}
.spec-meta dt { font-weight: 600; color: #333; text-transform: capitalize; }
.spec-meta dd { margin: 0; color: #444; }

/* ── Status banner ───────────────────────────────────────────── */
.status-banner {
  font-family: var(--font-sans);
  font-size: 9pt;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: #b45309;
  background: #fef3c7;
  padding: 4px 10px;
  border-radius: 3px;
  display: inline-block;
  margin: 0 0 16px;
  font-weight: 600;
}

/* ── Table styles ────────────────────────────────────────────── */

/* Base table */
table {
  border-collapse: collapse;
  width: 100%;
  margin: 1em 0;
  font-size: 9.5pt;
  page-break-inside: avoid;
}
th, td {
  border: 1px solid var(--border);
  padding: 6px 10px;
  text-align: left;
  vertical-align: top;
}
th {
  background: #ededea;
  font-family: var(--font-sans);
  font-weight: 600;
  color: #222;
  font-size: 8.5pt;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-variant: small-caps;
}
table code { font-size: 0.88em; }

/* Field-definition tables: zebra rows */
table.field-table tbody tr:nth-child(odd)  { background: white; }
table.field-table tbody tr:nth-child(even) { background: #fafaf8; }

/* Error-code tables: compact, dense, monospace code column */
table.error-table {
  font-size: 8.5pt;
}
table.error-table td:first-child {
  font-family: var(--font-mono);
  font-size: 0.9em;
  white-space: nowrap;
  color: #be185d;
}
table.error-table tbody tr:nth-child(odd)  { background: white; }
table.error-table tbody tr:nth-child(even) { background: #fafaf8; }

/* Comparison tables: no zebra, vertical rule between columns */
table.comparison-table {
  max-width: 80%;
}
table.comparison-table td + td {
  border-left: 2px solid var(--border);
}

/* Default zebra for unclassed tables */
tbody tr:nth-child(even) { background: #fafaf8; }

/* ── Lists ───────────────────────────────────────────────────── */
ol, ul { margin: 0.5em 0 0.8em; padding-left: 1.6em; }
li { margin: 0.25em 0; }
ol li::marker, ul li::marker { color: #777; }

/* ── Block quote / pull quote ────────────────────────────────── */
blockquote {
  margin: 1.2em 0;
  padding: 0 0 0 20px;
  border-left: 3px solid var(--accent);
  color: #334155;
  font-style: italic;
  background: transparent;
}

/* Pull quote — use <blockquote class="pull-quote"> */
blockquote.pull-quote {
  font-family: var(--font-serif);
  font-size: 14pt;
  font-style: italic;
  color: #1a1a1a;
  border: none;
  padding: 0 2em;
  text-align: center;
  position: relative;
}
blockquote.pull-quote::before {
  content: '\201C';
  font-size: 40pt;
  color: var(--accent);
  line-height: 0.8;
  display: block;
  margin-bottom: -0.2em;
}
blockquote.pull-quote::after {
  content: '\201D';
  font-size: 40pt;
  color: var(--accent);
  line-height: 0;
  display: inline-block;
  vertical-align: bottom;
  margin-left: 0.1em;
}

/* ── Callouts ────────────────────────────────────────────────── */
.callout {
  margin: 1em 0;
  padding: 10px 16px 10px 14px;
  border-left: 3px solid;
  border-radius: 0 3px 3px 0;
  font-size: 0.95em;
  page-break-inside: avoid;
}
.callout p:first-child { margin-top: 0; }
.callout p:last-child  { margin-bottom: 0; }

.callout-info {
  border-color: var(--accent);
  background: var(--accent-light);
  color: #1e3a8a;
}
.callout-info::before {
  content: 'ℹ  ';
  font-style: normal;
  font-weight: 700;
  color: var(--accent);
  font-family: var(--font-sans);
}

.callout-warning {
  border-color: #d97706;
  background: #fef3c7;
  color: #78350f;
}
.callout-warning::before {
  content: '⚠  ';
  font-style: normal;
  font-weight: 700;
  color: #d97706;
  font-family: var(--font-sans);
}

.callout-note {
  border-color: #6b7280;
  background: #f3f4f6;
  color: #1f2937;
}
.callout-note::before {
  content: '◆  ';
  font-style: normal;
  font-weight: 700;
  color: #6b7280;
  font-family: var(--font-sans);
}

.callout-example {
  border-color: #555;
  background: #fafaf7;
  color: #333;
}
.callout-example::before {
  content: '▷  ';
  font-style: normal;
  font-weight: 700;
  color: #555;
  font-family: var(--font-sans);
}

/* ── Diagrams ────────────────────────────────────────────────── */
figure.diagram {
  margin: 1.6em 0;
  text-align: center;
  page-break-inside: avoid;
}
figure.diagram img {
  max-width: 100%;
  height: auto;
  border: 1px solid var(--border);
  border-radius: 3px;
  background: white;
}
figure.diagram figcaption {
  font-family: var(--font-sans);
  font-size: 9pt;
  color: #888;
  margin-top: 6px;
  font-style: italic;
}
pre.diagram-fallback {
  border-left: 3px solid #e5e3da;
  color: #666;
  font-size: 8pt;
}

/* ── Section dividers ────────────────────────────────────────── */
/* Injected by h2 pseudo-element pattern above;
   explicit .section-divider class for manual use. */
.section-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 2em 0 1em;
}
.section-divider::before,
.section-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.section-divider span {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  flex-shrink: 0;
}

/* ── Table of Contents ───────────────────────────────────────── */
nav.toc {
  page-break-after: always;
  margin: 24px 0;
  padding: 18px 22px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-left: 3px solid var(--accent);
  border-radius: 2px;
}
nav.toc .toc-title {
  margin-top: 0;
  font-size: 13pt;
  border-bottom: 1px solid var(--border);
  padding-bottom: 7px;
  font-family: var(--font-sans);
  color: var(--accent);
}
nav.toc::before { content: none; }
nav.toc h2::before { content: none; counter-increment: none; }
nav.toc h2 { border-bottom: 1px solid var(--border); page-break-before: avoid; }
nav.toc ol {
  list-style: none;
  padding-left: 0;
  margin: 0;
}
nav.toc .toc-sub { padding-left: 22px; margin-top: 3px; }
nav.toc li.toc-l2 {
  margin: 5px 0;
  font-family: var(--font-sans);
  font-size: 10pt;
  font-weight: 600;
}
nav.toc li.toc-l3 {
  margin: 2px 0;
  font-family: var(--font-sans);
  font-size: 9pt;
  font-weight: 400;
  color: #555;
}
nav.toc a {
  color: inherit;
  border-bottom: none;
  display: flex;
  gap: 12px;
}
nav.toc .toc-num {
  color: #999;
  flex-shrink: 0;
  min-width: 30px;
  font-variant-numeric: tabular-nums;
  font-size: 0.92em;
}

/* ── Print refinements ───────────────────────────────────────── */
@media print {
  body { padding: 0; }
  a { color: inherit; border-bottom: none; }
  a.ref { color: #0f766e; background: transparent; }
  h2 { page-break-before: always; }
  h2:first-of-type { page-break-before: avoid; }
  nav.toc h2 { page-break-before: avoid; }
  .spec-meta, .status-banner { page-break-after: avoid; }
  .cover-page { page-break-after: always; }
}
"""


# ── HTML template ──────────────────────────────────────────────────────────────

def build_html(meta: dict, body_html: str, toc_html: str, cover_html: str) -> str:
    meta_items = "".join(
        f"<dt>{k}</dt><dd>{v}</dd>"
        for k, v in meta.items()
        if k not in ("title",)
    )
    title  = meta.get("title", "Tool-Call Provenance Envelope")
    status = meta.get("status", "Draft")
    stype  = meta.get("type", "Standards Track")
    created = meta.get("created", "")

    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{title}</title>
<style>{CSS}</style>
</head>
<body>
{cover_html}
<h1>{title}</h1>
<div class="status-banner">Status: {status} &middot; {stype} &middot; {created}</div>
<div class="spec-meta"><dl>{meta_items}</dl></div>
{toc_html}
{body_html}
</body>
</html>
"""


# ── Main ───────────────────────────────────────────────────────────────────────

def main() -> int:
    chrome_path = preflight()

    print(f"reading {SRC}")
    if not SRC.exists():
        print(f"[F22] HALT — source file not found: {SRC}", file=sys.stderr)
        return 1
    text = SRC.read_text(encoding="utf-8")

    meta, body = strip_frontmatter(text)
    print(f"  frontmatter: {len(meta)} keys — {list(meta.keys())}")

    print("substituting hand-authored SVG diagrams (replacing Mermaid blocks)")
    body = replace_mermaid_with_svgs(body)

    print("converting MDX components")
    body = convert_mdx_components(body)

    print("normalizing citation refs")
    body = normalize_citation_refs(body)

    # Non-normative phrase markup
    body = re.sub(
        r"^\s*\*This section is non-normative\.\*\s*$",
        '<em class="non-normative">This section is non-normative.</em>',
        body,
        flags=re.MULTILINE,
    )

    print("converting Markdown -> HTML")
    from markdown_it import MarkdownIt
    from mdit_py_plugins.anchors import anchors_plugin
    from mdit_py_plugins.footnote import footnote_plugin

    md = (
        MarkdownIt("commonmark", {"html": True, "linkify": True, "typographer": True})
        .enable("table")
        .enable("strikethrough")
        .use(anchors_plugin, max_level=4, slug_func=lambda s: re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-"))
        .use(footnote_plugin)
    )
    body_html = md.render(body)

    print("building TOC")
    toc_html = build_toc_from_html(body_html)

    print("rendering cover page")
    cover_html = build_cover_page(meta)

    print(f"writing {SPEC_HTML.name}")
    final_html = build_html(meta, body_html, toc_html, cover_html)
    SPEC_HTML.write_text(final_html, encoding="utf-8")

    print("invoking Chrome headless --print-to-pdf")
    src_url  = "file:///" + str(SPEC_HTML).replace("\\", "/")
    out_path = str(SPEC_PDF).replace("\\", "/")
    result = subprocess.run(
        [
            chrome_path,
            "--headless=new",
            "--disable-gpu",
            "--no-sandbox",
            "--no-pdf-header-footer",
            f"--print-to-pdf={out_path}",
            src_url,
        ],
        capture_output=True,
        text=True,
        timeout=180,
    )
    if result.returncode != 0:
        print("chrome stderr:", result.stderr, file=sys.stderr)
        print("chrome stdout:", result.stdout, file=sys.stderr)
        return 1

    if SPEC_PDF.exists():
        size_kb = SPEC_PDF.stat().st_size / 1024
        print(f"\n  OK {SPEC_PDF.name} written: {size_kb:.1f} KB")
        return 0
    else:
        print(f"\n  {SPEC_PDF.name} was not created", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
