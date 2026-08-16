#!/usr/bin/env python3
import json
import re
import sys
from pathlib import Path

def parse_map_desc(path: Path) -> list[dict]:
    text = path.read_bytes().decode("gbk", errors="replace")
    pattern = re.compile(
        r"\(\s*(\d+)\s*,\s*'([^']*)'\s*,\s*'([^']*)'\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*'([^']*)'\s*,\s*'([^']*)'\s*,\s*'([^']*)'",
    )
    entries = []
    for match in pattern.finditer(text):
        entries.append(
            {
                "id": int(match.group(1)),
                "type": match.group(2),
                "name": match.group(3),
                "width": int(match.group(4)),
                "height": int(match.group(5)),
                "maxPlayers": int(match.group(6)),
                "mapFile": match.group(7),
                "previewFile": match.group(8),
                "bgmFile": match.group(9),
            }
        )
    return entries

def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit("usage: export-map-catalog.py mapDesc.py maps.json")
    source = Path(sys.argv[1])
    target = Path(sys.argv[2])
    entries = parse_map_desc(source)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        json.dumps({"schemaVersion": 1, "maps": entries}, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    print(f"Exported {len(entries)} maps -> {target}")

if __name__ == "__main__":
    main()
