"""Export complete generated artwork at Telegram-friendly dimensions."""

import json
import sys
from pathlib import Path
from PIL import Image, ImageOps, ImageFilter


FORMATS = {"1:1": (1080, 1080), "4:5": (1080, 1350), "16:9": (1440, 810)}


def format_artwork(source, ratio):
    """Keep the entire composition and natural proportions, including all text.

    Native model dimensions can differ from the requested export. Extend the
    background in that case; never crop headlines or stretch a person's face.
    """
    size = FORMATS.get(ratio, FORMATS["1:1"])
    source = ImageOps.exif_transpose(source).convert("RGB")
    if source.size == size:
        return source.copy()
    content = ImageOps.contain(source, size, method=Image.Resampling.LANCZOS)
    if content.size == size:
        return content
    canvas = ImageOps.fit(source, size, method=Image.Resampling.LANCZOS).filter(ImageFilter.GaussianBlur(40))
    canvas.paste(content, ((size[0] - content.width) // 2, (size[1] - content.height) // 2))
    return canvas


def render_artwork(data, destination):
    background = Path(data.get("background", ""))
    if not background.is_file():
        raise ValueError("An actual generated image is required")
    with Image.open(background) as source:
        im = format_artwork(source, data.get("aspect_ratio", "1:1"))
    Path(destination).parent.mkdir(parents=True, exist_ok=True)
    im.save(destination, "PNG")


def main():
    data = json.load(sys.stdin)
    render_artwork(data, sys.argv[1])


if __name__ == "__main__":
    main()
