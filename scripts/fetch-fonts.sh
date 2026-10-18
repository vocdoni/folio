#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fetches Folio's bundled fonts from their upstream releases, verifies every
# archive and every extracted file by SHA-256, and installs them (plus their
# licence files) into assets/fonts. Run from the repository root:
#   scripts/fetch-fonts.sh            # downloads into $FOLIO_FONT_CACHE
#   FOLIO_FONT_CACHE=/path scripts/fetch-fonts.sh
# All fonts are licensed under the SIL Open Font License 1.1.
set -euo pipefail

cache="${FOLIO_FONT_CACHE:-$HOME/.cache/folio/font-dl}"
dest="assets/fonts"
mkdir -p "$cache" "$dest"

# url | archive sha256
archives=(
  "https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip|9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e"
  "https://github.com/adobe-fonts/source-serif/releases/download/4.005R/source-serif-4.005_Desktop.zip|549fdb8f9a682bd06944298621404969f6de77c2e422ff3b8244a1dcd6a0c425"
  "https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip|6f6376c6ed2960ea8a963cd7387ec9d76e3f629125bc33d1fdcd7eb7012f7bbf"
  "https://github.com/notofonts/latin-greek-cyrillic/releases/download/NotoSans-v2.015/NotoSans-v2.015.zip|0c34df072a3fa7efbb7cbf34950e1f971a4447cffe365d3a359e2d4089b958f5"
  "https://github.com/notofonts/math/releases/download/NotoSansMath-v3.000/NotoSansMath-v3.000.zip|ac351837b41f8a897f020b97fb0f075ad574c1e9669fb5839ada1f92fd748356"
)

# archive | member path | installed name | sha256
files=(
  "Inter-4.1.zip|extras/ttf/Inter-Regular.ttf|Inter-Regular.ttf|40d692fce188e4471e2b3cba937be967878f631ad3ebbbdcd587687c7ebe0c82"
  "Inter-4.1.zip|extras/ttf/Inter-Bold.ttf|Inter-Bold.ttf|288316099b1e0a47a4716d159098005eef7c0066921f34e3200393dbdb01947f"
  "Inter-4.1.zip|extras/ttf/Inter-Italic.ttf|Inter-Italic.ttf|bbc051dd204b5019a1aa0bc0ae2aa8a05ab13e7a3f979fa357631dc7feb6833a"
  "Inter-4.1.zip|extras/ttf/Inter-BoldItalic.ttf|Inter-BoldItalic.ttf|948405a16cdc62701da5f4005ed068ca5f4d27061d98f7974ccfc37831d9581d"
  "Inter-4.1.zip|LICENSE.txt|LICENSE-Inter.txt|"
  "source-serif-4.005_Desktop.zip|source-serif-4.005_Desktop/TTF/SourceSerif4-Regular.ttf|SourceSerif4-Regular.ttf|e5a4ee6a3d87bb9024796be390c6771e2a0eb1883dae25effaf57ca01668e24b"
  "source-serif-4.005_Desktop.zip|source-serif-4.005_Desktop/TTF/SourceSerif4-Bold.ttf|SourceSerif4-Bold.ttf|7cf4f4e1ad74f45058d5bc61716b82560442fbdcd9d3654d2dea96bf6c683d86"
  "source-serif-4.005_Desktop.zip|source-serif-4.005_Desktop/TTF/SourceSerif4-It.ttf|SourceSerif4-It.ttf|9d2950a8f1da66e21502c35d646a1d2148e79f9ea43fd2158cf02f5232e7f430"
  "source-serif-4.005_Desktop.zip|source-serif-4.005_Desktop/TTF/SourceSerif4-BoldIt.ttf|SourceSerif4-BoldIt.ttf|7b215b37f8873f5579f3f8d2ded3ca7c588e2f435cd996605ebfc5befe2cd5eb"
  "source-serif-4.005_Desktop.zip|source-serif-4.005_Desktop/LICENSE.md|LICENSE-SourceSerif4.md|"
  "JetBrainsMono-2.304.zip|fonts/ttf/JetBrainsMono-Regular.ttf|JetBrainsMono-Regular.ttf|a0bf60ef0f83c5ed4d7a75d45838548b1f6873372dfac88f71804491898d138f"
  "JetBrainsMono-2.304.zip|fonts/ttf/JetBrainsMono-Bold.ttf|JetBrainsMono-Bold.ttf|5590990c82e097397517f275f430af4546e1c45cff408bde4255dad142479dcb"
  "JetBrainsMono-2.304.zip|fonts/ttf/JetBrainsMono-Italic.ttf|JetBrainsMono-Italic.ttf|9d0a1f7a708e6af183f1193b7e81d40da294f5c67682c085d8401c60aac8ded4"
  "JetBrainsMono-2.304.zip|fonts/ttf/JetBrainsMono-BoldItalic.ttf|JetBrainsMono-BoldItalic.ttf|4039d5ce0ed225bf9c8b2c8c6436290ae2f356b7e90d70fa666227238324aa3b"
  "JetBrainsMono-2.304.zip|OFL.txt|LICENSE-JetBrainsMono.txt|"
  "NotoSans-v2.015.zip|NotoSans/unhinted/ttf/NotoSans-Regular.ttf|NotoSans-Regular.ttf|f3961a9cde016d41a4879aecda1474d3a36d6bf54fa0e4643de029cc2248b0e8"
  "NotoSans-v2.015.zip|NotoSans/unhinted/ttf/NotoSans-Bold.ttf|NotoSans-Bold.ttf|87cb2d84472a7d66da659ee47b6cdb9552326e8c128245231f191b6ac72529d9"
  "NotoSans-v2.015.zip|NotoSans/unhinted/ttf/NotoSans-Italic.ttf|NotoSans-Italic.ttf|678288f868807d4d64a6f3b51466871d117d915780381ce9d0ed4b3bcbd06d37"
  "NotoSans-v2.015.zip|NotoSans/unhinted/ttf/NotoSans-BoldItalic.ttf|NotoSans-BoldItalic.ttf|3d367743f371f28671d2764e911a53d7c20ec9b6aa8791d059e7090389fc52a5"
  "NotoSans-v2.015.zip|OFL.txt|LICENSE-NotoSans.txt|"
  "NotoSansMath-v3.000.zip|NotoSansMath/unhinted/ttf/NotoSansMath-Regular.ttf|NotoSansMath-Regular.ttf|b127e84699212b6b2ef50aff58e0ebebeec04ffe6db1b9eb9e209c8c3d97b4aa"
  "NotoSansMath-v3.000.zip|OFL.txt|LICENSE-NotoSansMath.txt|"
)

verify() { # file sha256
  echo "$2  $1" | sha256sum --check --quiet - || { echo "checksum mismatch: $1" >&2; exit 1; }
}

for entry in "${archives[@]}"; do
  url="${entry%%|*}"; sum="${entry##*|}"; zip="$cache/$(basename "$url")"
  [ -f "$zip" ] || curl -fsSL --retry 3 -o "$zip" "$url"
  verify "$zip" "$sum"
done

for entry in "${files[@]}"; do
  IFS='|' read -r archive member name sum <<<"$entry"
  unzip -p "$cache/$archive" "$member" >"$dest/$name"
  [ -z "$sum" ] || verify "$dest/$name" "$sum"
  chmod 0644 "$dest/$name"
done
echo "installed $(ls "$dest" | wc -l) files into $dest"
