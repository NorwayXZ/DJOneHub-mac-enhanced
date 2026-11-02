#!/bin/zsh
# 由 DJOneHub.app 的安装入口调用：install.sh <DJOneHub.app 路径>
# 把 App 内置的后端运行时和 App 本身装到用户目录，并注册开机自启。
set -eu
SRC_APP="$1"

DEST="$HOME/Library/Application Support/DJOneHub"
RUNTIME="$DEST/runtime"
NOTIFIER_DIR="$DEST/notifier"
AGENTS="$HOME/Library/LaunchAgents"
UID_NUM=$(id -u)
MULTI_MODULE_XML=""
if /usr/libexec/PlistBuddy -c 'Print :ProgramArguments' "$AGENTS/com.jamie.djonehub.plist" 2>/dev/null | /usr/bin/grep -Eq '^[[:space:]]*--?multi-module(=true)?[[:space:]]*$'; then
  MULTI_MODULE_XML='<string>--multi-module</string>'
fi

echo "DJOneHub 安装：$SRC_APP -> $DEST"

# 1. 停止旧的服务与通知助手
launchctl bootout "gui/$UID_NUM/com.jamie.djonehub" >/dev/null 2>&1 || true
launchctl bootout "gui/$UID_NUM/com.jamie.djonehub-notifier" >/dev/null 2>&1 || true

# 2. 复制主程序与运行库
rm -rf "$RUNTIME"
mkdir -p "$RUNTIME"
ditto --norsrc --noextattr --noqtn --noacl "$SRC_APP/Contents/Resources/djonehub" "$RUNTIME"
chmod 755 "$RUNTIME/djonehub" "$RUNTIME/bin/djonehub-macos" "$RUNTIME/lib/libusb-1.0.0.dylib"

# 3. 复制通知助手
mkdir -p "$NOTIFIER_DIR"
rm -rf "$NOTIFIER_DIR/DJOneHubNotifier.app"
ditto --norsrc --noextattr --noqtn --noacl "$SRC_APP" "$NOTIFIER_DIR/DJOneHubNotifier.app"

# 4. 注册开机自启
mkdir -p "$AGENTS" "$HOME/Library/Logs/DJOneHub"
cat > "$AGENTS/com.jamie.djonehub.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.jamie.djonehub</string>
  <key>ProgramArguments</key>
  <array>
    <string>$RUNTIME/bin/djonehub-macos</string>
    <string>-listen</string>
    <string>127.0.0.1:7575</string>
    $MULTI_MODULE_XML
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>5</integer>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>$HOME/Library/Logs/DJOneHub/launchd.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/Library/Logs/DJOneHub/launchd.log</string>
  <key>WorkingDirectory</key>
  <string>$RUNTIME</string>
</dict>
</plist>
EOF

cat > "$AGENTS/com.jamie.djonehub-notifier.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.jamie.djonehub-notifier</string>
  <key>ProgramArguments</key>
  <array>
    <string>$NOTIFIER_DIR/DJOneHubNotifier.app/Contents/MacOS/DJOneHubNotifier</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>$HOME/Library/Logs/DJOneHub/notifier.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/Library/Logs/DJOneHub/notifier.log</string>
</dict>
</plist>
EOF

launchctl bootstrap "gui/$UID_NUM" "$AGENTS/com.jamie.djonehub.plist"
launchctl bootstrap "gui/$UID_NUM" "$AGENTS/com.jamie.djonehub-notifier.plist"

# 5. 等待服务就绪并打开控制台
for i in {1..150}; do
  if curl -fsS --max-time 1 http://127.0.0.1:7575/ >/dev/null 2>&1; then
    open "$NOTIFIER_DIR/DJOneHubNotifier.app" --args --show-window
    echo "安装完成"
    exit 0
  fi
  sleep 0.2
done
echo "后台服务未能就绪，请查看 $HOME/Library/Logs/DJOneHub/launchd.log" >&2
exit 1
