"""Sentence-length audit with a proper tokenizer.

Replaces the naive `re.split` heuristic used by s10 with one that handles:
- Code blocks (```...```) — excluded
- Inline code (`...`) — preserved as single token
- Citation refs ([[REFNAME]]) — preserved
- Markdown links ([text](url)) — preserved
- Markdown tables — excluded
- Headings (lines starting with #) — excluded
- Abbreviations (e.g., i.e., RFC 8174 § 5, v1.4.2) — not treated as sentence boundaries

Usage:
    python sentence-audit.py index-v2.mdx
"""
from __future__ import annotations

import re
import sys
from pathlib import Path


def strip_non_prose(text: str) -> str:
    """Remove content that shouldn't count toward sentence-length stats."""
    # Strip YAML frontmatter
    text = re.sub(r"^---\n.*?\n---\n", "", text, count=1, flags=re.DOTALL)
    # Strip fenced code blocks (greedy)
    text = re.sub(r"```[^\n]*\n.*?\n```", "", text, flags=re.DOTALL)
    # Strip indented code blocks (4+ leading spaces) — rare in our spec
    text = re.sub(r"\n(?:    [^\n]*\n)+", "\n", text)
    # Strip markdown tables (lines starting with |)
    text = re.sub(r"^[ \t]*\|.*$", "", text, flags=re.MULTILINE)
    # Strip table separators
    text = re.sub(r"^[ \t]*\|?[ \t]*[-:]+[ \t]*\|.*$", "", text, flags=re.MULTILINE)
    # Strip headings
    text = re.sub(r"^#+ .*$", "", text, flags=re.MULTILINE)
    # Strip HTML-like JSX/MDX tags (Info, Warning, Note, div, etc.)
    text = re.sub(r"</?(?:Info|Warning|Note|div)[^>]*>", "", text)
    # Strip horizontal rules
    text = re.sub(r"^---+$", "", text, flags=re.MULTILINE)
    # Collapse multiple newlines
    text = re.sub(r"\n{3,}", "\n\n", text)
    return text


def tokenize_sentences(text: str) -> list[str]:
    """Split text into sentences using a tokenizer that handles real spec prose.

    The tokenizer:
    - Treats `.` `?` `!` as sentence boundaries only when followed by whitespace + capital letter
      or whitespace + `[` (for citation refs) or end of string
    - Recognizes abbreviations: e.g., i.e., vs., etc., RFC, §, v1.X, etc.
    - Does not split on `.` inside `code` (handled by stripping first), inside numbers (1.4.2),
      inside abbreviation (e.g.), inside DID (did:web:example.com#...)
    """
    # Protect known abbreviations and version strings with sentinel tokens
    protect = [
        (r"\be\.g\.", "<<EG>>"),
        (r"\bi\.e\.", "<<IE>>"),
        (r"\bvs\.", "<<VS>>"),
        (r"\betc\.", "<<ETC>>"),
        (r"\bcf\.", "<<CF>>"),
        (r"\bRFC \d+\.\d+", lambda m: m.group(0).replace(".", "<<DOT>>")),
        (r"§ ?\d+(?:\.\d+)+", lambda m: m.group(0).replace(".", "<<DOT>>")),
        # Version strings v1.2.3
        (r"\bv?\d+\.\d+(?:\.\d+)*\b", lambda m: m.group(0).replace(".", "<<DOT>>")),
        # DID identifiers (didtype:host:path) — periods are part of host
        (r"did:[a-z]+:[^\s)]+", lambda m: m.group(0).replace(".", "<<DOT>>")),
        # URLs
        (r"https?://[^\s)]+", lambda m: m.group(0).replace(".", "<<DOT>>")),
        # File extensions like schema.json
        (r"\b[a-z][a-z0-9_-]*\.(?:json|md|mdx|html|svg|py|ts|tsx|js|yaml|yml|xml|pdf)\b", lambda m: m.group(0).replace(".", "<<DOT>>")),
    ]
    for pattern, repl in protect:
        text = re.sub(pattern, repl, text, flags=re.IGNORECASE)

    # Split on sentence-ending punctuation followed by whitespace + capital
    sentences = re.split(r"(?<=[.!?])\s+(?=[A-Z\[\"])", text)

    # Restore protected dots and abbreviations
    out = []
    for s in sentences:
        s = s.replace("<<DOT>>", ".")
        s = s.replace("<<EG>>", "e.g.")
        s = s.replace("<<IE>>", "i.e.")
        s = s.replace("<<VS>>", "vs.")
        s = s.replace("<<ETC>>", "etc.")
        s = s.replace("<<CF>>", "cf.")
        s = s.strip()
        if s and len(s) > 1:
            out.append(s)
    return out


def count_words(sentence: str) -> int:
    """Count words in a sentence. Inline code, citations, links count as one token each."""
    # Replace inline code with single token
    s = re.sub(r"`[^`]+`", "CODE", sentence)
    # Replace citations with single token
    s = re.sub(r"\[\[[^\]]+\]\]", "CITE", s)
    # Replace markdown links with their text only
    s = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", s)
    # Now count whitespace-separated tokens
    return len([t for t in s.split() if any(c.isalnum() for c in t)])


def main() -> int:
    if len(sys.argv) < 2:
        print("Usage: python sentence-audit.py <file.mdx>", file=sys.stderr)
        return 2

    path = Path(sys.argv[1])
    text = path.read_text(encoding="utf-8")
    prose = strip_non_prose(text)
    sentences = tokenize_sentences(prose)
    lengths = [count_words(s) for s in sentences]
    total = sum(lengths)
    n = len(lengths)
    avg = total / n if n else 0.0
    over_30 = [s for s, ln in zip(sentences, lengths) if ln > 30]
    over_25 = [s for s, ln in zip(sentences, lengths) if ln > 25]

    print(f"File: {path}")
    print(f"Sentences: {n}")
    print(f"Total words: {total}")
    print(f"Average sentence length: {avg:.1f}")
    print(f"Sentences over 25 words: {len(over_25)} ({100 * len(over_25) / n:.1f}%)")
    print(f"Sentences over 30 words: {len(over_30)} ({100 * len(over_30) / n:.1f}%)")
    if over_30 and "--show-long" in sys.argv:
        print()
        print("Sentences over 30 words:")
        for i, s in enumerate(over_30[:30], 1):
            ln = count_words(s)
            print(f"  [{i}] ({ln} words) {s[:200]}{'...' if len(s) > 200 else ''}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
