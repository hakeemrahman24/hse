#!/usr/bin/env python3
"""Rebuilds the two fonts the SOFI PDF report loads on demand (see static/sofi-report.js).

    pip install fonttools
    python3 build-report-fonts.py [--big5] [CJK_TTC [ARABIC_TTF]]

Run it from this folder. It writes:

  NotoSansArabic-Regular.ttf  a plain copy of Noto Sans Arabic Regular (235 KB, TrueType already).
  NotoSansCJK-Report.ttf      Noto Sans CJK JP Regular cut down to the characters people write in
                              names (about 3.7 MB; 5.9 MB with --big5), converted to TrueType.

Why the CJK font is rebuilt and not just copied:
  * jsPDF can only embed TrueType outlines. Noto Sans CJK ships as CFF (OpenType), which it cannot
    read, so the outlines are converted (cubic to quadratic curves, error 1 unit of 1000).
  * The full font is about 18 MB. The browser downloads it the first time a report contains CJK
    text, so it is cut down to: kana, CJK punctuation and full-width forms, plus every character of
    the national character sets GB 2312 (Simplified Chinese), JIS X 0208 (Japanese) and
    KS X 1001 (Korean, hangul and hanja). --big5 adds Big5 for Traditional Chinese (2.2 MB more).
  * Plain ASCII (space, digits, brackets) is kept on purpose. A name such as "PT Foo (中国)" is drawn
    in runs, and jsPDF drops the rest of a run at the first character its font does not have.

The pan-CJK glyph set is the same in the JP, KR, SC and TC faces, so one face covers all of them
(Han characters use the Japanese forms). Licenses: OFL-Noto-report-fonts.txt.
"""
import io
import shutil
import sys

from fontTools.pens.cu2quPen import Cu2QuPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.subset import Options, Subsetter
from fontTools.ttLib import TTCollection, TTFont, newTable

CJK_TTC = "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc"
ARABIC_TTF = "/usr/share/fonts/noto/NotoSansArabic-Regular.ttf"
CJK_OUT = "NotoSansCJK-Report.ttf"
ARABIC_OUT = "NotoSansArabic-Regular.ttf"
CURVE_ERROR = 1.0  # font units; the font is 1000 units per em


def charset(codec: str) -> set[int]:
    """Every CJK character a legacy double-byte encoding can hold."""
    found = set()
    for lead in range(0x81, 0xFF):
        for trail in range(0x40, 0xFF):
            try:
                ch = bytes([lead, trail]).decode(codec)
            except UnicodeDecodeError:
                continue
            if len(ch) == 1 and ord(ch) > 0x2E80:
                found.add(ord(ch))
    return found


def wanted_codepoints(with_big5: bool) -> set[int]:
    cps = set(range(0x20, 0x7F))  # ASCII, see the docstring
    for lo, hi in [(0x2010, 0x2027), (0x3000, 0x303F), (0x3040, 0x309F), (0x30A0, 0x30FF), (0x3100, 0x312F), (0xFF00, 0xFFEF)]:
        cps |= set(range(lo, hi + 1))
    for codec in ["gb2312", "shift_jis", "euc_kr"] + (["big5"] if with_big5 else []):
        cps |= charset(codec)
    return cps


def cff_to_truetype(font: TTFont) -> None:
    """Replaces the CFF outlines with TrueType ones, in place."""
    order = font.getGlyphOrder()
    glyph_set = font.getGlyphSet()
    glyf = newTable("glyf")
    glyf.glyphOrder = order
    glyf.glyphs = {}
    for name in order:
        pen = TTGlyphPen(glyph_set)
        glyph_set[name].draw(Cu2QuPen(pen, CURVE_ERROR, reverse_direction=True))
        glyf[name] = pen.glyph()
    font["loca"] = newTable("loca")
    font["glyf"] = glyf
    for tag in ("CFF ", "VORG"):
        if tag in font:
            del font[tag]
    maxp = font["maxp"]
    maxp.tableVersion = 0x00010000
    for attr in ("maxZones", "maxTwilightPoints", "maxStorage", "maxFunctionDefs", "maxInstructionDefs",
                 "maxStackElements", "maxSizeOfInstructions", "maxComponentElements", "maxPoints", "maxContours",
                 "maxCompositePoints", "maxCompositeContours", "maxComponentDepth"):
        setattr(maxp, attr, 0)
    maxp.maxZones = 1
    font["head"].glyphDataFormat = 0
    post = font["post"]
    post.formatType = 2.0
    post.extraNames = []
    post.mapping = {}
    post.glyphOrder = order
    font.sfntVersion = "\x00\x01\x00\x00"


def build_cjk(ttc_path: str, with_big5: bool) -> None:
    font = TTCollection(ttc_path).fonts[0]  # the JP face; same glyph set as the others
    options = Options()
    options.layout_features = ["*"]
    options.notdef_outline = True
    options.name_IDs = ["*"]
    options.drop_tables += ["GSUB", "GPOS", "GDEF", "BASE", "vhea", "vmtx", "JSTF", "DSIG"]
    subsetter = Subsetter(options)
    subsetter.populate(unicodes=wanted_codepoints(with_big5))
    subsetter.subset(font)
    # Re-read the subset so the glyph set is the small one, then convert.
    buf = io.BytesIO()
    font.save(buf)
    font = TTFont(io.BytesIO(buf.getvalue()))
    cff_to_truetype(font)
    font.save(CJK_OUT)


def main(argv: list[str]) -> None:
    with_big5 = "--big5" in argv
    paths = [a for a in argv if not a.startswith("--")]
    ttc = paths[0] if len(paths) > 0 else CJK_TTC
    arabic = paths[1] if len(paths) > 1 else ARABIC_TTF
    build_cjk(ttc, with_big5)
    shutil.copyfile(arabic, ARABIC_OUT)
    print("wrote", CJK_OUT, "and", ARABIC_OUT)


if __name__ == "__main__":
    main(sys.argv[1:])
