#!/bin/bash
# Test harness: builds a simulated Linux Steam + Project Zomboid B42 install
# under a fake HOME, then drives the installer through install/uninstall flows.
set -u

PROJ="$(cd "$(dirname "$0")/../installer" && pwd)"
BIN=$PROJ/zombiebuddy-linux-installer
FH=/tmp/fakehome
LIB2=/tmp/fakehome/ExtraLib
STEAM=$FH/.local/share/Steam
PZ=$LIB2/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853

fail=0
ok()   { echo "  PASS: $1"; }
bad()  { echo "  FAIL: $1"; fail=1; }

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
                "path"          "/tmp/fakehome/.local/share/Steam"
                "label"         ""
                "contentid"             "11"
                "totalsize"             "1000000000"
                "update_clean_bytes_tally"              "0"
                "time_last_update_corruption"           "0"
                "apps"
                {
                        "228983"                "388096874"
                }
        }
        "1"
        {
                "path"          "/tmp/fakehome/ExtraLib"
                "label"         "Games"
                "contentid"             "22"
                "totalsize"             "2000000000"
                "update_clean_bytes_tally"              "0"
                "time_last_update_corruption"           "0"
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
        "UserGuestConfigStore"
        {
                "language"              "english"
        }
        "Software"
        {
                "Valve"
                {
                        "Steam"
                        {
                                "apps"
                                {
                                        "730"
                                        {
                                                "LastPlayed"            "1729500000"
                                        }
                                        "108600"
                                        {
                                                "LastPlayed"            "1729600000"
                                                "LaunchOptions"         "-Xmx8192m"
                                        }
                                }
                                "VerifyIntegrityFilesInsteadOfUninstalling"             "0"
                        }
                }
        }
}
EOF

  cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
        "mainClass": "zombie/gameStates/MainScreenState",
        "classpath": [
                ".",
                "projectzomboid.jar"
        ],
        "vmArgs": [
                "-Djava.awt.headless=true",
                "-Xmx3072m",
                "-Dzomboid.steam=1",
                "-Dzomboid.znetlog=1",
                "-Djava.library.path=linux64:.",
                "-XX:-CreateCoredumpOnCrash",
                "-XX:-OmitStackTraceInFastThrow"
        ]
}
EOF

  cat > "$PZ/projectzomboid.sh" <<'EOF'
#!/bin/bash
cd "$(dirname "$0")"
exec ./projectzomboid/ProjectZomboid64 "$@"
EOF
  chmod +x "$PZ/projectzomboid.sh"

  printf 'FAKE-JAR-CONTENTS-v4.2' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
  printf 'MZ-FAKE-DLL' > "$ZB/mods/ZombieBuddy/libs/zbNative.dll"

  printf 'fake-launcher-binary' > "$PZ/projectzomboid/ProjectZomboid64"
  printf 'fake-game-jar' > "$PZ/projectzomboid/projectzomboid.jar"
}

run() { HOME=$FH "$BIN" "$@"; }

echo "=== T1: help output + nothing flow ==="
fresh_layout
run --help > /tmp/t1h.out 2>&1
grep -q "ZombieBuddy Linux Installer (v4.2) - Ported by Kiwi" /tmp/t1h.out && ok "--help banner" || bad "--help banner"
grep -q -- "-steam-dir" /tmp/t1h.out && ok "--help documents --steam-dir" || bad "--help flags"
printf '3\n' | run > /tmp/t1.out 2>&1
grep -q "Okay, nothing changed." /tmp/t1.out && ok "nothing flow" || bad "nothing flow"

