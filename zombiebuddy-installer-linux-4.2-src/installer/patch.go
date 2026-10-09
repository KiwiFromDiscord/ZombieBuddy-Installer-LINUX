package main

import (
        "bytes"
        "encoding/json"
        "fmt"
        "io"
        "os"
        "path/filepath"
        "regexp"
        "strings"
)

// ---------------------------------------------------------------------------
// Java option helpers (Linux flavor)
//
// On Linux ZombieBuddy is loaded with -javaagent:ZombieBuddy.jar (the jar is
// pure Java and needs no native helper on this platform). We still recognize
// the Windows-style -agentlib:zbNative tokens so machines that previously
// tried the Windows setup get cleaned up properly.
// ---------------------------------------------------------------------------

func isZombieBuddyJavaOption(value string) bool {
        if value == "-agentlib:zbNative" || strings.HasPrefix(value, "-agentlib:zbNative=") {
                return true
        }
        if strings.HasPrefix(value, "-javaagent:") {
                path := strings.TrimPrefix(value, "-javaagent:")
                path = strings.SplitN(path, "=", 2)[0]
                return strings.HasSuffix(filepath.ToSlash(path), "ZombieBuddy.jar")
        }
        return false
}

func addZombieBuddyJavaOption(options string) (string, bool) {
        if containsZombieBuddyJavaOption(options) {
                return options, false
        }
        if strings.TrimSpace(options) == "" {
                return ZB_LAUNCH_ARG, true
        }
        return ZB_LAUNCH_ARG + " " + options, true
}

func removeZombieBuddyJavaOption(options string) (string, bool) {
        fields := strings.Fields(options)
        filtered := make([]string, 0, len(fields))
        changed := false
        for _, field := range fields {
                if isZombieBuddyJavaOption(field) {
                        changed = true
                        continue
                }
                filtered = append(filtered, field)
        }
        if !changed {
                return options, false
        }
        return strings.Join(filtered, " "), true
}

func containsZombieBuddyJavaOption(options string) bool {
        for _, field := range strings.Fields(options) {
                if isZombieBuddyJavaOption(field) {
                        return true
                }
        }
        return false
}

// ---------------------------------------------------------------------------
// Core file copy
// ---------------------------------------------------------------------------

// copyCoreFiles copies ZombieBuddy.jar (and, if present, a Linux native
// library) into the JVM working directory of the game.
//
// Source priority:
//  1. --jar flag
//  2. Steam Workshop content (mods/ZombieBuddy/libs/)
//  3. ~/Zomboid/mods/ZombieBuddy/libs/ (manual install location)
//  4. next to the AppImage / executable
//  5. an already-installed jar (keeps going so launch configs can still be
//     re-patched after a game update)
func copyCoreFiles(paths installPaths) error {
        home, _ := os.UserHomeDir()

        sources := []string{}
        if jarOverride != "" {
                sources = append(sources, jarOverride)
        }
        if paths.zb != "" {
                sources = append(sources, filepath.Join(paths.zb, "mods", "ZombieBuddy", "libs", ZB_CORE_JAR))
        }
        if home != "" {
                sources = append(sources, filepath.Join(home, "Zomboid", "mods", "ZombieBuddy", "libs", ZB_CORE_JAR))
        }
        for _, base := range extraPayloadDirs() {
                sources = append(sources, filepath.Join(base, ZB_CORE_JAR))
        }
        dstPath := filepath.Join(paths.javaDir, ZB_CORE_JAR)
        sources = append(sources, dstPath) // already-installed fallback

        var srcPath string
        for _, s := range sources {
                if s == "" {
                        continue
                }
                if _, err := os.Stat(s); err == nil {
                        srcPath = s
                        break
                }
        }
        if srcPath == "" {
                return fmt.Errorf("could not find ZombieBuddy.jar source.\n    Subscribe to the ZombieBuddy Workshop item first: " + ZB_MODS_URL +
                        "\n    or re-run with: --jar /path/to/ZombieBuddy.jar")
        }
        if srcPath == dstPath {
                fmt.Println("[i] No update source found - keeping the already installed ZombieBuddy.jar")
        } else {
                fmt.Printf("[.] copying %s to %s\n", quotePath(srcPath), quotePath(dstPath))
                if err := copyFile(srcPath, dstPath); err != nil {
                        return fmt.Errorf("failed to copy ZombieBuddy.jar: %v", err)
                }
        }

        // Windows native helper: not needed on Linux, mention it when present.
        if paths.zb != "" {
                if _, err := os.Stat(filepath.Join(paths.zb, "mods", "ZombieBuddy", "libs", ZB_CORE_DLL)); err == nil {
                        fmt.Println("[i] zbNative.dll found in Workshop content - it is Windows-only and not needed on Linux, skipped")
                }
        }

        // Future-proofing: copy a Linux native library if one ever ships.
        if paths.zb != "" {
                soSrc := filepath.Join(paths.zb, "mods", "ZombieBuddy", "libs", ZB_CORE_SO)
                soDst := filepath.Join(paths.javaDir, ZB_CORE_SO)
                if _, err := os.Stat(soSrc); err == nil {
                        fmt.Printf("[.] copying %s to %s\n", quotePath(soSrc), quotePath(soDst))
                        if err := copyFile(soSrc, soDst); err != nil {
                                return fmt.Errorf("failed to copy libzbNative.so: %v", err)
                        }
                }
        }

        return nil
}

