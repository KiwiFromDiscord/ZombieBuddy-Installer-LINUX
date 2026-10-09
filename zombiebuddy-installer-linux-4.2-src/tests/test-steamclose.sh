#!/bin/bash
# Tests for the automatic Steam shutdown feature (steamcontrol.go).
#
# Uses a helper binary named "steam" (fake-steam.go) so the installer's
# /proc-based detection sees a live Steam process. The sandbox has no
# steam/flatpak/snap CLI and the fake Steam root has no steam.sh, so the
# shutdown falls through to the SIGTERM method - exactly the path a Steam
# Deck hits when the steam launcher is not on PATH.
set -u

PROJ="$(cd "$(dirname "$0")/../installer" && pwd)"
BIN=$PROJ/zombiebuddy-linux-installer
FAKE=/tmp/zb-fake-steam/steam
if [ ! -x "$FAKE" ]; then
  mkdir -p "$(dirname "$FAKE")"
  (cd "$(dirname "$0")" && go build -o "$FAKE" fake-steam.go) || { echo "cannot build fake-steam helper (needs Go)"; exit 2; }
fi
FH=/tmp/zb-steamclose-home
STEAM=$FH/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
OUT=/tmp/zb-steamclose.out

fail=0
ok()  { echo "  PASS: $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

count() { grep -c "$1" "$OUT" 2>/dev/null || true; }

fresh_layout() {
  rm -rf "$FH"
  mkdir -p "$STEAM/steamapps" "$STEAM/userdata/12345678/config" \
           "$PZ/projectzomboid" \
           "$ZB/mods/ZombieBuddy/libs"

  cat > "$STEAM/steamapps/libraryfolders.vdf" <<'EOF'
"libraryfolders"
{
        "0"
        {
                "path"          "/tmp/zb-steamclose-home/.local/share/Steam"
                "apps"
                {
                        "108600"                "9765625000"
                }
        }
}
EOF

  cat > "$STEAM/userdata/12345678/config/localconfig.vdf" <<'EOF'
"UserLocalConfigStore"
{
        "Software"
        {
                "Valve"
                {
                        "Steam"
                        {
                                "apps"
                                {
                                        "108600"
                                        {
                                                "LastPlayed"            "1729600000"
                                                "LaunchOptions"         "-Xmx8192m"
                                        }
                                }
                        }
                }
        }
}
EOF

  cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
        "mainClass": "zombie/gameStates/MainScreenState",
        "classpath": [ ".", "projectzomboid.jar" ],
        "vmArgs": [ "-Djava.awt.headless=true", "-Xmx3072m" ]
}
EOF

  cat > "$PZ/projectzomboid.sh" <<'EOF'
#!/bin/bash
cd "$(dirname "$0")"
exec ./projectzomboid/ProjectZomboid64 "$@"
EOF
  chmod +x "$PZ/projectzomboid.sh"

  printf 'FAKE-JAR-CONTENTS-v4.2' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
}

start_fake_steam() {
  "$FAKE" "$1" &
  FAKE_PID=$!
  sleep 0.3
  # confirm the helper is visible the way the installer sees it
  grep -q "steam" "/proc/$FAKE_PID/comm" || { echo "fake steam helper broken"; exit 2; }
}

fake_steam_alive() { kill -0 "$FAKE_PID" 2>/dev/null; }

echo "=== S0: banner ==="
fresh_layout
printf '3\n' | HOME="$FH" timeout 30 "$BIN" > "$OUT" 2>&1
grep -q "ZombieBuddy Linux Installer (v4.2) - Ported by Kiwi" "$OUT" \
  && ok "banner shows Ported by Kiwi" || bad "banner shows Ported by Kiwi"
grep -q "Could not close Steam" "$OUT" && bad "no steam-close noise without steam" || ok "no steam-close noise without steam"

