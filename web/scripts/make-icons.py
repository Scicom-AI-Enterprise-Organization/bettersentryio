#!/usr/bin/env python3
"""Generate the favicon set from the bettersentryio brand mark.

Run from the repo root after changing the mark:

    python3 web/scripts/make-icons.py

Writes web/src/app/favicon.ico and web/src/app/apple-icon.png. The hand-written
web/src/app/icon.svg is the same mark and is what modern browsers actually use —
these two exist for Safari (which ignores SVG favicons) and for iOS home screens.
Checked in alongside the assets so the icon stays reproducible rather than becoming
a binary nobody can edit.

The mark is the rail mark's distinctive half: three Scicom-orange dots on a neutral
chip. Two decisions were made by looking at the output rather than by reasoning:

  * The dots need ~1.5px of clear space between them at 16px. The first attempt
    spaced them 0.7px apart and they merged into one orange bar.
  * The chip is #22242a, not the theme's near-black #1a1b1f. Near-black disappears
    into a dark tab strip, leaving three dots with no container.

The rail mark's two rules are dropped below 32px, where a 1px stroke under three
dots is mud.
"""

from PIL import Image, ImageDraw

ORANGE = (243, 106, 16, 255)  # #f36a10, the Scicom orange in rail-mark.tsx
CHIP = (34, 36, 42, 255)      # light enough to hold its shape on a dark tab strip
RULE = (235, 235, 235, 190)   # the rail mark's stroke-foreground/70

SS = 8  # supersample factor; PIL has no rasteriser, so draw big and downsample


def mark(size: int, underline: bool) -> Image.Image:
    S = size * SS
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    d.rounded_rectangle([0, 0, S - 1, S - 1], radius=int(S * 0.22), fill=CHIP)

    # Without the rules the dots take the whole chip and can afford wider gaps.
    cy = S * 0.40 if underline else S * 0.5
    r = S * (0.088 if underline else 0.094)
    gap = S * (0.255 if underline else 0.283)
    for cx in (S / 2 - gap, S / 2, S / 2 + gap):
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=ORANGE)

    if underline:
        w = int(S * 0.062)
        d.line([S * 0.235, S * 0.655, S * 0.765, S * 0.655], fill=RULE, width=w)
        d.line([S * 0.235, S * 0.80, S * 0.60, S * 0.80], fill=RULE, width=w)

    return img.resize((size, size), Image.LANCZOS)


def write_ico(path: str, frames: list[Image.Image]) -> None:
    """Assemble a PNG-payload .ico by hand.

    Pillow's ICO writer takes one image and downscales it to every requested size,
    which would force the 16px frame to be a shrunken copy of the 256px one, rules
    and all. Writing the container directly keeps each frame's own drawing: a
    6-byte ICONDIR, one 16-byte ICONDIRENTRY per frame, then the payloads.
    """
    import io
    import struct

    blobs = []
    for f in frames:
        buf = io.BytesIO()
        f.save(buf, format="PNG", optimize=True)
        blobs.append(buf.getvalue())

    offset = 6 + 16 * len(frames)
    out = bytearray(struct.pack("<HHH", 0, 1, len(frames)))
    for f, blob in zip(frames, blobs):
        # A zero in the size byte means 256; the field is only one byte wide.
        w = 0 if f.width >= 256 else f.width
        h = 0 if f.height >= 256 else f.height
        out += struct.pack("<BBBBHHII", w, h, 0, 0, 1, 32, len(blob), offset)
        offset += len(blob)
    for blob in blobs:
        out += blob
    with open(path, "wb") as fh:
        fh.write(bytes(out))


def main() -> None:
    import os

    app = os.path.join(os.path.dirname(__file__), "..", "src", "app")
    frames = [mark(s, underline=s >= 32) for s in (16, 24, 32, 48, 64, 128, 256)]
    write_ico(os.path.join(app, "favicon.ico"), frames)
    mark(180, underline=True).save(os.path.join(app, "apple-icon.png"), optimize=True)
    print(f"favicon.ico ({len(frames)} frames) and apple-icon.png written to src/app/")


if __name__ == "__main__":
    main()
