#!/bin/sh
set -eu

app=bin/CD-Switcher-dev.app
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go run ./cmd/genicon "$work/icon.png"
mkdir "$work/icon.iconset"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$work/icon.png" --out "$work/icon.iconset/icon_${size}x${size}.png" >/dev/null
    double=$((size * 2))
    sips -z "$double" "$double" "$work/icon.png" --out "$work/icon.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$work/icon.iconset" -o "$work/icon.icns"

mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp bin/cd-switcher "$app/Contents/MacOS/cd-switcher"
cp "$work/icon.icns" "$app/Contents/Resources/icon.icns"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>cd-switcher</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>CFBundleIdentifier</key><string>com.lavluda.cd-switcher.dev</string>
  <key>CFBundleName</key><string>CD-Switcher dev</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>dev</string>
  <key>CFBundleURLTypes</key><array><dict>
    <key>CFBundleURLName</key><string>com.lavluda.cd-switcher.dev</string>
    <key>CFBundleURLSchemes</key><array><string>cdswitcher</string></array>
  </dict></array>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
plutil -lint "$app/Contents/Info.plist"
codesign --force --sign - "$app"