echo "=== T2: full install (Both) ==="
fresh_layout
printf '1\n1\ny\n\n' | run > /tmp/t2.out 2>&1
cat /tmp/t2.out
echo "--- assertions ---"
grep -qE "Steam +is at /tmp/fakehome/.local/share/Steam \(native\)" /tmp/t2.out && ok "steam path detect" || bad "steam path detect"
grep -qE "PZ +is at /tmp/fakehome/ExtraLib/steamapps/common/ProjectZomboid" /tmp/t2.out && ok "PZ in secondary library via libraryfolders.vdf" || bad "PZ in secondary library via libraryfolders.vdf"
grep -qE "ZB +is at /tmp/fakehome/.local/share/Steam/steamapps/workshop/content/108600/3619862853" /tmp/t2.out && ok "ZB workshop path detect" || bad "ZB workshop path detect"
grep -qE "JSON +is at /tmp/fakehome/ExtraLib/steamapps/common/ProjectZomboid/projectzomboid/ProjectZomboid64.json" /tmp/t2.out && ok "B42 json location detect" || bad "B42 json location detect"
grep -q "Successfully installed ZombieBuddy.jar" /tmp/t2.out && ok "install msg" || bad "install msg"
cmp -s "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar" "$PZ/projectzomboid/ZombieBuddy.jar" && ok "jar copied to javaDir" || bad "jar copied"
grep -q "zbNative.dll found in Workshop content - it is Windows-only" /tmp/t2.out && ok "dll skip note" || bad "dll skip note"
python3 - <<'EOF' && ok "json vmArgs patched (first, ordered, others intact)" || bad "json vmArgs"
import json
cfg = json.load(open('/tmp/fakehome/ExtraLib/steamapps/common/ProjectZomboid/projectzomboid/ProjectZomboid64.json'))
a = cfg['vmArgs']
assert a[0] == '-javaagent:ZombieBuddy.jar', a
assert a[1:] == ["-Djava.awt.headless=true","-Xmx3072m","-Dzomboid.steam=1","-Dzomboid.znetlog=1","-Djava.library.path=linux64:.","-XX:-CreateCoredumpOnCrash","-XX:-OmitStackTraceInFastThrow"], a
assert cfg['mainClass'] == 'zombie/gameStates/MainScreenState'
assert cfg['classpath'] == ['.', 'projectzomboid.jar']
EOF
head -3 "$PZ/projectzomboid/ProjectZomboid64.json" | grep -q '"mainClass"' && ok "json key order preserved" || bad "json key order"
grep -q "^export _JAVA_OPTIONS='-javaagent:ZombieBuddy.jar'$" "$PZ/projectzomboid.sh" && ok "sh patched after shebang" || bad "sh patched: $(head -4 "$PZ/projectzomboid.sh" | tail -2)"
grep -q "current PZ Steam launch options: \"-Xmx8192m\"" /tmp/t2.out && ok "launch options transparency" || bad "transparency"
grep -qP '"LaunchOptions"\s+"-javaagent:ZombieBuddy.jar --"' "$STEAM/userdata/12345678/config/localconfig.vdf" && ok "localconfig LaunchOptions set" || bad "localconfig: $(grep LaunchOptions "$STEAM/userdata/12345678/config/localconfig.vdf")"
grep -q "Updated launch options for user 12345678" /tmp/t2.out && ok "user update msg" || bad "user update msg"
grep -q "Installation complete!" /tmp/t2.out && ok "complete msg" || bad "complete msg"

echo "=== T3: re-install idempotency ==="
printf '1\n2\n2\ny\n\n' | run > /tmp/t3.out 2>&1   # normal launch, json only
grep -q "ProjectZomboid64.json already contains ZombieBuddy agent." /tmp/t3.out && ok "json idempotent" || bad "json idempotent"
python3 -c "import json;a=json.load(open('$PZ/projectzomboid/ProjectZomboid64.json'))['vmArgs'];assert a.count('-javaagent:ZombieBuddy.jar')==1,a" && ok "no duplicate vmArg" || bad "duplicate vmArg"

echo "=== T4: uninstall ==="
printf '2\ny\n\n' | run > /tmp/t4.out 2>&1
cat /tmp/t4.out
echo "--- assertions ---"
[ ! -e "$PZ/projectzomboid/ZombieBuddy.jar" ] && ok "jar removed" || bad "jar removed"
grep -q 'Removed "-javaagent:ZombieBuddy.jar" from ProjectZomboid64.json' /tmp/t4.out && ok "json arg removed msg" || bad "json removal msg"
python3 -c "
import json
cfg=json.load(open('$PZ/projectzomboid/ProjectZomboid64.json'))
a=cfg['vmArgs']
assert '-javaagent:ZombieBuddy.jar' not in a, a
assert a[0]=='-Djava.awt.headless=true', a
assert cfg['mainClass']=='zombie/gameStates/MainScreenState'
" && ok "json restored vanilla" || bad "json restored"
grep -q "^exec ./projectzomboid/ProjectZomboid64" "$PZ/projectzomboid.sh" && ok "sh restored" || bad "sh restored: $(cat "$PZ/projectzomboid.sh")"
grep -q "^unset _JAVA_OPTIONS$" "$PZ/projectzomboid.sh" && ok "sh java_options unset cleanly" || bad "sh java_options state: $(grep -n JAVA "$PZ/projectzomboid.sh")"
grep -qP '"LaunchOptions"\s+""' "$STEAM/userdata/12345678/config/localconfig.vdf" && ok "localconfig LaunchOptions emptied (matches Windows original)" || bad "localconfig restored: $(grep LaunchOptions "$STEAM/userdata/12345678/config/localconfig.vdf")"
grep -q "Uninstall complete!" /tmp/t4.out && ok "uninstall complete msg" || bad "uninstall complete msg"

echo "=== T5: uninstall when nothing installed ==="
fresh_layout
printf '2\n\n' | run > /tmp/t5.out 2>&1
grep -q "Nothing to uninstall." /tmp/t5.out && ok "nothing to uninstall" || bad "nothing to uninstall"

echo "=== T6: cancel install at confirmation ==="
fresh_layout
printf '1\n1\nn\n\n' | run > /tmp/t6.out 2>&1
grep -q "Installation cancelled." /tmp/t6.out && ok "cancel works" || bad "cancel works"
[ ! -e "$PZ/projectzomboid/ZombieBuddy.jar" ] && ok "no side effects after cancel" || bad "side effects after cancel"

echo
if [ $fail -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "SOME TESTS FAILED"; fi
exit $fail
