#!/bin/bash
# Edge-case tests: Flatpak detection, B41 layout, overrides, weird files.
set -u

PROJ="$(cd "$(dirname "$0")/../installer" && pwd)"
BIN=$PROJ/zombiebuddy-linux-installer
FH=/tmp/fakehome2
fail=0
ok()  { echo "  PASS: $1"; }
bad() { echo "  FAIL: $1"; fail=1; }

# ---------- T7: Flatpak Steam layout ----------
echo "=== T7: Flatpak Steam detection ==="
rm -rf "$FH"
STEAM=$FH/.var/app/com.valvesoftware.Steam/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
mkdir -p "$PZ/projectzomboid" "$ZB/mods/ZombieBuddy/libs" "$STEAM/userdata/777/config"
printf '"libraryfolders"\n{\n\t"0"\n\t{\n\t\t"path"\t\t"%s"\n\t}\n}\n' "$STEAM" > "$STEAM/steamapps/libraryfolders.vdf"
cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "zombie/gameStates/MainScreenState",
	"classpath": ["."],
	"vmArgs": ["-Xmx2048m"]
}
EOF
cat > "$PZ/projectzomboid.sh" <<'EOF'
#!/bin/sh
exec ./projectzomboid/ProjectZomboid64 "$@"
EOF
printf 'JAR-FLATPAK' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
printf '3\n' > /dev/null
printf '1\n2\n2\ny\n\n' | HOME=$FH "$BIN" > /tmp/t7.out 2>&1
grep -qE "Steam +is at .*\.var/app/com\.valvesoftware\.Steam/.local/share/Steam \(flatpak\)" /tmp/t7.out && ok "flatpak detected" || bad "flatpak detected: $(grep 'Steam' /tmp/t7.out)"
grep -q "Installation complete!" /tmp/t7.out && ok "flatpak install ok" || bad "flatpak install: $(tail -5 /tmp/t7.out)"
[ -f "$PZ/projectzomboid/ZombieBuddy.jar" ] && ok "flatpak jar copied" || bad "flatpak jar copied"
python3 -c "import json;a=json.load(open('$PZ/projectzomboid/ProjectZomboid64.json'))['vmArgs'];assert a==['-javaagent:ZombieBuddy.jar','-Xmx2048m'],a" && ok "flatpak json patched" || bad "flatpak json patched"

# ---------- T8: B41-style layout (json + sh in game root) ----------
echo "=== T8: B41-style layout (files at game root) ==="
rm -rf "$FH"
STEAM=$FH/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
mkdir -p "$PZ" "$ZB/mods/ZombieBuddy/libs"
cat > "$PZ/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "zombie/gameStates/MainScreenState",
	"classpath": ["."],
	"vmArgs": ["-Xmx2048m"]
}
EOF
cat > "$PZ/ProjectZomboid64.sh" <<'EOF'
#!/bin/sh
cd "$(dirname "$0")"
java -Xmx2048m -cp ./ zombie.gameStates.MainScreenState
EOF
printf 'JAR-B41' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
printf '1\n1\ny\n\n' | HOME=$FH "$BIN" > /tmp/t8.out 2>&1
grep -qE "JSON +is at .*/ProjectZomboid/ProjectZomboid64.json" /tmp/t8.out && ok "B41 json at root detected" || bad "B41 json detect"
grep -qE "SH +is at .*/ProjectZomboid/ProjectZomboid64.sh" /tmp/t8.out && ok "B41 sh detected" || bad "B41 sh detect"
[ -f "$PZ/ZombieBuddy.jar" ] && ok "B41 jar at game root" || bad "B41 jar at game root"
python3 -c "import json;a=json.load(open('$PZ/ProjectZomboid64.json'))['vmArgs'];assert a==['-javaagent:ZombieBuddy.jar','-Xmx2048m'],a" && ok "B41 json patched" || bad "B41 json patched"
grep -q "^export _JAVA_OPTIONS='-javaagent:ZombieBuddy.jar'$" "$PZ/ProjectZomboid64.sh" && ok "B41 sh patched" || bad "B41 sh patched"
printf '2\ny\n\n' | HOME=$FH "$BIN" > /tmp/t8u.out 2>&1
[ ! -f "$PZ/ZombieBuddy.jar" ] && ok "B41 uninstall removes jar" || bad "B41 uninstall jar"
grep -q "^java -Xmx2048m" "$PZ/ProjectZomboid64.sh" && ok "B41 sh unpatched (java line intact)" || bad "B41 sh unpatched: $(cat "$PZ/ProjectZomboid64.sh")"