// extraPayloadDirs returns directories where a user may have dropped
// ZombieBuddy.jar manually: next to the AppImage, or next to the executable.
func extraPayloadDirs() []string {
        var dirs []string
        if appimage := os.Getenv("APPIMAGE"); appimage != "" {
                dirs = append(dirs, filepath.Dir(appimage))
        }
        if exe, err := os.Executable(); err == nil {
                if resolved, err := filepath.EvalSymlinks(exe); err == nil {
                        dirs = append(dirs, filepath.Dir(resolved))
                } else {
                        dirs = append(dirs, filepath.Dir(exe))
                }
        }
        if wd, err := os.Getwd(); err == nil {
                dirs = append(dirs, wd)
        }
        return dirs
}

func copyFile(src, dst string) error {
        sourceFile, err := os.Open(src)
        if err != nil {
                return err
        }
        defer sourceFile.Close()

        destFile, err := os.Create(dst)
        if err != nil {
                return err
        }
        defer destFile.Close()

        _, err = io.Copy(destFile, sourceFile)
        return err
}

// ---------------------------------------------------------------------------
// ProjectZomboid64.json patching
//
// Unlike a struct-based round trip (which silently drops unknown keys), we
// parse the launcher config as an ordered map of raw JSON values so every
// field survives the rewrite untouched - only "vmArgs" is replaced.
// ---------------------------------------------------------------------------

type jsonLauncher struct {
        keys    []string
        raw     map[string]json.RawMessage
        vmArgs  []string
        hasArgs bool
}

func readJSONLauncher(path string) (*jsonLauncher, bool, error) {
        input, err := os.ReadFile(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return nil, false, nil
                }
                return nil, false, err
        }

        dec := json.NewDecoder(bytes.NewReader(input))
        tok, err := dec.Token()
        if err != nil {
                return nil, false, fmt.Errorf("invalid JSON: %v", err)
        }
        if delim, ok := tok.(json.Delim); !ok || delim != '{' {
                return nil, false, fmt.Errorf("invalid launcher config: expected a JSON object")
        }

        l := &jsonLauncher{raw: map[string]json.RawMessage{}}
        for dec.More() {
                keyTok, err := dec.Token()
                if err != nil {
                        return nil, false, fmt.Errorf("invalid JSON: %v", err)
                }
                key, ok := keyTok.(string)
                if !ok {
                        return nil, false, fmt.Errorf("invalid launcher config: unexpected key %v", keyTok)
                }
                var raw json.RawMessage
                if err := dec.Decode(&raw); err != nil {
                        return nil, false, fmt.Errorf("invalid value for %q: %v", key, err)
                }
                if _, dup := l.raw[key]; !dup {
                        l.keys = append(l.keys, key)
                }
                l.raw[key] = raw

                if strings.EqualFold(key, "vmArgs") {
                        var args []string
                        if err := json.Unmarshal(raw, &args); err != nil {
                                return nil, false, fmt.Errorf("invalid vmArgs array: %v", err)
                        }
                        l.vmArgs = args
                        l.hasArgs = true
                }
        }
        if _, err := dec.Token(); err != nil { // consume closing '}'
                return nil, false, fmt.Errorf("invalid JSON: %v", err)
        }
        return l, true, nil
}

