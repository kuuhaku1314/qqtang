#!/bin/zsh
# Convert original QQTang 4.3 BGM (ogg) to m4a for AVAudioPlayer.
# Mapping from res/uiRes/uiConst.pyc:
#   musicStart=m09  musicDirAndSection=das  musicRoom=m10
set -eu
SRC=/tmp/qqt43/extracted/QQTang4.3_Beta1Build2/music
DEST="$(cd "$(dirname "$0")/.." && pwd)/QQTangMac/Resources/Legacy43/Audio/BGM"
mkdir -p "$DEST"

names=(m09 das m10 M07 PlayerWin PlayerLoss T1
       town snow desert mine water field bomb bun match machine sculpture tank)

for n in $names; do
  src="$SRC/$n.ogg"
  # 官方文件名大小写不一（m09 在包里是 M09.ogg）
  [ -f "$src" ] || src="$SRC/${n:u}.ogg"
  [ -f "$src" ] || src="$SRC/${n:l}.ogg"
  if [ ! -f "$src" ]; then echo "MISS $n"; continue; fi
  out="$DEST/$n.m4a"
  [ -s "$out" ] && continue
  ffmpeg -loglevel error -y -i "$src" -c:a aac -b:a 128k "$out"
  echo "OK   $n"
done
ls "$DEST" | wc -l
