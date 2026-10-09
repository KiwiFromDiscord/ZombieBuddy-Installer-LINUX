# ZombieBuddy-Installer-LINUX
A modified and unnoficial version of the Windows ZombieBuddy Installer that ahould work on linux.
BUILDS AS AN APPIMAGE.


# ZombieBuddy Linux Installer (v4.2) - Ported by Kiwi

A native Linux rework of `ZombieBuddyInstaller_v4.2.exe` — the setup installer
for the [ZombieBuddy](https://steamcommunity.com/sharedfiles/filedetails/?id=3619862853)
modloader for Project Zomboid. It is a self-contained AppImage; nothing is
installed system-wide and no root rights are needed.

## Requirements

- Linux x86_64 (SteamOS, Ubuntu, Mint, Fedora, Arch, ...)
- Steam for Linux **with the native Linux build of Project Zomboid installed**
  (Build 42 recommended; Build 41 layout is also supported)
- Subscribed to the **ZombieBuddy** Steam Workshop item
  (https://steamcommunity.com/sharedfiles/filedetails/?id=3619862853) — the
  installer copies `ZombieBuddy.jar` from its Workshop content, exactly like
  the Windows installer does

## Running it

```bash
chmod +x ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage
./ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage
```

Double-clicking also works — it opens a terminal window and runs there
(the desktop entry is marked `Terminal=true`). If your desktop does not pick
a terminal emulator, just run it from a shell.

**No FUSE / Ubuntu 24.04+ note:** if you get `Cannot mount AppImage, please
check your FUSE setup`, either install `libfuse2` (package `libfuse2t64` on
Ubuntu 24.04) or run without FUSE:

```bash
./ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage --appimage-extract-and-run
```

## What it does (same flow as the Windows installer)

1. Detects Steam on Linux — native (`~/.steam/steam`,
   `~/.local/share/Steam`), Flatpak
   (`~/.var/app/com.valvesoftware.Steam/...`) and Snap
   (`~/snap/steam/...`) installations, in that order
2. Finds Project Zomboid via `steamapps/libraryfolders.vdf`
   (works with games installed in secondary libraries)
3. Finds the ZombieBuddy Workshop content
   (`steamapps/workshop/content/108600/3619862853`)
4. Copies `ZombieBuddy.jar` into the game's Java directory
   (`.../ProjectZomboid/projectzomboid/` on Build 42)
5. Patches the launch modes you choose:
   - **Normal Launch** — adds `-javaagent:ZombieBuddy.jar` to `vmArgs` in
     `ProjectZomboid64.json` (the official launcher config)
   - **Alternate Launch** — adds `export _JAVA_OPTIONS='-javaagent:ZombieBuddy.jar'`
     to `projectzomboid.sh`
   - **Steam launch options** — sets `-javaagent:ZombieBuddy.jar --` in
     `localconfig.vdf`
6. Shows a full preview of every change and asks for confirmation
7. **After you press `y` to confirm, Steam is closed automatically**
   (gracefully: `steam -shutdown` / Flatpak / Snap shutdown request, then a
   terminate signal as last resort) — handy on the Steam Deck, where you may
   need Steam running to type into the installer. Nothing happens while
   Steam could still rewrite its config files on exit.
8. **Uninstall** mode cleanly reverses all of the above (also closing Steam
   after confirmation)

## Linux differences vs the Windows installer

- Uses `-javaagent:ZombieBuddy.jar` instead of `-agentlib:zbNative`.
  The `zbNative.dll` native helper is a Windows-only JRE workaround and is
  **not needed on Linux** — the installer tells you when it sees it and
  skips it. (This matches the official ZombieBuddy documentation.)
- Detects Steam by probing the well-known Linux install locations instead of
  reading the Windows registry.
- Detects Steam processes via `/proc` instead of `tasklist`.

## Extra command line flags

```text
--steam-dir PATH   use this Steam installation directory (auto-detected by default)
--jar PATH         install this ZombieBuddy.jar instead of the Workshop copy
```

## First launch tip

On first launch ZombieBuddy asks you to approve every Java mod (security
prompt). On some desktop environments the approval dialog does not appear —
start the game once from a terminal (`cd <game folder> && ./projectzomboid.sh`)
and answer the prompt there.

## Uninstall leftovers

ZombieBuddy stores mod approval decisions in `~/.zombie_buddy` — delete that
folder manually if you want a fully clean removal.

## Source code layout

```
zombiebuddy-installer-linux-4.2-src/
├── build.sh              one-command build: source -> AppImage
├── installer/            Go source of the installer itself
│   ├── main.go           menus, install/uninstall flow
│   ├── detect.go         Steam / PZ / Workshop discovery (native, Flatpak, Snap)
│   ├── patch.go          JSON / launch-script / localconfig.vdf patching
│   ├── steamcontrol.go   automatic Steam shutdown after confirmation
│   └── go.mod, go.sum    module deps (github.com/andygrunwald/vdf)
├── appimage/             AppImage packaging assets
│   ├── AppRun            terminal detection + terminal-emulator fallback chain
│   ├── zombiebuddy-installer.desktop
│   └── icons/            ZombieBuddy icons (128 / 256 px)
└── tests/                shell test suites (78 checks total)
    ├── test-installer.sh main flows: install / uninstall / cancel
    ├── test-edge.sh      Flatpak home, B41 layout, overrides, odd files
    ├── test-steamclose.sh  automatic Steam-close behaviour
    └── fake-steam.go     helper that masquerades as a Steam process
```

## Building from source

```bash
./build.sh        # needs Go 1.25+ (https://go.dev/dl/) and curl
```

Produces `ZombieBuddyInstaller-Linux-v4.2-x86_64.AppImage` in the current
directory (downloaded appimagetool and intermediate files land in `build/`).
The binary is compiled statically (`CGO_ENABLED=0`) — no root rights or
system libraries are required.



## Provenance

Linux port of `ZombieBuddyInstaller_v4.2.exe` from the ZombieBuddy project
(https://github.com/zed-0xff/ZombieBuddy, Workshop item 3619862853).
Windows original by zed-0xff; Linux port by Kiwi.