// write re-serializes the launcher config, replacing (or appending) the
// vmArgs array, preserving key order and all other fields byte-for-byte.
func (l *jsonLauncher) write(path string, vmArgs []string) error {
        b, err := json.Marshal(vmArgs)
        if err != nil {
                return err
        }
        raw := make(map[string]json.RawMessage, len(l.raw))
        for k, v := range l.raw {
                raw[k] = append(json.RawMessage(nil), v...)
        }
        raw["vmArgs"] = b
        keys := append([]string(nil), l.keys...)
        if !l.hasArgs {
                keys = append(keys, "vmArgs")
        }

        var buf bytes.Buffer
        buf.WriteString("{\n")
        for i, key := range keys {
                keyBytes, _ := json.Marshal(key)
                buf.WriteString("\t")
                buf.Write(keyBytes)
                buf.WriteString(": ")
                var pretty bytes.Buffer
                if err := json.Indent(&pretty, raw[key], "\t", "\t"); err != nil {
                        return err
                }
                buf.Write(pretty.Bytes())
                if i < len(keys)-1 {
                        buf.WriteString(",")
                }
                buf.WriteString("\n")
        }
        buf.WriteString("}")

        out := append(buf.Bytes(), '\n')
        return os.WriteFile(path, out, 0644)
}

func patchJSONLauncher(paths installPaths) (bool, error) {
        if paths.jsonPath == "" {
                return false, nil
        }
        return updateJSONLauncherVMArgs(paths.jsonPath, true)
}

func updateJSONLauncherVMArgs(path string, install bool) (bool, error) {
        l, found, err := readJSONLauncher(path)
        if err != nil || !found {
                return false, err
        }

        args := l.vmArgs
        var changed bool
        if install {
                args, changed = addZombieBuddyVMArg(args)
        } else {
                args, changed = removeZombieBuddyVMArg(args)
        }
        if !changed {
                return false, nil
        }
        return true, l.write(path, args)
}

func jsonLauncherHasZombieBuddy(path string) (bool, error) {
        l, found, err := readJSONLauncher(path)
        if err != nil || !found {
                return false, err
        }
        return hasZombieBuddyVMArg(l.vmArgs), nil
}

func addZombieBuddyVMArg(args []string) ([]string, bool) {
        if hasZombieBuddyVMArg(args) {
                return args, false
        }
        return append([]string{ZB_LAUNCH_ARG}, args...), true
}

func hasZombieBuddyVMArg(args []string) bool {
        for _, arg := range args {
                if isZombieBuddyJavaOption(arg) {
                        return true
                }
        }
        return false
}

func removeZombieBuddyVMArg(args []string) ([]string, bool) {
        filtered := make([]string, 0, len(args))
        changed := false
        for _, arg := range args {
                if isZombieBuddyJavaOption(arg) {
                        changed = true
                        continue
                }
                filtered = append(filtered, arg)
        }
        if !changed {
                return args, false
        }
        return filtered, true
}

// ---------------------------------------------------------------------------
// Launch script patching (projectzomboid.sh / ProjectZomboid64.sh)
//
// Linux equivalent of the ProjectZomboid64.bat patch: keep an
// "export _JAVA_OPTIONS=..." assignment in the launch script so every JVM
// the game starts loads the ZombieBuddy agent.
// ---------------------------------------------------------------------------

var shJavaOptionsRe = regexp.MustCompile(`^(\s*)(?:(export)\s+)?(_JAVA_OPTIONS)\s*=`)

func patchLaunchScript(paths installPaths) (bool, error) {
        if paths.launchSh == "" {
                return false, nil
        }
        return updateBatchJavaOptions(paths.launchSh, true)
}

func updateBatchJavaOptions(path string, install bool) (bool, error) {
        return updateBatchJavaOptionsValue(path, func(currentValue string) (string, bool) {
                if install {
                        return addZombieBuddyJavaOption(currentValue)
                }
                return removeZombieBuddyJavaOption(currentValue)
        }, install)
}

