#!/usr/bin/env python3
"""Download the preserved QQTang 4.3 UI assets used by the native client.

The QQTang-Local runtime is intentionally stripped and omits most object/ui
files.  The historical UI mirror at qqt.95hyc.cn exposes PNG conversions of
the same assets.  This importer keeps runtime fully offline by bundling a
versioned, checksummed subset required by the Mac client.
"""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


ROOT = "https://qqt.95hyc.cn"
MAP_EDITOR_ROOT = (
    "https://raw.githubusercontent.com/brangpd/qqt_map_editor_fin/"
    "eb6765e211100f9117cba45da131455e233bd85b/mapElem"
)
TARGET = Path(__file__).resolve().parents[1] / "QQTangMac/Resources/Legacy43"


@dataclass(frozen=True)
class Asset:
    local: str
    remote: str
    required: bool = True


def shared_assets() -> list[Asset]:
    values = [
        Asset("Common/message-box.png", "/source/3.0/common/dlg_msgBox_0_0.png"),
        Asset("Common/close-normal.png", "/source/3.0/common/btn_close_0_3.png"),
        Asset("Common/cancel-normal.png", "/source/3.0/common/btn_cancel_0_3.png"),
        Asset("Common/cancel-hover.png", "/source/3.0/common/btn_cancel_0_2.png"),
        Asset("Common/cancel-pressed.png", "/source/3.0/common/btn_cancel_0_0.png"),
        Asset("Common/confirm-normal.png", "/source/3.0/common/btn_confirm_0_3.png"),
    ]
    values.extend(
        Asset(
            f"Common/Numbers/{digit}.png",
            f"/source/3.0/common/number1_0_0/{digit}.png",
        )
        for digit in range(10)
    )
    for frame in (0, 1, 2, 3):
        values.append(
            Asset(
                f"Common/leave-{frame}.png",
                f"/ui/3.0/common/btn_leave_0_{frame}.png",
                required=False,
            )
        )
    return values


def login_assets() -> list[Asset]:
    values = [
        Asset("Login/background.png", "/source/bg_login_4.0-4.3.png"),
        Asset("Login/dialog.png", "/source/3.2/dlg_login_0.png"),
        Asset("Login/logo.png", "/source/4.3/img_logo_0.png"),
        Asset("Login/check-on.png", "/source/3.2/checkBox1.png"),
        Asset("Login/check-off.png", "/source/3.2/checkBox.png"),
        Asset("Login/agreement.png", "/source/3.2/text_xieyi.png"),
        Asset("Login/wait.png", "/source/3.0/login/dlg_wait_0_0.png"),
        Asset("Login/link.gif", "/source/3.0/login/img_link.gif"),
    ]
    states = {
        "login": (0, 1, 2),
        "quit": (0, 1, 2),
        "help": (0, 2, 3),
        "playAnim": (0, 1, 2),
    }
    for name, frames in states.items():
        for frame in frames:
            values.append(
                Asset(
                    f"Login/{name}-{frame}.png",
                    f"/source/3.0/login/btn_{name}_0_{frame}.png",
                )
            )
    return values


def section_assets() -> list[Asset]:
    base = "/subpage/selSect/source_selSect"
    values = [
        Asset("Section/background.png", f"{base}/bg_selSect_5.2.png"),
        Asset("Section/section-row.png", f"{base}/5.2/img_sect_0_0.png"),
        Asset("Section/scroll-thumb.png", f"{base}/5.2/scl_block_0_1.png"),
        Asset("Section/server-dialog.png", f"{base}/5.2/dlg_selServer_0_0.png"),
        Asset("Section/server-content.png", f"{base}/5.2/selServer_main.png"),
        Asset("Section/server-close.png", f"{base}/5.2/btn_cross_0_3.png"),
    ]
    tabs = ("practice", "greenhand", "freedom", "match", "party")
    for tab in tabs:
        for frame in (0, 1):
            values.append(
                Asset(
                    f"Section/tab-{tab}-{frame}.png",
                    f"{base}/5.2/tab_{tab}_0_{frame}.png",
                    required=False,
                )
            )
    buttons = ("practice", "selZone", "quickJoin", "sysSetup")
    for button in buttons:
        for frame in (0, 1, 2):
            values.append(
                Asset(
                    f"Section/{button}-{frame}.png",
                    f"{base}/5.2/btn_{button}_0_{frame}.png",
                    required=frame == 2,
                )
            )
    for frame in range(4):
        values.append(
            Asset(
                f"Section/quit-{frame}.png",
                f"{base}/5.2/btn_quit_0_{frame}.png",
                required=frame == 3,
            )
        )
    for state in range(1, 7):
        values.append(
            Asset(
                f"Section/state-{state}.png",
                f"{base}/5.2/img_state_{state}.png",
                required=False,
            )
        )
    return values


