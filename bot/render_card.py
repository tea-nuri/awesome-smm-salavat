"""Deterministic PNG card rendered from the AI's art direction, no external API."""
import json, math, re, sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont, ImageFilter

def font(size, bold=False):
    choices = [
        '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf' if bold else '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',
        '/System/Library/Fonts/Supplemental/Arial Bold.ttf' if bold else '/System/Library/Fonts/Supplemental/Arial.ttf',
    ]
    for path in choices:
        if Path(path).exists(): return ImageFont.truetype(path, size)
    return ImageFont.load_default()

def wrap(draw, text, ft, width):
    lines, current = [], ''
    for word in text.split():
        trial = (current + ' ' + word).strip()
        if draw.textlength(trial, font=ft) > width and current:
            lines.append(current); current = word
        else: current = trial
    if current: lines.append(current)
    return lines

def main():
    data = json.load(sys.stdin)
    accent = data.get('accent', '#6948ff')
    if not re.fullmatch(r'#[0-9A-Fa-f]{6}', accent): accent = '#6948ff'
    rgb = tuple(int(accent[i:i+2],16) for i in (1,3,5))
    im = Image.new('RGB', (1080,1080), '#f2f0ff'); d = ImageDraw.Draw(im)
    d.rounded_rectangle((55,55,1025,1025), radius=38, fill='#ffffff')
    d.text((100,100), str(data.get('company','AWESOME'))[:40].upper(), fill='#353044', font=font(22, True))
    d.rounded_rectangle((100,164,247,200),18,fill='#f0ecff'); d.text((117,170),'AI / STUDIO',fill=rgb,font=font(15,True))
    art = Image.new('RGBA', im.size, (0,0,0,0)); p = ImageDraw.Draw(art)
    motif=data.get('motif','orbit')
    if motif=='waves':
        for i in range(8):
            pts=[(70+x, 460+math.sin(x/125+i*.4)*110+i*23) for x in range(950)]
            p.line(pts,fill=(*rgb,180-i*15),width=14)
    elif motif=='grid':
        for i in range(5):
            for j in range(4):
                x=150+i*160;y=280+j*85
                p.rounded_rectangle((x,y,x+135,y+65),20,fill=(*rgb,35+i*22),outline=(*rgb,150),width=2)
    elif motif=='glass':
        for i in range(5):
            x=230+i*105;y=280+i*18
            p.rounded_rectangle((x,y,x+205,y+280),30,fill=(*rgb,26+i*13),outline=(*rgb,180),width=3)
            p.line((x+20,y+20,x+180,y+20),fill=(255,255,255,220),width=4)
    else:
        for i in range(5):
            x=160+i*68;y=250+i*24
            p.ellipse((x,y,850-i*28,650-i*22),outline=(*rgb,70+i*30),width=13)
        p.ellipse((470,370,620,520),fill=(*rgb,255))
    im=Image.alpha_composite(im.convert('RGBA'),art);d=ImageDraw.Draw(im)
    title=str(data.get('title') or 'Контент со смыслом')[:70];ft=font(62,True);lines=wrap(d,title,ft,850)
    if len(lines)>3: ft=font(50,True);lines=wrap(d,title,ft,850)
    y=675
    for line in lines[:3]: d.text((100,y),line,fill='#24212f',font=ft);y+=76
    subtitle=str(data.get('subtitle',''))[:120]
    for line in wrap(d,subtitle,font(25),850)[:2]:d.text((102,y+15),line,fill='#777086',font=font(25));y+=34
    d.line((100,966,980,966),fill='#e9e5f2',width=2);d.text((100,983),'AWESOME  /  AI MARKETING',fill='#8c859a',font=font(14))
    Path(sys.argv[1]).parent.mkdir(parents=True,exist_ok=True);im.convert('RGB').save(sys.argv[1],'PNG')
if __name__=='__main__': main()