# ---------- T9: sh with pre-existing _JAVA_OPTIONS (quoted) ----------
echo "=== T9: pre-existing _JAVA_OPTIONS in script ==="
rm -rf "$FH"
STEAM=$FH/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
mkdir -p "$PZ/projectzomboid" "$ZB/mods/ZombieBuddy/libs"
cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "x",
	"vmArgs": ["-Xmx1g"]
}
EOF
cat > "$PZ/projectzomboid.sh" <<'EOF'
#!/bin/bash
export _JAVA_OPTIONS='-Xmx2g -Dfoo=bar'
exec ./projectzomboid/ProjectZomboid64 "$@"
EOF
printf 'JAR' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
printf '1\n3\ny\n\n' | HOME=$FH "$BIN" > /tmp/t9.out 2>&1
grep -q "^export _JAVA_OPTIONS='-javaagent:ZombieBuddy.jar -Xmx2g -Dfoo=bar'$" "$PZ/projectzomboid.sh" && ok "existing options preserved, agent prepended" || bad "sh merge: $(grep JAVA "$PZ/projectzomboid.sh")"
printf '2\ny\n\n' | HOME=$FH "$BIN" > /tmp/t9u.out 2>&1
grep -q "^export _JAVA_OPTIONS='-Xmx2g -Dfoo=bar'$" "$PZ/projectzomboid.sh" && ok "uninstall restores original options" || bad "sh restore: $(grep JAVA "$PZ/projectzomboid.sh")"

# ---------- T10: --steam-dir and --jar overrides ----------
echo "=== T10: --steam-dir / --jar overrides ==="
rm -rf "$FH" /tmp/altsteam
STEAM=/tmp/altsteam
PZ=$STEAM/steamapps/common/ProjectZomboid
mkdir -p "$PZ/projectzomboid"
printf '"libraryfolders"\n{\n\t"0"\n\t{\n\t\t"path"\t\t"/tmp/altsteam"\n\t}\n}\n' > "$STEAM/steamapps/libraryfolders.vdf"
cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "x",
	"vmArgs": ["-Xmx1g"]
}
EOF
mkdir -p /tmp/jarsrc && printf 'JAR-OVERRIDE' > /tmp/jarsrc/ZombieBuddy.jar
printf '1\n2\n2\ny\n\n' | HOME=$FH "$BIN" --steam-dir /tmp/altsteam --jar /tmp/jarsrc/ZombieBuddy.jar > /tmp/t10.out 2>&1
grep -qE "Steam +is at /tmp/altsteam \(custom\)" /tmp/t10.out && ok "--steam-dir override works" || bad "steam-dir override"
[ -f "$PZ/projectzomboid/ZombieBuddy.jar" ] && cmp -s /tmp/jarsrc/ZombieBuddy.jar "$PZ/projectzomboid/ZombieBuddy.jar" && ok "--jar override copied" || bad "--jar override"

