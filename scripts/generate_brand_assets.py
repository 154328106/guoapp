import argparse
import io
import json
import math
import shutil
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont, ImageStat

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(
    description='从统一图形生成果子熟了 / 果子黄了平台资源；需要 Pillow（用 SVG 时另需 resvg-py）。'
                '两个版本图标分开：默认生成果子熟了，--all-sources 生成果子黄了（只生成安卓、iOS 和电视横幅）。')
parser.add_argument('--output', type=Path, default=root)
parser.add_argument('--font', type=Path, default=Path('/System/Library/Fonts/PingFang.ttc'))
parser.add_argument('--all-sources', action='store_true', help='生成果子黄了（全站源版）的图标')
options = parser.parse_args()
output = options.output

if options.all_sources:
    display = '果子黄了'
    raster = root / 'scripts/brand_icon_all_sources.png'
    ios_set = 'AppIconAllSources'      # build_ios.py --all-sources 构建时换进 AppIcon
    launcher = 'ic_launcher_all_sources'  # build.gradle.kts 里按 ALL_SOURCES 选
    banner_name = 'tv_banner_all_sources'
else:
    display = '果子熟了'
    raster = root / 'scripts/brand_icon.png'
    ios_set = 'AppIcon'
    launcher = 'ic_launcher'
    banner_name = 'tv_banner'

if raster.exists():
    # 位图图标：1024 满幅不透明方图（单色图形 + 纯色/渐变底）。
    # 安卓自适应图标需要「前景层 + 背景色」：底色取四周边框的平均色，按每个像素离底色的距离抠出图形，
    # 图形颜色取离底色最远那批像素的平均色。白色、黄色等任意颜色的图形都适用。
    icon = Image.open(raster).convert('RGB').resize((1024, 1024), Image.Resampling.LANCZOS)
    border = Image.new('L', (1024, 1024), 255)
    ImageDraw.Draw(border).rectangle((24, 24, 999, 999), fill=0)
    background_color = tuple(round(c) for c in ImageStat.Stat(icon, border).mean)
    pixels = icon.load()
    distance = Image.new('L', (1024, 1024))
    far = distance.load()
    for y in range(1024):
        for x in range(1024):
            r, g, b = pixels[x, y]
            far[x, y] = min(255, round(math.dist((r, g, b), background_color)))
    peak = distance.getextrema()[1]
    glyph_mask = distance.point(lambda v: 255 if v >= peak * .8 else 0)
    glyph_color = tuple(round(c) for c in ImageStat.Stat(icon, glyph_mask).mean)
    span = max(1.0, math.dist(glyph_color, background_color))
    floor = min(24.0, span / 4)  # 渐变底色的轻微起伏不算图形
    alpha = distance.point(lambda v: max(0, min(255, round((v - floor) * 255 / max(1.0, span - floor)))))
    foreground = Image.new('RGBA', (1024, 1024), glyph_color + (0,))
    foreground.putalpha(alpha)
    silhouette = Image.new('RGBA', (1024, 1024), (255, 255, 255, 0))
    silhouette.putalpha(alpha)
    launcher_background = '#%02X%02X%02X' % background_color
    icon = icon.convert('RGBA')
else:
    import resvg_py
    source = (root / 'scripts/brand_icon.svg').read_text(encoding='utf-8')

    def render(svg):
        png = resvg_py.svg_to_bytes(svg_string=svg, width=1024, height=1024)
        return Image.open(io.BytesIO(bytes(png))).convert('RGBA')

    foreground = render(source)
    silhouette = render(source.replace(' filter="url(#shadow)"', ''))
    icon = Image.new('RGBA', (1024, 1024), '#CDE5F9')
    icon.alpha_composite(foreground)
    launcher_background = '#CDE5F9'
rounded = icon.copy()
corners = Image.new('L', (1024, 1024), 0)
ImageDraw.Draw(corners).rounded_rectangle((0, 0, 1023, 1023), radius=236, fill=255)
rounded.putalpha(corners)
icon = icon.convert('RGB')
adaptive = Image.new('RGBA', (1536, 1536))
adaptive.paste(foreground, (256, 256))
monochrome = Image.new('RGBA', (1536, 1536))
monochrome.paste(silhouette, (256, 256))


def save(image, name):
    destination = output / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    image.save(destination)