def lobby_assets() -> list[Asset]:
    ui = "/ui/3.0"
    values = [
        Asset("Lobby/background.png", f"{ui}/bg/bg_selRoom.png"),
        Asset("Lobby/chat-normal.png", f"{ui}/selRoom/dlg_chatNormal_0_0.png"),
        Asset("Lobby/chat-min.png", f"{ui}/selRoom/dlg_chatMin_0_0.png"),
        Asset("Lobby/chat-max.png", f"{ui}/selRoom/dlg_chatMax_0_0.png"),
        Asset("Lobby/map-filter.png", f"{ui}/selRoom/map/dlg_mapFilter_0_0.png"),
    ]
    for frame in (0, 1, 2):
        values.append(
            Asset(
                f"Lobby/room-{frame}.png",
                f"{ui}/selRoom/dlg_room_0_{frame}.png",
                required=False,
            )
        )
    names = (
        "tab_compete",
        "tab_explore",
        "tab_chat",
        "tab_noItem",
        "tab_item",
        "tab_honor",
        "tab_player",
        "tab_friend",
        "tab_allKinMember",
    )
    for name in names:
        for frame in (0, 1):
            values.append(
                Asset(
                    f"Lobby/{name}-{frame}.png",
                    f"{ui}/selRoom/{name}_0_{frame}.png",
                    required=False,
                )
            )
    button_frames = {
        "createRoom": (0, 1, 2),
        "shop": (0, 1, 2),
        "match": (0, 1, 2),
        "left": (0, 2, 3),
        "right": (0, 2, 3),
        "mapFilter": (0, 2),
        "flexDown": (0, 1, 2),
        "flexUp": (0, 1, 2),
    }
    for name, frames in button_frames.items():
        for frame in frames:
            values.append(
                Asset(
                    f"Lobby/{name}-{frame}.png",
                    f"{ui}/selRoom/btn_{name}_0_{frame}.png",
                    required=False,
                )
            )
    for name in ("practice", "quickJoin"):
        for frame in (0, 1, 2):
            values.append(
                Asset(
                    f"Lobby/{name}-{frame}.png",
                    f"{ui}/selSect/btn_{name}_0_{frame}.png",
                    required=False,
                )
            )
    return values


def room_assets() -> list[Asset]:
    ui = "/ui/3.0"
    values = [
        Asset("Room/background.png", f"{ui}/bg/bg_room.png"),
        Asset("Room/select-mode-dialog.png", f"{ui}/room/dlg_selMode.png"),
        Asset("Room/room-property-dialog.png", f"{ui}/room/dlg_roomProp.png"),
        Asset("Room/select-map-dialog.png", f"{ui}/room/dlg_selMap.png", required=False),
        Asset("Room/random-map.png", f"{ui}/map/rand_0.png"),
        Asset("Room/random-map-name.png", f"{ui}/map/rand_0_name.png"),
        Asset("Room/chat-small.png", f"{ui}/chatRoom/dlg_chatAreaSmall_0_0.png", required=False),
        Asset("Room/chat-big.png", f"{ui}/chatRoom/dlg_chatAreaBig_0_0.png", required=False),
        Asset("Room/ready-mark.png", f"{ui}/room/img_ready_0_0.png", required=False),
        Asset("Room/master-mark.png", f"{ui}/room/img_master_0_0.png", required=False),
    ]
    buttons = {
        "selMode": (0, 1, 2),
        "roomProp": (0, 1, 2),
        "selMap": (0, 1, 2),
        "ready": (0, 1, 2, 3),
        "start": (0, 1, 2, 3),
        "playerUp": (0, 1, 2, 3),
        "playerDown": (0, 1, 2, 3),
        "return": (0, 1, 2, 3),
        "unready": (0, 1, 2, 3),
        "leftRole": (0, 1, 2, 3, 4, 5, 6),
        "rightRole": (0, 1, 2, 3, 4, 5, 6),
    }
    for name, frames in buttons.items():
        folder = "common" if name == "return" else "room"
        for frame in frames:
            values.append(
                Asset(
                    f"Room/{name}-{frame}.png",
                    f"{ui}/{folder}/btn_{name}_0_{frame}.png",
                    required=False,
                )
            )
    return values


