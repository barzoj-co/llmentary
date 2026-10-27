"""MkDocs hook: point links that leave docs/ at the file on GitHub.

The Markdown under docs/ is also read on github.com, so it links to repo files
such as ../SECURITY.md or ../api/control/openapi.yaml. Those files are not part
of the site; rewrite them to blob/tree URLs on main so `mkdocs build --strict`
still checks every link that stays inside docs/.
"""

from __future__ import annotations

import posixpath
import re

REPO = "https://github.com/barzoj-co/llmentary"
LINK = re.compile(r"(\]\()(\.\./[^)\s#]*)(#[^)\s]*)?(\))")


def on_page_markdown(markdown, page, config, files):
    page_dir = posixpath.dirname(page.file.src_uri)

    def rewrite(match):
        target = match.group(2)
        resolved = posixpath.normpath(posixpath.join("docs", page_dir, target))
        if resolved == "docs" or resolved.startswith("docs/"):
            return match.group(0)
        kind = "tree" if target.endswith("/") else "blob"
        anchor = match.group(3) or ""
        return f"{match.group(1)}{REPO}/{kind}/main/{resolved}{anchor}{match.group(4)}"

    return LINK.sub(rewrite, markdown)
