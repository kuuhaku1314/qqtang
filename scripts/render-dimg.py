#!/usr/bin/env python3
"""Render QQTang DIMG (.img) files to PNG for analysis, dumping header fields."""
import struct
import sys
import zlib

def u32(b, o):
    return struct.unpack_from("<I", b, o)[0]

def u16(b, o):
    return struct.unpack_from("<H", b, o)[0]

def dump_header(data, path):
    print(f"== {path} size={len(data)}")
    for off in range(8, 0x48, 4):
        print(f"  +0x{off:02x}: {u32(data, off):>10d} (0x{u32(data, off):08x})")

def write_png(path, width, height, rgba):
    def chunk(tag, payload):
        out = struct.pack(">I", len(payload)) + tag + payload
        out += struct.pack(">I", zlib.crc32(tag + payload) & 0xFFFFFFFF)
        return out
    raw = b"".join(
        b"\x00" + bytes(rgba[y * width * 4:(y + 1) * width * 4])
        for y in range(height)
    )
    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(raw, 6))
    png += chunk(b"IEND", b"")
    with open(path, "wb") as f:
        f.write(png)

def rgb565(v):
    r = ((v >> 11) & 0x1F) * 255 // 31
    g = ((v >> 5) & 0x3F) * 255 // 63
    b = (v & 0x1F) * 255 // 31
    return r, g, b

def decode(data, out_path, verbose=True):
    if verbose:
        dump_header(data, out_path)
    canvas_w, canvas_h = u32(data, 0x20), u32(data, 0x24)
    fmt = u32(data, 0x34)
    frame_w, frame_h = u32(data, 0x38), u32(data, 0x3C)

    def sane(v):
        return v if 1 <= v <= 4096 else None

    w = sane(frame_w) or sane(canvas_w)
    h = sane(frame_h) or sane(canvas_h)
    if not w or not h:
        print("  !! bad dims")
        return
    packed = w * 2
    # +0x40 looks aligned, but v0 pixel planes are tightly packed.
    stride = packed
    pixoff = 68
    color_bytes = stride * h
    alphaoff = pixoff + color_bytes
    alpha_n = w * h
    has_alpha = len(data) >= alphaoff + alpha_n
    scale = 1
    if has_alpha:
        mx = max(data[alphaoff:alphaoff + min(alpha_n, 4096)])
        scale = 8 if (fmt != 8 and mx <= 32) else 1
    rgba = bytearray(w * h * 4)
    for y in range(h):
        row = pixoff + y * stride
        arow = alphaoff + y * w
        for x in range(w):
            v = data[row + x * 2] | (data[row + x * 2 + 1] << 8)
            r, g, b = rgb565(v)
            a = 255
            if has_alpha:
                a = min(data[arow + x] * scale, 255)
            i = (y * w + x) * 4
            rgba[i:i + 4] = bytes((r, g, b, a))
    write_png(out_path, w, h, rgba)
    leftover = len(data) - (alphaoff + alpha_n if has_alpha else alphaoff)
    print(f"  -> {out_path}  {w}x{h} stride={stride} fmt={fmt} alpha={has_alpha} scale={scale} leftover={leftover}")

if __name__ == "__main__":
    for p in sys.argv[1:]:
        with open(p, "rb") as f:
            data = f.read()
        name = p.replace("/", "_").replace(".img", "") + ".png"
        decode(data, "/tmp/dimg/" + name.split("_client-patched_")[-1])