# ---------- T11: json without vmArgs key ----------
echo "=== T11: json without vmArgs ==="
rm -rf "$FH"
STEAM=$FH/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
mkdir -p "$PZ/projectzomboid" "$ZB/mods/ZombieBuddy/libs"
cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "zombie/gameStates/MainScreenState",
	"classpath": [
		".",
		"projectzomboid.jar"
	],
	"unknownFutureKey": { "keep": ["me"] }
}
EOF
cat > "$PZ/projectzomboid.sh" <<'EOF'
#!/bin/sh
exec ./projectzomboid/ProjectZomboid64 "$@"
EOF
printf 'JAR' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
printf '1\n2\n2\ny\n\n' | HOME=$FH "$BIN" > /tmp/t11.out 2>&1
python3 - <<'EOF' && ok "vmArgs appended + unknown key preserved" || bad "vmArgs append"
import json
cfg = json.load(open('/tmp/fakehome2/.local/share/Steam/steamapps/common/ProjectZomboid/projectzomboid/ProjectZomboid64.json'))
assert cfg['vmArgs'] == ['-javaagent:ZombieBuddy.jar'], cfg.get('vmArgs')
assert cfg['unknownFutureKey'] == {"keep": ["me"]}, cfg.get('unknownFutureKey')
assert cfg['mainClass'] == 'zombie/gameStates/MainScreenState'
assert cfg['classpath'] == ['.', 'projectzomboid.jar']
EOF
printf '2\ny\n\n' | HOME=$FH "$BIN" > /tmp/t11u.out 2>&1
python3 -c "
import json
cfg = json.load(open('/tmp/fakehome2/.local/share/Steam/steamapps/common/ProjectZomboid/projectzomboid/ProjectZomboid64.json'))
assert 'vmArgs' not in cfg or cfg['vmArgs'] == [], cfg.get('vmArgs')
assert cfg.get('unknownFutureKey') == {'keep': ['me']}
" && ok "uninstall: vmArgs emptied, unknown key still intact" || bad "uninstall json"

# ---------- T12: localconfig without 108600 section ----------
echo "=== T12: localconfig without PZ section (warning, no corruption) ==="
rm -rf "$FH"
STEAM=$FH/.local/share/Steam
PZ=$STEAM/steamapps/common/ProjectZomboid
ZB=$STEAM/steamapps/workshop/content/108600/3619862853
mkdir -p "$PZ/projectzomboid" "$ZB/mods/ZombieBuddy/libs" "$STEAM/userdata/42/config"
cp /tmp/fakehome/.local/share/Steam/steamapps/libraryfolders.vdf $STEAM/steamapps/ 2>/dev/null || printf '"libraryfolders"\n{\n\t"0"\n\t{\n\t\t"path"\t\t"%s"\n\t}\n}\n' "$STEAM" > "$STEAM/steamapps/libraryfolders.vdf"
cat > "$STEAM/userdata/42/config/localconfig.vdf" <<'EOF'
"UserLocalConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"Apps"
				{
					"730"
					{
						"LastPlayed"		"1729500000"
					}
				}
			}
		}
	}
}
EOF
cat > "$PZ/projectzomboid/ProjectZomboid64.json" <<'EOF'
{
	"mainClass": "x",
	"vmArgs": ["-Xmx1g"]
}
EOF
printf 'JAR' > "$ZB/mods/ZombieBuddy/libs/ZombieBuddy.jar"
printf '1\n2\n3\ny\n\n' | HOME=$FH "$BIN" > /tmp/t12.out 2>&1
grep -q "Could not find the Project Zomboid (108600) section" /tmp/t12.out && ok "warning shown for missing PZ section" || bad "warning missing"
cmp -s - <<'EOF' && ok "localconfig byte-identical (no corruption)" || bad "localconfig corrupted"
"UserLocalConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"Apps"
				{
					"730"
					{
						"LastPlayed"		"1729500000"
					}
				}
			}
		}
	}
}
EOF
cat "$STEAM/userdata/42/config/localconfig.vdf"

echo
if [ $fail -eq 0 ]; then echo "ALL EDGE TESTS PASSED"; else echo "SOME EDGE TESTS FAILED"; fi
exit $fail
