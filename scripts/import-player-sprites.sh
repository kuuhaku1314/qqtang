#!/bin/zsh
# Download the 4.3 character walk sprites (n02-n18) from qqt.95hyc.cn.
# c1 full walk set (4 directions x 6 frames) + c2 down1 for blue-team idle.
set -u
DEST="$(cd "$(dirname "$0")/.." && pwd)/QQTangMac/Resources/Legacy43/Game/Player"
mkdir -p "$DEST"

jobs=()
for n in 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18; do
  for dir in down up left right; do
    for f in 1 2 3 4 5 6; do
      jobs+=("n${n}_c1_${dir}${f}.png n${n}-c1-${dir}-${f}.png")
    done
  done
  jobs+=("n${n}_c2_down1.png n${n}-c2-down-1.png")
done

printf '%s\n' "${jobs[@]}" | xargs -P 8 -L 1 sh -c '
  src="$0"; out="$1"
  dest="'"$DEST"'/$out"
  [ -s "$dest" ] && exit 0
  code=$(curl -s -o "$dest" -w "%{http_code}" --max-time 20 "https://qqt.95hyc.cn/ui/player/$src")
  if [ "$code" != "200" ]; then rm -f "$dest"; echo "MISS $src"; fi
'
echo done; ls "$DEST" | wc -l