def game_assets() -> list[Asset]:
    ui = "/ui/3.0"
    values = [
        Asset("Game/status-bar.png", f"{ui}/game/dlg_statusBar.png", required=False),
        Asset("Game/player-list.png", f"{ui}/game/dlg_playerList.png", required=False),
        Asset("Game/pve-functions.png", f"{ui}/game/dlg_pveFunc.png", required=False),
        Asset("Game/item-mask.png", f"{ui}/game/img_itemMask.png", required=False),
        Asset("Game/mask-cycle.png", f"{ui}/game/img_maskCycle.png", required=False),
        Asset("Game/player-life.png", f"{ui}/game/img_playerLife.png", required=False),
        Asset(
            "Game/chat-input.png",
            f"{ui}/gameChat/liaotianshurukuang_di.png",
            required=False,
        ),
    ]
    # One verified historical character vertical slice. Additional characters
    # are imported only after this set passes the gameplay acceptance tests.
    for direction in ("down", "left", "right", "up"):
        for frame in range(1, 7):
            values.append(
                Asset(
                    f"Game/Player/n02-c1-{direction}-{frame}.png",
                    f"/ui/player/n02_c1_{direction}{frame}.png",
                    required=False,
                )
            )
    for frame in range(4):
        values.append(
            Asset(
                f"Game/leave-{frame}.png",
                f"{ui}/common/btn_leavegame_0_{frame}.png",
                required=False,
            )
        )
    return values


def map_assets() -> list[Asset]:
    stems = (
        "rand",
        "match01_2",
        "match02_2",
        "match03_2",
        "town01_4",
        "desert01_4",
        "pig01_4",
    )
    return [
        Asset(
            f"Map/{stem}.png",
            f"/ui/3.0/map/{stem}_0.png",
            required=False,
        )
        for stem in stems
    ]


def map_element_assets() -> list[Asset]:
    values = [
        Asset(
            f"Game/MapElements/pig/elem{element_id}_stand.img",
            f"{MAP_EDITOR_ROOT}/pig/elem{element_id}_stand.img",
        )
        for element_id in range(1, 15)
    ]
    values.extend(
        Asset(
            f"Game/MapElements/pig/elem{element_id}_die.img",
            f"{MAP_EDITOR_ROOT}/pig/elem{element_id}_die.img",
        )
        for element_id in (10, 11, 12)
    )
    return values


def download(asset: Asset) -> dict[str, object] | None:
    destination = TARGET / asset.local
    destination.parent.mkdir(parents=True, exist_ok=True)
    url = asset.remote if asset.remote.startswith(("http://", "https://")) else ROOT + asset.remote
    if destination.exists() and destination.stat().st_size > 0:
        payload = destination.read_bytes()
        return {
            "path": asset.local,
            "source": url,
            "bytes": len(payload),
            "sha256": hashlib.sha256(payload).hexdigest(),
        }
    temporary = destination.with_suffix(destination.suffix + ".download")
    result = subprocess.run(
        [
            "curl",
            "-fLsS",
            "--retry",
            "2",
            "--connect-timeout",
            "10",
            "--max-time",
            "30",
            "-A",
            "QQTangMac/0.1",
            "-o",
            str(temporary),
            url,
        ],
        check=False,
    )
    if result.returncode != 0:
        temporary.unlink(missing_ok=True)
        if not asset.required:
            print(f"optional missing: {asset.remote}", flush=True)
            return None
        raise RuntimeError(f"curl failed ({result.returncode}): {url}")
    payload = temporary.read_bytes()
    temporary.replace(destination)
    digest = hashlib.sha256(payload).hexdigest()
    print(f"{asset.local} ({len(payload)} bytes)", flush=True)
    return {
        "path": asset.local,
        "source": url,
        "bytes": len(payload),
        "sha256": digest,
    }


def main() -> None:
    assets = (
        shared_assets()
        + login_assets()
        + section_assets()
        + lobby_assets()
        + room_assets()
        + game_assets()
        + map_assets()
        + map_element_assets()
    )
    records = []
    for asset in assets:
        record = download(asset)
        if record is not None:
            records.append(record)
    manifest = {
        "schemaVersion": 1,
        "clientVersion": "4.3",
        "assetCount": len(records),
        "assets": records,
    }
    (TARGET / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    print(f"Imported {len(records)} assets into {TARGET}")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"asset import failed: {error}", file=sys.stderr)
        raise
