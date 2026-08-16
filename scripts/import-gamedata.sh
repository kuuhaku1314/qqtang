#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR_RAR="$ROOT/.vendor/QQTang-Local.rar"
VENDOR_DIR="$ROOT/.vendor/QQTang-Local"
CLIENT_SRC="$VENDOR_DIR/runtime/client-patched"
GAME_DATA="$ROOT/GameData/client-patched"
CATALOG="$ROOT/GameData/catalog"

if [[ ! -d "$CLIENT_SRC" ]]; then
  if [[ ! -f "$VENDOR_RAR" ]]; then
    echo "Downloading QQTang-Local.rar ..."
    mkdir -p "$ROOT/.vendor"
    curl -L --fail --retry 3 \
      -o "$VENDOR_RAR" \
      "https://github.com/kuuhaku1314/qqtang/raw/main/QQTang-Local.rar"
  fi
  echo "Extracting archive ..."
  command -v unar >/dev/null || { echo "Install unar: brew install unar"; exit 1; }
  unar -o "$ROOT/.vendor" "$VENDOR_RAR" >/dev/null
fi

echo "Linking GameData -> extracted client-patched ..."
mkdir -p "$ROOT/GameData"
rm -rf "$GAME_DATA"
ln -sfn "$CLIENT_SRC" "$GAME_DATA"

mkdir -p "$CATALOG"
python3 "$ROOT/scripts/export-map-catalog.py" \
  "$CLIENT_SRC/map/mapDesc.py" \
  "$CATALOG/maps.json"

python3 "$ROOT/scripts/export-protocol-config.py" \
  "$VENDOR_DIR/configs/server-directory-local-ui.json" \
  "$CATALOG/protocol-local-ui.json"

echo "GameData ready at $GAME_DATA"
echo "Catalog written to $CATALOG"
