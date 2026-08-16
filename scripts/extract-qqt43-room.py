#!/usr/bin/env python3
"""Extract original QQTang 4.3 room UI assets from the official installer pkg.

Source: QQTang4.3_Beta1Build2.EXE (Wayback Machine, dl_dir.qq.com/qqtangfile/),
NSIS archive containing data/object.idx + data/object.pkg (zlib-per-file pack).

DIMG layout (from qqt_map_editor_fin lib/QQFDIMG.cpp):
  0x00  "QQF\x1a" "DIMG"
  0x08  i16 version (0 = RGB565 + alpha 0..32, 1 = BGRA8888)
  0x0a  skip i16 + i32
  0x10  u32 nFrames
  0x14  u32 nDirections
  0x18  i32 xOffset, 0x1c i32 yOffset
  0x20  i32 canvasW, 0x24 i32 canvasH
  per frame: skip i32, i32 frameX, i32 frameY, skip i32,
             i32 w, i32 h, skip i32, pixels
"""
import struct
import sys
import zlib
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
PKG_DIR = REPO / ".vendor/QQTang-4.3-official/data"
OUT_BASE = REPO / "QQTangMac/Resources/Legacy43"


def load_pack():
    idx = (PKG_DIR / "object.idx").read_bytes()
    pkg = (PKG_DIR / "object.pkg").read_bytes()
    _, count, _, _ = struct.unpack_from("<IIII", idx, 0)
    pos = 16
    entries = {}
    for _ in range(count):
        (nlen,) = struct.unpack_from("<H", idx, pos)
        pos += 2
        name = idx[pos:pos + nlen].decode("gbk", errors="replace")
        pos += nlen
        _, off, usize, csize = struct.unpack_from("<IIII", idx, pos)
        pos += 16
        entries[name.replace("\\", "/").lower()] = (off, usize, csize)
    return entries, pkg


def read_img(entries, pkg, name):
    key = name.lower()
    if key not in entries:
        return None
    off, usize, csize = entries[key]
    data = zlib.decompress(pkg[off:off + csize])
    assert len(data) == usize, name
    return data


def decode_frames(data):
    assert data[:8] == b"QQF\x1aDIMG", "bad magic"
    (version,) = struct.unpack_from("<h", data, 8)
    n_frames, n_dirs = struct.unpack_from("<II", data, 0x10)
    hdr_x, hdr_y, canvas_w, canvas_h = struct.unpack_from("<iiii", data, 0x18)
    pos = 0x28
    frames = []
    for _ in range(max(1, n_frames)):
        _, fx, fy, _, w, h, _ = struct.unpack_from("<iiiiiii", data, pos)
        pos += 28
        rgba = bytearray(w * h * 4)
        if version == 0:
            color = data[pos:pos + w * h * 2]
            alpha = data[pos + w * h * 2:pos + w * h * 3]
            pos += w * h * 3
            for i in range(w * h):
                v = color[i * 2] | (color[i * 2 + 1] << 8)
                rgba[i * 4] = ((v >> 11) & 0x1F) * 255 // 31
                rgba[i * 4 + 1] = ((v >> 5) & 0x3F) * 255 // 63
                rgba[i * 4 + 2] = (v & 0x1F) * 255 // 31
                rgba[i * 4 + 3] = min(alpha[i] * 8, 255)
        elif version == 1:
            src = data[pos:pos + w * h * 4]
            pos += w * h * 4
            for i in range(w * h):
                rgba[i * 4] = src[i * 4 + 2]
                rgba[i * 4 + 1] = src[i * 4 + 1]
                rgba[i * 4 + 2] = src[i * 4]
                rgba[i * 4 + 3] = src[i * 4 + 3]
        else:
            raise ValueError(f"unsupported DIMG version {version}")
        frames.append({
            "x": fx - hdr_x, "y": fy - hdr_y,
            "w": w, "h": h, "rgba": rgba,
        })
    return {"canvas": (canvas_w, canvas_h), "frames": frames}


def compose(canvas, frame):
    cw, ch = canvas
    out = bytearray(cw * ch * 4)
    fx, fy, w, h, rgba = frame["x"], frame["y"], frame["w"], frame["h"], frame["rgba"]
    for row in range(h):
        dy = fy + row
        if not 0 <= dy < ch:
            continue
        for col in range(w):
            dx = fx + col
            if not 0 <= dx < cw:
                continue
            si = (row * w + col) * 4
            di = (dy * cw + dx) * 4
            out[di:di + 4] = rgba[si:si + 4]
    return out


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
    png += chunk(b"IDAT", zlib.compress(raw, 9))
    png += chunk(b"IEND", b"")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(png)


