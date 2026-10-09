package main

import (
        "fmt"
        "os"
        "path/filepath"
        "sort"
        "strings"

        "github.com/andygrunwald/vdf"
)

// steamHomeCandidate describes a place a Linux Steam installation can live.
type steamHomeCandidate struct {
        path string
        kind string // "native", "flatpak", "snap", "custom"
}

// detectSteamPath finds the Steam root directory on Linux.
//
// Linux has no registry, so we probe the well-known installation locations
// (native Debian/RPM tarball install, Flatpak, Snap), plus an optional
// override. A valid Steam root must contain a "steamapps" directory (or the
// steam.sh launcher / libraryfolders.vdf which older layouts use).
func detectSteamPath() (string, string, error) {
        home, err := os.UserHomeDir()
        if err != nil {
                return "", "", fmt.Errorf("could not determine home directory: %v", err)
        }

        candidates := []steamHomeCandidate{}
        add := func(path, kind string) {
                if path == "" {
                        return
                }
                if !filepath.IsAbs(path) {
                        abs, err := filepath.Abs(path)
                        if err == nil {
                                path = abs
                        }
                }
                candidates = append(candidates, steamHomeCandidate{path: path, kind: kind})
        }

        if steamDirOverride != "" {
                add(steamDirOverride, "custom")
        }
        if env := os.Getenv("STEAM_DIR"); env != "" {
                add(env, "custom")
        }

        // native Steam (deb/rpm/manual tarball)
        add(filepath.Join(home, ".steam", "steam"), "native")
        add(filepath.Join(home, ".local", "share", "Steam"), "native")
        add(filepath.Join(home, ".steam", "root"), "native")
        add(filepath.Join(home, ".steam", "debian-installation"), "native")

        // Flatpak Steam
        add(filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"), "flatpak")

        // Snap Steam
        add(filepath.Join(home, "snap", "steam", "common", ".local", "share", "Steam"), "snap")
        add(filepath.Join(home, "snap", "steam", "current", ".local", "share", "Steam"), "snap")

        seen := map[string]bool{}
        var found []steamHomeCandidate
        for _, c := range candidates {
                resolved, err := filepath.EvalSymlinks(c.path)
                if err != nil {
                        continue // does not exist
                }
                info, err := os.Stat(resolved)
                if err != nil || !info.IsDir() {
                        continue
                }
                if !looksLikeSteamRoot(resolved) {
                        continue
                }
                if seen[resolved] {
                        continue
                }
                seen[resolved] = true
                found = append(found, steamHomeCandidate{path: resolved, kind: c.kind})
        }

        if len(found) == 0 {
                return "", "", fmt.Errorf("could not find Steam installation (tried ~/.steam/steam, ~/.local/share/Steam, Flatpak and Snap locations)\n    If Steam lives somewhere else, re-run with: --steam-dir /path/to/Steam")
        }

        // Prefer native, then flatpak, then snap, then custom (custom only wins when it is the only one).
        order := map[string]int{"native": 0, "custom": 1, "flatpak": 2, "snap": 3}
        sort.SliceStable(found, func(i, j int) bool {
                return order[found[i].kind] < order[found[j].kind]
        })

        if len(found) > 1 {
                fmt.Println("[i] Multiple Steam installations found:")
                for i, c := range found {
                        marker := ""
                        if i == 0 {
                                marker = "  (using this one)"
                        }
                        fmt.Printf("    [%s] %s%s\n", c.kind, c.path, marker)
                }
                fmt.Println("    (use --steam-dir to pick a different one)")
        }
        return found[0].path, found[0].kind, nil
}

func looksLikeSteamRoot(path string) bool {
        for _, probe := range []string{
                filepath.Join(path, "steamapps"),
                filepath.Join(path, "steam.sh"),
                filepath.Join(path, "steamapps", "libraryfolders.vdf"),
                filepath.Join(path, "ubuntu12_32", "steam"),
                filepath.Join(path, "steamclientPublic.err"),
        } {
                if _, err := os.Stat(probe); err == nil {
                        return true
                }
        }
        return false
}

func detectInstallPaths(includeZB bool) (installPaths, error) {
        steamPath, steamKind, err := detectSteamPath()
        if err != nil {
                return installPaths{}, fmt.Errorf("Error detecting Steam: %v", err)
        }
        fmt.Printf("[.] %-6s is at %s (%s)\n", "Steam", steamPath, steamKind)

        // Remember for late safety-net Steam shutdown calls.
        detectedSteamDir = steamPath
        detectedSteamKind = steamKind

        pzPath, err := detectPZPath(steamPath)
        if err != nil {
                return installPaths{}, fmt.Errorf("Error detecting Project Zomboid: %v", err)
        }
        fmt.Printf("[.] %-6s is at %s\n", "PZ", pzPath)

        paths := installPaths{steam: steamPath, steamKind: steamKind, pz: pzPath}

        // Figure out where the game's Java side lives (B42 keeps the launcher
        // config and runs the JVM inside the "projectzomboid" subdirectory).
        paths.javaDir, paths.jsonPath, paths.launchSh = detectPZLaunchFiles(pzPath)
        detectedSh = paths.launchSh
        if paths.jsonPath != "" {
                fmt.Printf("[.] %-6s is at %s\n", "JSON", paths.jsonPath)
        } else {
                fmt.Println("[i] ProjectZomboid64.json not found (normal launcher patch will be skipped)")
        }
        if paths.launchSh != "" {
                fmt.Printf("[.] %-6s is at %s\n", "SH", paths.launchSh)
        } else {
                fmt.Println("[i] projectzomboid.sh not found (alternate launcher patch will be skipped)")
        }

        // Safety net: a Windows (Proton/Wine) game install also sits in
        // steamapps/common/ProjectZomboid, but it ships ProjectZomboid64.bat
        // and no Linux launch script. ZombieBuddy's Linux setup (pure
        // -javaagent) targets the native Linux build, so warn instead of
        // patching blindly.
        if paths.launchSh == "" {
                batPath := filepath.Join(pzPath, "ProjectZomboid64.bat")
                subDir := filepath.Join(pzPath, "projectzomboid")
                _, batErr := os.Stat(batPath)
                _, subErr := os.Stat(subDir)
                if batErr == nil && subErr != nil {
                        fmt.Println("[!] This appears to be a Windows (Proton/Wine) install of Project Zomboid.")
                        fmt.Println("    The Linux version of ZombieBuddy needs the native Linux game build")
                        fmt.Println("    (Steam -> Project Zomboid -> Properties -> Compatibility -> disable")
                        fmt.Println("    Proton, then let Steam download the Linux build and re-run this installer).")
                }
        }

        if includeZB {
                zbPath, err := detectZBPath(steamPath)
                if err != nil {
                        if jarOverride != "" {
                                // An explicit --jar was given: Workshop content is optional.
                                fmt.Println("[i] ZombieBuddy Workshop content not found - using the --jar override")
                        } else {
                                return installPaths{}, fmt.Errorf("Error detecting ZombieBuddy mod: %v", err)
                        }
                } else {
                        fmt.Printf("[.] %-6s is at %s\n", "ZB", zbPath)
                        paths.zb = zbPath
                }
        }
        return paths, nil
}

// detectPZLaunchFiles locates, inside the Project Zomboid install:
//   - the directory the JVM uses as working directory (javaDir)
//   - ProjectZomboid64.json (launch config of the official launcher)
//   - the alternate launch shell script
//
// Build 42 Linux layout:  ProjectZomboid/projectzomboid.sh
//
//      ProjectZomboid/projectzomboid/ProjectZomboid64.json
//
// Build 41 / other layout: ProjectZomboid/ProjectZomboid64.sh
//
//      ProjectZomboid/ProjectZomboid64.json
func detectPZLaunchFiles(pzPath string) (javaDir string, jsonPath string, launchSh string) {
        sub := filepath.Join(pzPath, "projectzomboid")

        jsonCandidates := []string{
                filepath.Join(sub, "ProjectZomboid64.json"),
                filepath.Join(pzPath, "ProjectZomboid64.json"),
        }
        for _, c := range jsonCandidates {
                if _, err := os.Stat(c); err == nil {
                        jsonPath = c
                        break
                }
        }

        if jsonPath != "" {
                javaDir = filepath.Dir(jsonPath)
        } else {
                // No launcher config found: fall back to the B42 subdir when it
                // exists (jvm working dir), else the game root.
                if info, err := os.Stat(sub); err == nil && info.IsDir() {
                        javaDir = sub
                } else {
                        javaDir = pzPath
                }
        }

        shCandidates := []string{
                filepath.Join(pzPath, "projectzomboid.sh"),
                filepath.Join(pzPath, "ProjectZomboid64.sh"),
                filepath.Join(sub, "projectzomboid.sh"),
        }
        for _, c := range shCandidates {
                if _, err := os.Stat(c); err == nil {
                        launchSh = c
                        break
                }
        }
        return javaDir, jsonPath, launchSh
}

func detectPZPath(steamPath string) (string, error) {
        // First check the default path
        defaultPath := filepath.Join(steamPath, "steamapps", "common", "ProjectZomboid")
        if isPZInstall(defaultPath) {
                return defaultPath, nil
        }

        // Then check libraryfolders.vdf
        libraryVDF := filepath.Join(steamPath, "steamapps", "libraryfolders.vdf")
        if _, err := os.Stat(libraryVDF); err == nil {
                return findAppInLibraries(libraryVDF, "common", "ProjectZomboid")
        }

        return "", fmt.Errorf("could not find Project Zomboid installation")
}

func isPZInstall(path string) bool {
        if _, err := os.Stat(path); err != nil {
                return false
        }
        // Sanity check: the game dir should contain at least one recognizable file.
        for _, probe := range []string{
                filepath.Join(path, "projectzomboid.sh"),
                filepath.Join(path, "ProjectZomboid64.sh"),
                filepath.Join(path, "ProjectZomboid64.json"),
                filepath.Join(path, "projectzomboid", "ProjectZomboid64.json"),
                filepath.Join(path, "projectzomboid", "ProjectZomboid64"),
                filepath.Join(path, "zombie"),
        } {
                if _, err := os.Stat(probe); err == nil {
                        return true
                }
        }
        return false
}

func detectZBPath(steamPath string) (string, error) {
        // First check the default path
        defaultPath := filepath.Join(steamPath, "steamapps", "workshop", "content", PZ_APP_ID, ZB_MOD_ID)
        if _, err := os.Stat(defaultPath); err == nil {
                return defaultPath, nil
        }

        // Then check libraryfolders.vdf
        libraryVDF := filepath.Join(steamPath, "steamapps", "libraryfolders.vdf")
        if _, err := os.Stat(libraryVDF); err == nil {
                return findAppInLibraries(libraryVDF, "workshop", "content", PZ_APP_ID, ZB_MOD_ID)
        }

        return "", fmt.Errorf("could not find the ZombieBuddy Workshop content - make sure you are subscribed to\n    " + ZB_MODS_URL)
}

// findAppInLibraries scans every library from libraryfolders.vdf looking for
// the given sub path (e.g. steamapps/common/ProjectZomboid).
func findAppInLibraries(libraryVDFPath string, subPath ...string) (string, error) {
        m, err := parseVDFMap(libraryVDFPath)
        if err != nil {
                return "", err
        }

        libraryfolders, ok := m["libraryfolders"].(map[string]interface{})
        if !ok {
                return "", fmt.Errorf("invalid libraryfolders.vdf")
        }

        // Iterate libraries in index order ("0", "1", ...) for determinism.
        indices := make([]string, 0, len(libraryfolders))
        for k := range libraryfolders {
                indices = append(indices, k)
        }
        sort.Slice(indices, func(i, j int) bool {
                return libraryIndexLess(indices[i], indices[j])
        })

        var missing []string
        for _, idx := range indices {
                folderMap, ok := libraryfolders[idx].(map[string]interface{})
                if !ok {
                        continue
                }
                path, ok := folderMap["path"].(string)
                if !ok || path == "" {
                        continue
                }

                fullSubPath := append([]string{"steamapps"}, subPath...)
                fullPath := filepath.Join(path, filepath.Join(fullSubPath...))
                if _, err := os.Stat(fullPath); err == nil {
                        return fullPath, nil
                }
                missing = append(missing, path)
        }

        _ = missing
        return "", fmt.Errorf("app not found in any Steam library (checked %d libraries)", len(indices))
}

func libraryIndexLess(a, b string) bool {
        ai, aerr := parseUint(a)
        bi, berr := parseUint(b)
        if aerr == nil && berr == nil {
                return ai < bi
        }
        return a < b
}

func parseUint(s string) (uint64, error) {
        var v uint64
        if s == "" {
                return 0, fmt.Errorf("empty")
        }
        for _, c := range s {
                if c < '0' || c > '9' {
                        return 0, fmt.Errorf("not a number")
                }
                v = v*10 + uint64(c-'0')
        }
        return v, nil
}

func parseVDFMap(path string) (map[string]interface{}, error) {
        f, err := os.Open(path)
        if err != nil {
                return nil, err
        }
        defer f.Close()

        p := vdf.NewParser(f)
        return p.Parse()
}

func navigateMap(m map[string]interface{}, path ...string) (map[string]interface{}, error) {
        current := m
        for _, key := range path {
                found := false
                for k, v := range current {
                        if strings.EqualFold(k, key) {
                                next, ok := v.(map[string]interface{})
                                if !ok {
                                        return nil, fmt.Errorf("key %s is not a map", k)
                                }
                                current = next
                                found = true
                                break
                        }
                }
                if !found {
                        return nil, fmt.Errorf("key %s not found", key)
                }
        }
        return current, nil
}

func formatMapKeys(m map[string]interface{}) string {
        if len(m) == 0 {
                return "none"
        }

        keys := make([]string, 0, len(m))
        for key := range m {
                keys = append(keys, key)
        }
        sort.Strings(keys)
        return strings.Join(keys, ", ")
}
