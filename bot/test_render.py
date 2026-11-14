import tempfile
import unittest
from pathlib import Path

from PIL import Image, ImageDraw

from render_card import format_artwork, render_artwork


class ArtworkExportTest(unittest.TestCase):
    def test_all_formats_preserve_complete_artwork_without_stretching(self):
        source = Image.new("RGB", (600, 900), "white")
        draw = ImageDraw.Draw(source)
        colors = [(255, 0, 0), (0, 255, 0), (0, 0, 255), (255, 0, 255)]
        for box, color in zip([(0, 0, 59, 59), (540, 0, 599, 59), (0, 840, 59, 899), (540, 840, 599, 899)], colors):
            draw.rectangle(box, fill=color)
        for ratio, size in [("1:1", (1080, 1080)), ("4:5", (1080, 1350)), ("16:9", (1440, 810))]:
            with self.subTest(ratio=ratio):
                result = format_artwork(source, ratio)
                self.assertEqual(result.size, size)
                for color in colors:
                    # Every corner must survive export; no headline/face crop.
                    pixels = result.load()
                    matches = [(x, y) for y in range(result.height) for x in range(result.width) if pixels[x, y] == color]
                    self.assertTrue(matches)
                    width = max(x for x, _ in matches) - min(x for x, _ in matches) + 1
                    height = max(y for _, y in matches) - min(y for _, y in matches) + 1
                    # A square stays square, so people are not stretched.
                    self.assertLess(abs(width - height), 3)

    def test_generated_poster_is_exported_without_template_or_photo_overlay(self):
        with tempfile.TemporaryDirectory() as directory:
            source, destination = Path(directory) / "generated.png", Path(directory) / "final.png"
            original = Image.new("RGB", (1080, 1350), "#285055")
            original.save(source)
            render_artwork({"background": str(source), "aspect_ratio": "4:5", "photos": ["should-never-be-opened.jpg"]}, destination)
            with Image.open(destination) as result:
                self.assertEqual(result.size, original.size)
                self.assertEqual(result.tobytes(), original.tobytes())


if __name__ == "__main__":
    unittest.main()