echo "=== S1: install closes running Steam automatically ==="
fresh_layout
start_fake_steam ""
printf '1\n1\ny\n' | HOME="$FH" timeout 90 "$BIN" > "$OUT" 2>&1
RC=$?
[ $RC -eq 0 ] && ok "install exit code 0" || bad "install exit code $RC"
grep -q "Steam is currently running" "$OUT" && ok "detects running Steam" || bad "detects running Steam"
grep -q "Closing Steam automatically" "$OUT" && ok "announces automatic close" || bad "announces automatic close"
grep -q "Sending terminate signal" "$OUT" && ok "SIGTERM fallback used" || bad "SIGTERM fallback used"
grep -q "Steam is closed" "$OUT" && ok "reports Steam closed" || bad "reports Steam closed"
grep -q "Installation complete" "$OUT" && ok "install completes after close" || bad "install completes after close"
[ "$(count 'Waiting for Steam to close')" -eq 1 ] && ok "single wait cycle" || bad "wait cycle count $(count 'Waiting for Steam to close')"
fake_steam_alive && bad "fake steam terminated" || ok "fake steam terminated"
grep -q -- "-javaagent:ZombieBuddy.jar" "$PZ/projectzomboid/ProjectZomboid64.json" \
  && ok "json patched after steam closed" || bad "json patched after steam closed"
grep -q "LaunchOptions" "$STEAM/userdata/12345678/config/localconfig.vdf" \
  && grep -q -- "-javaagent:ZombieBuddy.jar --" "$STEAM/userdata/12345678/config/localconfig.vdf" \
  && ok "launch options patched after steam closed" || bad "launch options patched after steam closed"
kill "$FAKE_PID" 2>/dev/null

echo "=== S2: uninstall closes running Steam automatically ==="
start_fake_steam ""
printf '2\ny\n' | HOME="$FH" timeout 90 "$BIN" > "$OUT" 2>&1
RC=$?
[ $RC -eq 0 ] && ok "uninstall exit code 0" || bad "uninstall exit code $RC"
grep -q "Steam is currently running" "$OUT" && ok "uninstall detects running Steam" || bad "uninstall detects running Steam"
grep -q "Steam is closed" "$OUT" && ok "uninstall closes Steam" || bad "uninstall closes Steam"
grep -q "Uninstall complete" "$OUT" && ok "uninstall completes after close" || bad "uninstall completes after close"
fake_steam_alive && bad "fake steam terminated (uninstall)" || ok "fake steam terminated (uninstall)"
[ ! -f "$PZ/projectzomboid/ZombieBuddy.jar" ] && ok "jar removed" || bad "jar removed"
kill "$FAKE_PID" 2>/dev/null

echo "=== S3: no Steam running - unchanged behaviour ==="
fresh_layout
printf '1\n1\ny\n' | HOME="$FH" timeout 30 "$BIN" > "$OUT" 2>&1
RC=$?
[ $RC -eq 0 ] && ok "install exit code 0" || bad "install exit code $RC"
grep -q "Steam is currently running" "$OUT" && bad "no close attempt without steam" || ok "no close attempt without steam"
grep -q "Installation complete" "$OUT" && ok "install still works" || bad "install still works"

echo "=== S4: unkillable Steam + drained stdin - proceeds with warning, no hang ==="
fresh_layout
start_fake_steam "ignore"
printf '1\n1\ny\n' | HOME="$FH" timeout 90 "$BIN" > "$OUT" 2>&1
RC=$?
[ $RC -eq 0 ] && ok "no hang, exit code 0" || bad "exit code $RC (hang or failure)"
[ "$(count 'Could not close Steam automatically')" -eq 1 ] \
  && ok "manual fallback offered once" || bad "manual fallback count $(count 'Could not close Steam automatically')"
[ "$(count 'Proceeding with Steam still running')" -eq 1 ] \
  && ok "proceeds with warning once" || bad "proceed warning count $(count 'Proceeding with Steam still running')"
grep -q "did not react to the terminate signal" "$OUT" && ok "SIGTERM timeout reported" || bad "SIGTERM timeout reported"
grep -q "Installation complete" "$OUT" && ok "install completes anyway" || bad "install completes anyway"
grep -q -- "-javaagent:ZombieBuddy.jar --" "$STEAM/userdata/12345678/config/localconfig.vdf" \
  && ok "launch options patched anyway" || bad "launch options patched anyway"
kill -9 "$FAKE_PID" 2>/dev/null

echo
if [ $fail -eq 0 ]; then echo "ALL STEAM-CLOSE TESTS PASSED"; else echo "STEAM-CLOSE TESTS FAILED"; exit 1; fi