func updateBatchJavaOptionsValue(path string, mutate func(string) (string, bool), addIfMissing bool) (bool, error) {
        input, err := os.ReadFile(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return false, nil
                }
                return false, err
        }

        content := string(input)
        newline := "\n"
        if strings.Contains(content, "\r\n") {
                newline = "\r\n"
        } else if strings.Contains(content, "\r") {
                newline = "\r"
        }

        normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
        lines := strings.Split(normalized, "\n")
        updated := false
        foundJavaOptions := false

        for i, line := range lines {
                keyStart, valueStart, ok := javaOptionsAssignment(line)
                if !ok {
                        continue
                }

                foundJavaOptions = true
                currentValue := unquoteShValue(strings.TrimSpace(line[valueStart:]))
                newValue, changed := mutate(currentValue)
                if changed {
                        if strings.TrimSpace(newValue) == "" {
                                // removing the last option: on a shell script an empty
                                // assignment would still export _JAVA_OPTIONS="" (the JVM
                                // would then print "Picked up _JAVA_OPTIONS" on every
                                // launch), so clear it explicitly instead.
                                lines[i] = line[:keyStart] + "unset _JAVA_OPTIONS"
                        } else {
                                lines[i] = line[:keyStart] + "export _JAVA_OPTIONS=" + shQuote(newValue)
                        }
                        updated = true
                }
                break
        }

        if addIfMissing && !foundJavaOptions {
                insert := "export _JAVA_OPTIONS=" + shQuote(ZB_LAUNCH_ARG)
                if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
                        lines = append([]string{lines[0], insert}, lines[1:]...)
                } else {
                        lines = append([]string{insert}, lines...)
                }
                updated = true
        }

        if !updated {
                return false, nil
        }
        return true, os.WriteFile(path, []byte(strings.Join(lines, newline)), 0644)
}

// javaOptionsAssignment finds a "_JAVA_OPTIONS=" (or "export _JAVA_OPTIONS=")
// assignment in a shell script line and returns the byte offsets of the start
// of the statement and of the value.
func javaOptionsAssignment(line string) (int, int, bool) {
        m := shJavaOptionsRe.FindStringSubmatch(line)
        if m == nil {
                return 0, 0, false
        }
        keyStart := len(m[1])
        valueStart := len(m[0])
        return keyStart, valueStart, true
}

func unquoteShValue(v string) string {
        if len(v) >= 2 {
                if (v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"') {
                        return v[1 : len(v)-1]
                }
        }
        return v
}