# (source path in pkg, output stem, compose onto canvas?)
EXPORTS = [
    ("object/ui/bg/bg_room.img", "Room/background", False),
    ("object/ui/room/btn_selMode.img", "Room/selMode", True),
    ("object/ui/room/btn_roomProp.img", "Room/roomProp", True),
    ("object/ui/room/btn_selMap.img", "Room/selMap", True),
    ("object/ui/room/btn_start.img", "Room/start", True),
    ("object/ui/room/btn_ready.img", "Room/ready", True),
    ("object/ui/room/btn_unready.img", "Room/unready", True),
    ("object/ui/common/btn_return.img", "Room/return", True),
    ("object/ui/room/btn_playerUp.img", "Room/playerUp", True),
    ("object/ui/room/btn_playerDown.img", "Room/playerDown", True),
    ("object/ui/room/teamColorStrip.img", "Room/teamColorStrip", True),
    ("object/ui/room/btn_check.img", "Room/check", True),
    ("object/ui/room/img_master.img", "Room/master", True),
    ("object/ui/room/img_ready.img", "Room/ready-mark", True),
    ("object/ui/room/img_roomStop.img", "Room/room-stop", True),
    ("object/ui/room/tab_normalChar.img", "Room/tab-normalChar", True),
    ("object/ui/room/tab_vipChar.img", "Room/tab-vipChar", True),
    ("object/ui/room/btn_leftRole.img", "Room/leftRole", True),
    ("object/ui/room/btn_rightRole.img", "Room/rightRole", True),
    ("object/ui/room/btn_storage.img", "Room/storage", True),
    ("object/ui/room/btn_equipItem.img", "Room/equipItem", True),
    ("object/ui/room/equipTip.img", "Room/equipTip", True),
    ("object/ui/room/mask_equip.img", "Room/mask-equip", True),
    ("object/ui/room/dlg_selMode.img", "Room/select-mode-dialog", False),
    ("object/ui/room/dlg_roomProp.img", "Room/room-property-dialog", False),
    ("object/ui/room/dlg_selMap.img", "Room/select-map-dialog", False),
    ("object/ui/common/number4.img", "Room/num4", True),
    ("object/ui/common/img_chat.img", "Room/chat-bubble", True),
    ("object/ui/chatRoom/dlg_chatAreaSmall.img", "Room/chat-small", False),
    ("object/ui/chatRoom/dlg_chatAreaBig.img", "Room/chat-big", False),
    ("object/ui/chat/btn_send.img", "Room/send", True),
    ("object/ui/selRoom/dlg_room.img", "Lobby/room", True),
]

# uiRoom.pyc RoleNameList：普通 15 + VIP 4
CHAR_ICONS = [
    "random", "boy", "girl", "xwk", "tt", "xq", "cl", "bbl",
    "hy", "mly", "hwz", "yd", "ld", "ge", "tb",
    "kl", "nz", "wll", "yy",
]

# 大立绘 + 名牌（悬停提示 roleTip 用）
CHAR_PORTRAITS = [
    "boy", "girl", "xwk", "tt", "xq", "cl", "bbl",
    "hy", "mly", "hwz", "yd", "ld", "ge", "tb",
    "kl", "nz", "wll", "yy",
]

VIP_ICONS: list[int] = []

MID_ICONS = [
    "all", "rand", "normal", "pig", "match", "town", "snow", "desert",
    "mine", "water", "field", "bomb", "bun", "treasure", "sculpture",
    "box", "tank",
]


def main():
    entries, pkg = load_pack()
    exports = list(EXPORTS)
    exports += [
        (f"object/ui/room/char/icon_{n}.img", f"Room/charIcon-{n}", True)
        for n in CHAR_ICONS
    ]
    exports += [
        (f"object/ui/room/char/{n}.img", f"Room/charBig-{n}", True)
        for n in CHAR_PORTRAITS
    ]
    exports += [
        (f"object/ui/room/char/name_{n}.img", f"Room/charName-{n}", True)
        for n in CHAR_PORTRAITS
    ]
    exports += [
        (f"object/ui/room/char/icon_{n}.img", f"Room/charIcon-vip{n}", True)
        for n in VIP_ICONS
    ]
    exports += [
        (f"object/ui/map/midIcon/{n}.img", f"Room/midIcon-{n}", True)
        for n in MID_ICONS
    ]

    for src, stem, on_canvas in exports:
        raw = read_img(entries, pkg, src)
        if raw is None:
            print(f"MISS {src}")
            continue
        img = decode_frames(raw)
        cw, ch = img["canvas"]
        frames = img["frames"]
        for index, frame in enumerate(frames):
            if on_canvas:
                rgba, w, h = compose((cw, ch), frame), cw, ch
            else:
                rgba, w, h = frame["rgba"], frame["w"], frame["h"]
            suffix = f"-{index}" if len(frames) > 1 else ""
            out = OUT_BASE / f"{stem}{suffix}.png"
            write_png(out, w, h, rgba)
        print(f"OK   {src} -> {stem} frames={len(frames)} canvas={cw}x{ch}")


if __name__ == "__main__":
    sys.exit(main())