# iOS：果子黄了用单独的 AppIconAllSources（沿用 AppIcon 的 Contents.json）
ios_contents_path = root / 'ios/Runner/Assets.xcassets/AppIcon.appiconset/Contents.json'
contents = json.loads(ios_contents_path.read_text())
ios_dir = f'ios/Runner/Assets.xcassets/{ios_set}.appiconset/'
if ios_set != 'AppIcon':
    (output / ios_dir).mkdir(parents=True, exist_ok=True)
    shutil.copy2(ios_contents_path, output / ios_dir / 'Contents.json')
for entry in contents['images']:
    if 'filename' in entry:
        size = round(float(entry['size'].split('x')[0]) * float(entry['scale'].rstrip('x')))
        save(icon.resize((size, size), Image.Resampling.LANCZOS), ios_dir + entry['filename'])

if not options.all_sources:
    # Windows / macOS 两个版本仍共用果子熟了的图标
    save(rounded.resize((256, 256), Image.Resampling.LANCZOS), 'windows/runner/resources/app_icon.ico')
    macos_canvas = Image.new('RGBA', (1024, 1024))
    macos_badge = rounded.resize((824, 824), Image.Resampling.LANCZOS)
    macos_canvas.paste(macos_badge, (100, 100), macos_badge)
    macos_contents = json.loads((root / 'macos/Runner/Assets.xcassets/AppIcon.appiconset/Contents.json').read_text())
    for entry in macos_contents['images']:
        if 'filename' in entry:
            size = round(float(entry['size'].split('x')[0]) * float(entry['scale'].rstrip('x')))
            save(macos_canvas.resize((size, size), Image.Resampling.LANCZOS),
                 'macos/Runner/Assets.xcassets/AppIcon.appiconset/' + entry['filename'])

for density, scale in [('mdpi', 1), ('hdpi', 1.5), ('xhdpi', 2), ('xxhdpi', 3), ('xxxhdpi', 4)]:
    legacy = round(48 * scale)
    layer = round(108 * scale)
    save(rounded.resize((legacy, legacy), Image.Resampling.LANCZOS),
         f'android/app/src/main/res/mipmap-{density}/{launcher}.png')
    save(adaptive.resize((layer, layer), Image.Resampling.LANCZOS),
         f'android/app/src/main/res/mipmap-{density}/{launcher}_foreground.png')
    save(monochrome.resize((layer, layer), Image.Resampling.LANCZOS),
         f'android/app/src/main/res/mipmap-{density}/{launcher}_monochrome.png')
anydpi = output / f'android/app/src/main/res/mipmap-anydpi-v26/{launcher}.xml'
anydpi.parent.mkdir(parents=True, exist_ok=True)
anydpi.write_text(
    '<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">\n'
    f'    <background android:drawable="@color/{launcher}_background"/>\n'
    f'    <foreground android:drawable="@mipmap/{launcher}_foreground"/>\n'
    f'    <monochrome android:drawable="@mipmap/{launcher}_monochrome"/>\n'
    '</adaptive-icon>\n', encoding='utf-8')
background_xml = output / f'android/app/src/main/res/values/{launcher}_background.xml'
background_xml.write_text(
    '<resources>\n'
    f'    <color name="{launcher}_background">{launcher_background}</color>\n'
    '</resources>\n', encoding='utf-8')

font_path = options.font
if not font_path.exists():
    for candidate in ('/System/Library/Fonts/Hiragino Sans GB.ttc',
                      '/System/Library/Fonts/STHeiti Medium.ttc',
                      'C:/Windows/Fonts/msyhbd.ttc', 'C:/Windows/Fonts/msyh.ttc'):
        if Path(candidate).exists():
            font_path = Path(candidate)
            break
font = ImageFont.truetype(str(font_path), 76)
banner = Image.new('RGB', (640, 360), '#F5F9FC')
badge = rounded.resize((180, 180), Image.Resampling.LANCZOS)
banner.paste(badge, (44, 90), badge)
draw = ImageDraw.Draw(banner)
draw.text((255, 128), display, font=font, fill='#3B4552')
save(banner, f'android/app/src/main/res/drawable-xhdpi/{banner_name}.png')
print(f'{display} 图标已生成；安卓自适应图标背景色', launcher_background)