// shQuote wraps a value in single quotes so it survives shell re-parsing.
func shQuote(v string) string {
        return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func batchFileHasZombieBuddy(path string) (bool, error) {
        value, found, err := readBatchJavaOptions(path)
        if err != nil || !found {
                return false, err
        }
        return containsZombieBuddyJavaOption(value), nil
}

func readBatchJavaOptions(path string) (string, bool, error) {
        input, err := os.ReadFile(path)
        if err != nil {
                if os.IsNotExist(err) {
                        return "", false, nil
                }
                return "", false, err
        }

        normalized := strings.ReplaceAll(strings.ReplaceAll(string(input), "\r\n", "\n"), "\r", "\n")
        for _, line := range strings.Split(normalized, "\n") {
                _, valueStart, ok := javaOptionsAssignment(line)
                if !ok {
                        continue
                }
                return unquoteShValue(strings.TrimSpace(line[valueStart:])), true, nil
        }
        return "", false, nil
}

// ---------------------------------------------------------------------------
// Steam launch options (localconfig.vdf)
// ---------------------------------------------------------------------------

var steamZombieBuddyLaunchOptionPattern = regexp.MustCompile(`(^|\s)(?:-agentlib:zbNative(?:=\S*)?|-javaagent:\S*ZombieBuddy\.jar(?:=\S*)?)[ ]*[-]*`)

func updateLaunchOptions(steamPath string) error {
        userdataPath := filepath.Join(steamPath, "userdata")
        entries, err := os.ReadDir(userdataPath)
        if err != nil {
                return err
        }

        updatedCount := 0
        for _, entry := range entries {
                if entry.IsDir() {
                        localConfigPath := filepath.Join(userdataPath, entry.Name(), "config", "localconfig.vdf")
                        if _, err := os.Stat(localConfigPath); err == nil {
                                updated, err := patchVDF(localConfigPath)
                                if err == nil {
                                        if updated {
                                                fmt.Printf("[.] Updated launch options for user %s\n", entry.Name())
                                        }
                                        updatedCount++
                                } else {
                                        fmt.Printf("[!] Failed to update launch options for user %s: %v\n", entry.Name(), err)
                                }
                        }
                }
        }

        if updatedCount == 0 {
                return fmt.Errorf("no user configurations found to update")
        }
        return nil
}

func removeLaunchOptions(localConfigPaths []string) error {
        if len(localConfigPaths) == 0 {
                return nil
        }

        waitForSteamToClose("launch option update")
        for _, localConfigPath := range localConfigPaths {
                updated, err := unpatchVDF(localConfigPath, false)
                if err != nil {
                        return fmt.Errorf("failed to remove launch options from %s: %v", localConfigPath, err)
                }
                if updated {
                        fmt.Printf("[.] Removed launch options from %s\n", localConfigPath)
                }
        }
        return nil
}

func steamLaunchOptionRemovalPlan(steamPath string) ([]string, error) {
        userdataPath := filepath.Join(steamPath, "userdata")
        entries, err := os.ReadDir(userdataPath)
        if err != nil {
                if os.IsNotExist(err) {
                        // No userdata directory: nothing to remove - don't block uninstall.
                        return nil, nil
                }
                return nil, err
        }

        var localConfigPaths []string
        for _, entry := range entries {
                if entry.IsDir() {
                        localConfigPath := filepath.Join(userdataPath, entry.Name(), "config", "localconfig.vdf")
                        if _, err := os.Stat(localConfigPath); err == nil {
                                has, err := vdfHasZombieBuddyLaunchOptions(localConfigPath)
                                if err != nil {
                                        fmt.Printf("[?] skipping Steam launch options for user %s: %v\n", entry.Name(), err)
                                        continue
                                }
                                if has {
                                        localConfigPaths = append(localConfigPaths, localConfigPath)
                                }
                        }
                }
        }
        return localConfigPaths, nil
}

func patchVDF(path string) (bool, error) {
        pz, err := findPZLaunchConfig(path, true)
        if err != nil {
                return false, err
        }

        if pz == nil {
                return false, fmt.Errorf("could not create Project Zomboid launch config")
        }

        currentOptions := launchOptionsFromConfig(pz)

        newOptions := ZB_LAUNCH_OPTIONS
        if hasZombieBuddyLaunchOptions(currentOptions) {
                fmt.Println("[-] Launch options already contain ZombieBuddy agent.")
                return false, nil
        }

        if strings.TrimSpace(currentOptions) != "" {
                fmt.Printf("[i] current PZ Steam launch options: %s (they will be replaced)\n", quote(currentOptions))
        }

        waitForSteamToClose("launch option update")
        return true, manualPatchVDF(path, currentOptions, newOptions)
}

func unpatchVDF(path string, waitForSteam bool) (bool, error) {
        currentOptions, err := readPZLaunchOptions(path)
        if err != nil {
                return false, err
        }

        newOptions, changed := stripZombieBuddyLaunchOptions(currentOptions)
        if !changed {
                return false, nil
        }

        if waitForSteam {
                waitForSteamToClose("launch option update")
        }
        return true, manualPatchVDF(path, currentOptions, newOptions)
}

func vdfHasZombieBuddyLaunchOptions(path string) (bool, error) {
        currentOptions, err := readPZLaunchOptions(path)
        if err != nil {
                return false, err
        }
        return hasZombieBuddyLaunchOptions(currentOptions), nil
}

func readPZLaunchOptions(path string) (string, error) {
        pz, err := findPZLaunchConfig(path, false)
        if err != nil || pz == nil {
                return "", err
        }
        return launchOptionsFromConfig(pz), nil
}

func findPZLaunchConfig(path string, create bool) (map[string]interface{}, error) {
        m, err := parseVDFMap(path)
        if err != nil {
                return nil, err
        }

        apps, err := navigateMap(m, "UserLocalConfigStore", "Software", "Valve", "Steam", "Apps")
        if err != nil {
                return nil, fmt.Errorf("%v (root keys: %s)", err, formatMapKeys(m))
        }

        var pz map[string]interface{}
        for k, v := range apps {
                if strings.EqualFold(k, PZ_APP_ID) {
                        pz, _ = v.(map[string]interface{})
                        break
                }
        }
        if pz == nil {
                if !create {
                        return nil, nil
                }
                pz = make(map[string]interface{})
                apps[PZ_APP_ID] = pz
        }
        return pz, nil
}

func launchOptionsFromConfig(pz map[string]interface{}) string {
        for k, v := range pz {
                if strings.EqualFold(k, "LaunchOptions") {
                        currentOptions, _ := v.(string)
                        return currentOptions
                }
        }
        return ""
}

func hasZombieBuddyLaunchOptions(options string) bool {
        _, changed := stripZombieBuddyLaunchOptions(options)
        return changed
}

func stripZombieBuddyLaunchOptions(options string) (string, bool) {
        newOptions := strings.Join(strings.Fields(steamZombieBuddyLaunchOptionPattern.ReplaceAllString(options, " ")), " ")
        if newOptions == strings.TrimSpace(options) {
                return options, false
        }
        return newOptions, true
}

// manualPatchVDF performs a text-level edit of localconfig.vdf to set the
// Project Zomboid "LaunchOptions" value, preserving the rest of the file
// byte-for-byte (Steam rewrites this file, so minimal diffs matter).
func manualPatchVDF(path, oldOpts, newOpts string) error {
        input, err := os.ReadFile(path)
        if err != nil {
                return err
        }

        newline := "\n"
        if strings.Contains(string(input), "\r\n") {
                newline = "\r\n"
        }
        lines := strings.Split(strings.ReplaceAll(string(input), "\r\n", "\n"), "\n")
        foundApp := false
        inApp := 0

        var output []string
        launchOptionsFound := false

        for _, line := range lines {
                trimmed := strings.TrimSpace(line)

                if !foundApp && (strings.EqualFold(trimmed, "\""+PZ_APP_ID+"\"") || strings.EqualFold(trimmed, PZ_APP_ID)) {
                        foundApp = true
                }

                if foundApp {
                        if strings.Contains(line, "{") {
                                inApp++
                        }
                        if strings.Contains(line, "}") {
                                inApp--
                                if inApp == 0 {
                                        if !launchOptionsFound {
                                                // Add launch options before closing the app block
                                                output = append(output, fmt.Sprintf("\t\t\t\t\t\"LaunchOptions\"\t\t\"%s\"", newOpts))
                                        }
                                        foundApp = false
                                }
                        }

                        if inApp > 0 && strings.Contains(strings.ToLower(trimmed), "\"launchoptions\"") {
                                // Replace existing launch options
                                parts := strings.SplitN(line, "\"", 4)
                                if len(parts) >= 4 {
                                        line = parts[0] + "\"" + parts[1] + "\"" + parts[2] + "\"" + newOpts + "\""
                                        launchOptionsFound = true
                                }
                        }
                }
                output = append(output, line)
        }

        if !launchOptionsFound {
                fmt.Printf("[!] Could not find the Project Zomboid (%s) section in %s\n", PZ_APP_ID, path)
                fmt.Println("    Launch PZ through Steam once so Steam creates the section, then re-run the installer.")
        }

        return os.WriteFile(path, []byte(strings.Join(output, newline)), 0644)
}

// ---------------------------------------------------------------------------
// Steam process detection (Linux)
// ---------------------------------------------------------------------------

func isSteamRunning() bool {
        return anySteamProcess()
}

// anySteamProcess scans /proc for running Steam client processes.
// Zombies (exited but unreaped) are ignored - they cannot touch files.
func anySteamProcess() bool {
        entries, err := os.ReadDir("/proc")
        if err != nil {
                return false
        }

        self := os.Getpid()
        for _, e := range entries {
                if !e.IsDir() {
                        continue
                }
                pid, err := parseUint(e.Name())
                if err != nil || int(pid) == self {
                        continue
                }
                if !processAlive(e.Name()) {
                        continue
                }

                comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
                if err != nil {
                        continue
                }
                name := strings.TrimSpace(string(comm))
                switch name {
                case "steam", "steamwebhelper", "steam.old", "steamreset":
                        return true
                }

                if name == "bash" || name == "sh" || name == "dash" {
                        // the steam.sh wrapper runs under a shell; check its command line
                        cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
                        if err != nil {
                                continue
                        }
                        for _, arg := range strings.Split(string(cmdline), "\x00") {
                                if strings.HasSuffix(arg, "/steam.sh") || arg == "steam.sh" {
                                        return true
                                }
                        }
                }
        }
        return false
}

func waitForSteamToClose(operation string) {
        // Safety net: Steam normally gets closed right after the user confirms
        // the changes (ensureSteamClosed in main flow). If Steam is running
        // again at this point (restarted mid-install, or an older code path),
        // close it the same way - automatically first, manually as a fallback.
        _ = operation
        if isSteamRunning() {
                ensureSteamClosed(detectedSteamDir, detectedSteamKind)
        }
}
