import tempfile
import unittest
import zipfile
from pathlib import Path

from extract_knowledge import extract


class SourceExtractionTest(unittest.TestCase):
    def test_docx_text_is_read(self):
        with tempfile.TemporaryDirectory() as folder:
            file = Path(folder) / "company.docx"
            with zipfile.ZipFile(file, "w") as archive:
                archive.writestr("word/document.xml", '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Компания выпускает стекло для архитектуры.</w:t></w:r></w:p></w:body></w:document>')
            self.assertIn("стекло для архитектуры", extract(file, "docx")["text"])

    def test_html_excludes_scripts_and_style(self):
        with tempfile.TemporaryDirectory() as folder:
            file = Path(folder) / "site.html"
            file.write_text('<title>Компания</title><style>CSS_SECRET</style><script>JS_SECRET</script><h1>О компании</h1><p>Сведения о продукции и сотрудничестве.</p>')
            result = extract(file, "html")
            self.assertEqual(result["title"], "Компания")
            self.assertIn("продукции", result["text"])
            self.assertNotIn("SECRET", result["text"])


if __name__ == "__main__":
    unittest.main()
