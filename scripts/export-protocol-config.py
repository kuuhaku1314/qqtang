#!/usr/bin/env python3
import json
import sys
from pathlib import Path

def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit("usage: export-protocol-config.py server-directory-local-ui.json out.json")
    source = Path(sys.argv[1])
    target = Path(sys.argv[2])
    payload = json.loads(source.read_text(encoding="utf-8"))
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Exported protocol config -> {target}")

if __name__ == "__main__":
    main()
