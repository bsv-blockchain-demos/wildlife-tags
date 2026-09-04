#!/usr/bin/env python3
"""Render prfaq-wildtag.md into the reading/print HTML.

Small on purpose. The document uses a narrow slice of markdown -- headings,
paragraphs, emphasis, links, tables, lists and one blockquote -- so a hundred
lines here beats a dependency, and it keeps the two formats provably identical.
"""
import html
import pathlib
import re

HERE = pathlib.Path(__file__).parent
HEAD = pathlib.Path(
    "/tmp/claude-1000/-git-dnr-tags/35bfa527-a84f-428b-aa12-1190393e0079/scratchpad/prfaq-head.html"
)


def inline(t: str) -> str:
    t = html.escape(t, quote=False)
    t = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", r'<a href="\2">\1</a>', t)
    # A bare URL in the sources list, made clickable without becoming noise.
    t = re.sub(r"(?<![\"=>])(https?://[^\s<]+)", r'<a class="src" href="\1">\1</a>', t)
    t = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", t)
    t = re.sub(r"(?<!\*)\*([^*]+)\*(?!\*)", r"<em>\1</em>", t)
    t = re.sub(r"`([^`]+)`", r"<code>\1</code>", t)
    # The open questions are what this document is for; make them findable.
    return re.sub(r"\[OPEN([^\]]*)\]", r'<span class="open">[OPEN\1]</span>', t)


def convert(src: str) -> str:
    out, lines, i = [], src.split("\n"), 0
    while i < len(lines):
        ln = lines[i]

        if ln.startswith("---") and set(ln.strip()) == {"-"}:
            out.append("<hr>")
            i += 1
        elif ln.startswith("#"):
            lvl = len(ln) - len(ln.lstrip("#"))
            txt = ln.lstrip("# ").strip()
            slug = re.sub(r"[^a-z0-9]+", "-", txt.lower()).strip("-")[:48]
            out.append(f'<h{lvl} id="{slug}">{inline(txt)}</h{lvl}>')
            i += 1
        elif ln.startswith("> "):
            buf = []
            while i < len(lines) and lines[i].startswith(">"):
                buf.append(lines[i].lstrip("> ").rstrip())
                i += 1
            out.append('<aside class="notice">' + inline(" ".join(buf)) + "</aside>")
        elif ln.startswith("| "):
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                rows.append([c.strip() for c in lines[i].strip("|").split("|")])
                i += 1
            head, body = rows[0], rows[2:]
            t = ['<div class="tablewrap"><table><thead><tr>']
            t += [f"<th>{inline(c)}</th>" for c in head]
            t.append("</tr></thead><tbody>")
            for r in body:
                t.append("<tr>" + "".join(f"<td>{inline(c)}</td>" for c in r) + "</tr>")
            t.append("</tbody></table></div>")
            out.append("".join(t))
        elif ln.startswith("- "):
            items = []
            while i < len(lines) and (lines[i].startswith("- ") or lines[i].startswith("  ")):
                if lines[i].startswith("- "):
                    items.append(lines[i][2:].strip())
                else:
                    items[-1] += " " + lines[i].strip()
                i += 1
            out.append("<ul>" + "".join(f"<li>{inline(x)}</li>" for x in items) + "</ul>")
        elif ln.strip() == "":
            i += 1
        else:
            buf = []
            while i < len(lines) and lines[i].strip() and lines[i][0] not in "#|->":
                buf.append(lines[i].strip())
                i += 1
            out.append("<p>" + inline(" ".join(buf)) + "</p>")
    return "\n".join(out)


src = (HERE / "prfaq-wildtag.md").read_text().split("\n", 1)[1]
body = convert(src)

# The metadata line and the draft notice are rendered by the masthead instead of
# flowing with the prose, so they come out of the body here.
body = re.sub(r"<p><strong>Author:</strong>.*?</p>\n", "", body, flags=re.S, count=1)
notice = re.search(r'<aside class="notice">.*?</aside>', body, flags=re.S)
notice_html = notice.group(0) if notice else ""
body = body.replace(notice_html, "", 1).lstrip("\n")
body = re.sub(r"^<hr>\n", "", body, count=1)

rail = "".join(
    f'<li><a href="#{m.group(1)}">{re.sub(r"<[^>]+>", "", m.group(2))}</a></li>'
    for m in re.finditer(r'<h2 id="([^"]+)">(.*?)</h2>', body)
)

print(f"""{HEAD.read_text()}
<div class="sheet">
  <header class="masthead">
    <p class="eyebrow">Working Backwards &middot; BSVA</p>
    <h1>WildTag &mdash; PR/FAQ</h1>
    <div class="meta">
      <span>Author <b>Dylan Murray</b></span>
      <span>Date <b>1 September 2026</b></span>
      <span>Version <b>v0.1</b></span>
      <span>Status <b>Draft</b></span>
      <span>Reviewers <b>none</b></span>
    </div>
  </header>

  <nav class="rail" aria-label="Contents">
    <p>Contents</p>
    <ol>{rail}</ol>
  </nav>

  <main>
    {notice_html}
    {body}
  </main>
</div>""")
