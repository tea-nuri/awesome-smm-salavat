"""Extract bounded text from documents and HTML without executing page content."""
import argparse
import json
import re
import sys
import zipfile
from html.parser import HTMLParser
from pathlib import Path
from xml.etree import ElementTree


class WebsiteText(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.parts, self.title, self.links = [], [], []
        self.ignored = 0
        self.in_title = False
        self.href, self.anchor = None, []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag in {"script", "style", "noscript", "svg", "nav", "footer"}:
            self.ignored += 1
        if tag == "title": self.in_title = True
        if tag == "a": self.href, self.anchor = attrs.get("href"), []
        if tag in {"p", "div", "h1", "h2", "h3", "li", "br", "section"}: self.parts.append("\n")

    def handle_endtag(self, tag):
        if tag in {"script", "style", "noscript", "svg", "nav", "footer"}: self.ignored = max(0, self.ignored - 1)
        if tag == "title": self.in_title = False
        if tag == "a" and self.href:
            self.links.append({"url": self.href, "text": " ".join(self.anchor)})
            self.href = None

    def handle_data(self, value):
        if self.in_title: self.title.append(value)
        if self.href: self.anchor.append(value)
        if not self.ignored and not self.in_title: self.parts.append(value)


def extract(path, kind, encoding=""):
    raw = Path(path).read_bytes()
    links, title = [], ""
    if kind == "pdf":
        from pypdf import PdfReader
        reader = PdfReader(path)
        if reader.is_encrypted: raise ValueError("PDF защищён паролем")
        pieces, length = [], 0
        for page in reader.pages[:80]:
            part = page.extract_text() or ""
            pieces.append(part)
            length += len(part)
            if length > 60000: break
        text = "\n".join(pieces)
    elif kind == "docx":
        with zipfile.ZipFile(path) as archive:
            info = archive.getinfo("word/document.xml")
            if info.file_size > 10_000_000: raise ValueError("Документ слишком большой")
            xml = archive.read(info)
        if b"<!DOCTYPE" in xml: raise ValueError("Неподдерживаемая структура документа")
        root = ElementTree.fromstring(xml)
        text = "\n".join("".join(p.itertext()) for p in root.iter("{http://schemas.openxmlformats.org/wordprocessingml/2006/main}p"))
    else:
        if not encoding and kind == "html":
            match = re.search(rb'charset\s*=\s*["\']?([A-Za-z0-9_-]+)', raw[:5000], re.I)
            if match: encoding = match.group(1).decode("ascii")
        try: text = raw.decode(encoding or "utf-8-sig")
        except (LookupError, UnicodeDecodeError): text = raw.decode("cp1251" if kind == "html" else "utf-8-sig", errors="replace")
        if kind == "html":
            parser = WebsiteText()
            parser.feed(text)
            text, title, links = " ".join(parser.parts), " ".join(parser.title), parser.links
    text = "\n".join(re.sub(r"[ \t]+", " ", line).strip() for line in text.splitlines())
    text = re.sub(r"\n{3,}", "\n\n", text).strip()[:60000]
    if len(text) < 20: raise ValueError("Не удалось извлечь текст. Для сканов загрузите документ с текстовым слоем")
    return {"text": text, "title": title[:200], "links": links[:200]}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("path")
    parser.add_argument("kind", choices=["pdf", "docx", "txt", "md", "html"])
    parser.add_argument("--encoding", default="")
    args = parser.parse_args()
    try: print(json.dumps(extract(args.path, args.kind, args.encoding), ensure_ascii=False))
    except Exception as error:
        print(json.dumps({"error": str(error)[:200]}, ensure_ascii=False))
        sys.exit(1)
