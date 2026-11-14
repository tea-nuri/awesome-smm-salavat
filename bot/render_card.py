"""Deterministic PNG card rendered from the AI's art direction, no external API."""

import json, math, os, re, sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont, ImageOps


def font(size, bold=False):
    choices = [
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
        if bold
        else "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
        if bold
        else "/System/Library/Fonts/Supplemental/Arial.ttf",
    ]
    for path in choices:
        if Path(path).exists():
            return ImageFont.truetype(path, size)
    return ImageFont.load_default()


def wrap(draw, text, ft, width):
    lines, current = [], ""
    for word in text.split():
        trial = (current + " " + word).strip()
        if draw.textlength(trial, font=ft) > width and current:
            lines.append(current)
            current = word
        else:
            current = trial
    if current:
        lines.append(current)
    return lines


def render_salavat(data, destination):
    assets = Path(os.getenv("BRAND_ASSETS", str(Path(__file__).resolve().parent.parent / "frontend/public/assets/salavat")))
    background = Path(data.get("background", ""))
    if not background.is_file():
        raise ValueError("An actual generated image is required for the brand card")
    with Image.open(background) as source:
        im = ImageOps.fit(source.convert("RGB"), (1080, 1080), method=Image.Resampling.LANCZOS)
    d = ImageDraw.Draw(im)
    # The wordmark is exported from the client's Illustrator artboard, never retyped.
    d.rectangle((0, 0, 1080, 180), fill="white")
    with Image.open(assets / "logo.png") as logo:
        logo = logo.convert("RGBA")
        logo.thumbnail((232, 126), Image.Resampling.LANCZOS)
        im.paste(logo, (54, 28), logo)
    small = ImageFont.truetype(str(assets / "avenir-demi.ttf"), 18)
    rubric = str(data.get("rubric") or "В КОМАНДЕ")[:30].upper()
    d.text((1024, 88), "В КОМАНДЕ", font=small, fill="#196C7F", anchor="rm")
    d.line((54, 179, 1026, 179), fill="#DDE1E1", width=2)
    # Light editorial field recalls the white collage edges in supplied references.
    d.polygon([(0, 702), (260, 684), (440, 702), (715, 670), (1080, 691), (1080, 1080), (0, 1080)], fill="white")
    d.text((58, 722), rubric, font=small, fill="#196C7F")
    title = str(data.get("title") or "СИЛА НАШЕЙ КОМАНДЫ").upper()
    for size in range(64, 31, -2):
        heading = ImageFont.truetype(str(assets / "intro.ttf"), size)
        lines = wrap(d, title, heading, 964)
        if len(lines) <= 2 and all(d.textlength(line, font=heading) <= 964 for line in lines):
            break
    if len(lines) > 2:
        raise ValueError("Brand headline is too long for the approved layout")
    y = 774
    for line in lines:
        d.text((54, y), line, font=heading, fill="#285055")
        y += size + 15
    body_font = ImageFont.truetype(str(assets / "avenir-regular.ttf"), 29)
    subtitle = str(data.get("subtitle") or "Люди. Производство. Наша общая история.")[:100]
    for line in wrap(d, subtitle, body_font, 950)[:2]:
        d.text((57, y + 16), line, font=body_font, fill="#52737A")
        y += 39
    d.line((58, 1047, 1024, 1047), fill="#C1D0D3", width=2)
    Path(destination).parent.mkdir(parents=True, exist_ok=True)
    im.save(destination, "PNG")


def main():
    data = json.load(sys.stdin)
    if data.get("brand_profile") == "salavatsteklo":
        render_salavat(data, sys.argv[1])
        return
    accent = data.get("accent", "#6948ff")
    if not re.fullmatch(r"#[0-9A-Fa-f]{6}", accent):
        accent = "#6948ff"
    rgb = tuple(int(accent[i : i + 2], 16) for i in (1, 3, 5))
    im = Image.new("RGB", (1080, 1080), "#f2f0ff")
    d = ImageDraw.Draw(im)
    d.rounded_rectangle((55, 55, 1025, 1025), radius=38, fill="#ffffff")
    d.text(
        (100, 100),
        str(data.get("company", "AWESOME"))[:40].upper(),
        fill="#353044",
        font=font(22, True),
    )
    d.rounded_rectangle((100, 164, 247, 200), 18, fill="#f0ecff")
    d.text((117, 170), "AI / STUDIO", fill=rgb, font=font(15, True))
    art = Image.new("RGBA", im.size, (0, 0, 0, 0))
    p = ImageDraw.Draw(art)
    motif = data.get("motif", "orbit")
    if motif == "waves":
        for i in range(8):
            pts = [
                (70 + x, 460 + math.sin(x / 125 + i * 0.4) * 110 + i * 23)
                for x in range(950)
            ]
            p.line(pts, fill=(*rgb, 180 - i * 15), width=14)
    elif motif == "grid":
        for i in range(5):
            for j in range(4):
                x = 150 + i * 160
                y = 280 + j * 85
                p.rounded_rectangle(
                    (x, y, x + 135, y + 65),
                    20,
                    fill=(*rgb, 35 + i * 22),
                    outline=(*rgb, 150),
                    width=2,
                )
    elif motif == "glass":
        for i in range(5):
            x = 230 + i * 105
            y = 280 + i * 18
            p.rounded_rectangle(
                (x, y, x + 205, y + 280),
                30,
                fill=(*rgb, 26 + i * 13),
                outline=(*rgb, 180),
                width=3,
            )
            p.line(
                (x + 20, y + 20, x + 180, y + 20), fill=(255, 255, 255, 220), width=4
            )
    else:
        for i in range(5):
            x = 160 + i * 68
            y = 250 + i * 24
            p.ellipse(
                (x, y, 850 - i * 28, 650 - i * 22),
                outline=(*rgb, 70 + i * 30),
                width=13,
            )
        p.ellipse((470, 370, 620, 520), fill=(*rgb, 255))
    im = Image.alpha_composite(im.convert("RGBA"), art)
    d = ImageDraw.Draw(im)
    title = str(data.get("title") or "Контент со смыслом")[:70]
    ft = font(62, True)
    lines = wrap(d, title, ft, 850)
    if len(lines) > 3:
        ft = font(50, True)
        lines = wrap(d, title, ft, 850)
    y = 675
    for line in lines[:3]:
        d.text((100, y), line, fill="#24212f", font=ft)
        y += 76
    subtitle = str(data.get("subtitle", ""))[:120]
    for line in wrap(d, subtitle, font(25), 850)[:2]:
        d.text((102, y + 15), line, fill="#777086", font=font(25))
        y += 34
    d.line((100, 966, 980, 966), fill="#e9e5f2", width=2)
    d.text((100, 983), "AWESOME  /  AI MARKETING", fill="#8c859a", font=font(14))
    Path(sys.argv[1]).parent.mkdir(parents=True, exist_ok=True)
    im.convert("RGB").save(sys.argv[1], "PNG")


if __name__ == "__main__":
    main()
