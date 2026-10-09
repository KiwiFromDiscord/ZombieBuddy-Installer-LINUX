package main

import (
        "bufio"
        "errors"
        "flag"
        "fmt"
        "io"
        "os"
        "path/filepath"
        "runtime"
        "strings"
)

const (
        PZ_APP_ID         = "108600"
        ZB_MOD_ID         = "3619862853"
        INSTALLER_VERSION = "4.2"
        // Linux/macOS agent invocation (per official ZombieBuddy docs):
        // the JVM loads ZombieBuddy.jar directly as a Java agent.
        // zbNative.dll is a Windows-only JRE workaround and is not used on Linux.
        ZB_LAUNCH_ARG     = "-javaagent:ZombieBuddy.jar"
        ZB_LAUNCH_OPTIONS = ZB_LAUNCH_ARG + " --"

        ZB_CORE_JAR = "ZombieBuddy.jar"
        ZB_CORE_SO  = "libzbNative.so"
        ZB_CORE_DLL = "zbNative.dll"
        ZB_JAR_NEW  = "ZombieBuddy.jar.new"
        ZB_MODS_URL = "https://steamcommunity.com/sharedfiles/filedetails/?id=" + ZB_MOD_ID
)

var (
        steamDirOverride string
        jarOverride      string
)

// stdinReader is shared by all prompts so that piped/scripted input works
// the same way as an interactive terminal (a fresh bufio.Reader per prompt
// would buffer ahead and discard the remaining input lines).
var stdinReader = bufio.NewReader(os.Stdin)

type patchTargets struct {
        normalJSON         bool
        steamLaunchOptions bool
        alternateBatch     bool
}

type promptOption struct {
        value string
        keys  []string
}

type uninstallPlan struct {
        jsonLauncherPath string
        batchFilePath    string
        steamConfigPaths []string
        coreFilePaths    []string
}

type installPaths struct {
        steam     string
        steamKind string // "native", "flatpak", "snap", "custom"
        pz        string
        zb        string
        javaDir   string // directory the JVM uses as working directory (holds ProjectZomboid64.json on B42)
        jsonPath  string
        launchSh  string // projectzomboid.sh / ProjectZomboid64.sh
}

type operationResult int

const (
        resultFailed operationResult = iota
        resultSucceeded
        resultCancelled
)

func main() {
        flag.StringVar(&steamDirOverride, "steam-dir", "", "Steam installation directory (auto-detected by default)")
        flag.StringVar(&jarOverride, "jar", "", "path to a ZombieBuddy.jar to install (auto-detected from Steam Workshop content by default)")
        flag.Usage = func() {
                fmt.Fprintf(os.Stderr, "ZombieBuddy Linux Installer (v%s) - Ported by Kiwi\n\n", INSTALLER_VERSION)
                fmt.Fprintf(os.Stderr, "Usage: zombiebuddy-linux-installer [flags]\n\n")
                fmt.Fprintf(os.Stderr, "Interactive installer for the ZombieBuddy modloader (Project Zomboid).\n")
                fmt.Fprintf(os.Stderr, "Finds Steam (native, Flatpak or Snap), Project Zomboid and the ZombieBuddy\n")
                fmt.Fprintf(os.Stderr, "Steam Workshop content, then installs and patches the game launcher.\n\n")
                flag.PrintDefaults()
        }
        flag.Parse()

        title := fmt.Sprintf("ZombieBuddy Linux Installer (v%s) - Ported by Kiwi", INSTALLER_VERSION)
        fmt.Println(title)
        fmt.Println(strings.Repeat("-", len(title)))

        if runtime.GOOS != "linux" {
                fmt.Printf("[!] Error: This installer is only designed for Linux (running on %s).\n", runtime.GOOS)
                waitForExit()
                return
        }

        if os.Geteuid() == 0 {
                fmt.Println("[!] Warning: running as root. Steam installations are per-user;")
                fmt.Println("    this will only work if root's home actually contains your Steam install.")
        }

        action, err := promptAction()
        if err != nil {
                fmt.Printf("[!] %v\n", err)
                waitForExit()
                return
        }

        result := resultFailed
        switch action {
        case "install":
                result = install()
        case "uninstall":
                result = uninstall()
        case "nothing":
                fmt.Println("\nOkay, nothing changed.")
                waitForExit()
                return
        }

        switch result {
        case resultSucceeded:
                switch action {
                case "install":
                        fmt.Println("\nInstallation complete! You can now start Steam and launch Project Zomboid.")
                        fmt.Println("[i] On first launch ZombieBuddy will ask you to approve every Java mod (security prompt).")
                        fmt.Println("    If the approval dialog does not show up, start the game from a terminal once:")
                        fmt.Println("      cd \"<Project Zomboid folder>\" && ./" + launchScriptHint())
                        fmt.Println("    and answer the prompt there.")
                case "uninstall":
                        fmt.Println("\nUninstall complete! You can now start Steam and launch Project Zomboid.")
                        fmt.Println("[i] ZombieBuddy approval data lives in ~/.zombie_buddy - delete it manually if you want.")
                }
        case resultCancelled:
                fmt.Println("\nNothing changed.")
        default:
                fmt.Printf("\n%s encountered errors. Please review the messages above.\n", displayAction(action))
        }
        waitForExit()
}

// detectedSh remembers the launch script found during detection (for tips).
var detectedSh string

// launchScriptHint names the detected alternate launch script.
func launchScriptHint() string {
        if detectedSh != "" {
                return filepath.Base(detectedSh)
        }
        return "projectzomboid.sh"
}

func waitForExit() {
        fmt.Println("Press Enter to exit...")
        stdinReader.ReadBytes('\n')
}

func promptAction() (string, error) {
        reader := stdinReader
        return promptChoice(reader, promptChoiceConfig{
                title: "What can I do you for?",
                lines: []string{
                        "  1) Install ZombieBuddy",
                        "  2) Uninstall ZombieBuddy",
                        "  3) Nothing, thanks!",
                        "",
                        "Any system changes will be shown for confirmation before they are applied.",
                },
                prompt:  "Choose 1, 2, or 3: ",
                eofErr:  "no action selected",
                invalid: "Please choose 1 for install, 2 for uninstall, or 3 to do nothing.",
                options: []promptOption{
                        {value: "install", keys: []string{"1", "i", "install"}},
                        {value: "uninstall", keys: []string{"2", "u", "uninstall"}},
                        {value: "nothing", keys: []string{"3", "n", "nothing", "no", "exit", "quit"}},
                },
        })
}

func promptInstallTargets() (patchTargets, error) {
        reader := stdinReader
        value, err := promptChoice(reader, promptChoiceConfig{
                title:   "What launch mode should ZombieBuddy patch?",
                lines:   []string{"  1) Both", "  2) Normal Launch", "  3) Alternate Launch"},
                prompt:  "Choose 1, 2, or 3: ",
                eofErr:  "no launch mode selected",
                invalid: "Please choose 1, 2, or 3.",
                options: []promptOption{
                        {value: "both", keys: []string{"1", "b", "both"}},
                        {value: "normal", keys: []string{"2", "n", "normal", "normal launch"}},
                        {value: "alternate", keys: []string{"3", "a", "alternate", "alternate launch"}},
                },
        })
        if err != nil {
                return patchTargets{}, err
        }

        switch value {
        case "normal":
                return promptNormalInstallTargets(reader)
        case "alternate":
                return patchTargets{alternateBatch: true}, nil
        case "both":
                return patchTargets{normalJSON: true, steamLaunchOptions: true, alternateBatch: true}, nil
        default:
                return patchTargets{}, fmt.Errorf("unknown launch mode %q", value)
        }
}

func promptNormalInstallTargets(reader *bufio.Reader) (patchTargets, error) {
        value, err := promptChoice(reader, promptChoiceConfig{
                title:   "How should Normal Launch be patched?",
                lines:   []string{"  1) Both", "  2) ProjectZomboid64.json", "  3) Steam launch options"},
                prompt:  "Choose 1, 2, or 3: ",
                eofErr:  "no normal launch patch selected",
                invalid: "Please choose 1, 2, or 3.",
                options: []promptOption{
                        {value: "both", keys: []string{"1", "b", "both"}},
                        {value: "json", keys: []string{"2", "j", "json"}},
                        {value: "steam", keys: []string{"3", "s", "steam", "steam launch options"}},
                },
        })
        if err != nil {
                return patchTargets{}, err
        }

        switch value {
        case "json":
                return patchTargets{normalJSON: true}, nil
        case "steam":
                return patchTargets{steamLaunchOptions: true}, nil
        case "both":
                return patchTargets{normalJSON: true, steamLaunchOptions: true}, nil
        default:
                return patchTargets{}, fmt.Errorf("unknown normal launch target %q", value)
        }
}

type promptChoiceConfig struct {
        title   string
        lines   []string
        prompt  string
        eofErr  string
        invalid string
        options []promptOption
}

func promptChoice(reader *bufio.Reader, config promptChoiceConfig) (string, error) {
        for {
                fmt.Println()
                fmt.Println(config.title)
                for _, line := range config.lines {
                        fmt.Println(line)
                }
                fmt.Print(config.prompt)

                line, err := reader.ReadString('\n')
                if err != nil && err != io.EOF {
                        return "", err
                }
                choice := strings.ToLower(strings.TrimSpace(line))
                for _, option := range config.options {
                        for _, key := range option.keys {
                                if choice == key {
                                        return option.value, nil
                                }
                        }
                }
                if err == io.EOF {
                        return "", errors.New(config.eofErr)
                }
                fmt.Println("[!] " + config.invalid)
        }
}

func confirmChanges(lines []string) (bool, error) {
        if len(lines) == 0 {
                fmt.Println()
                fmt.Println("No system changes are needed.")
                return false, nil
        }

        fmt.Println()
        fmt.Println("ZombieBuddy will make these changes:")
        for _, line := range lines {
                fmt.Println("  - " + line)
        }
        fmt.Print("Continue? [y/N]: ")

        line, err := stdinReader.ReadString('\n')
        if err != nil && err != io.EOF {
                return false, err
        }
        switch strings.ToLower(strings.TrimSpace(line)) {
        case "y", "yes":
                return true, nil
        default:
                return false, nil
        }
}

func displayAction(action string) string {
        switch action {
        case "install":
                return "Installation"
        case "uninstall":
                return "Uninstall"
        case "nothing":
                return "Nothing"
        default:
                return "Operation"
        }
}

func install() operationResult {
        targets, err := promptInstallTargets()
        if err != nil {
                fmt.Printf("[!] Error selecting launch targets: %v\n", err)
                return resultFailed
        }

        paths, err := detectInstallPaths(true)
        if err != nil {
                fmt.Printf("[!] %v\n", err)
                return resultFailed
        }

        preview := installPreview(paths, targets)
        confirmed, err := confirmChanges(preview)
        if err != nil {
                fmt.Printf("[!] Error reading confirmation: %v\n", err)
                return resultFailed
        }
        if !confirmed {
                fmt.Println("[-] Installation cancelled.")
                return resultCancelled
        }

        // Steam rewrites localconfig.vdf on exit, which would revert the launch
        // option patch - close it now, before anything is modified.
        ensureSteamClosed(paths.steam, paths.steamKind)

        err = copyCoreFiles(paths)
        if err != nil {
                fmt.Printf("[!] Error copying core files: %v\n", err)
                return resultFailed
        } else {
                fmt.Printf("[.] Successfully installed %s into %s\n", ZB_CORE_JAR, paths.javaDir)
        }

        if targets.normalJSON {
                updatedJSON, err := patchJSONLauncher(paths)
                if err != nil {
                        fmt.Printf("[!] Error updating ProjectZomboid64.json: %v\n", err)
                        return resultFailed
                }
                if paths.jsonPath == "" {
                        fmt.Println("[-] ProjectZomboid64.json not found - skipped (game launcher config missing).")
                } else {
                        reportChange(updatedJSON, "Updated "+paths.jsonPath+" for normal launcher mode", "ProjectZomboid64.json already contains ZombieBuddy agent.")
                }
        }

        if targets.alternateBatch {
                updatedBatch, err := patchLaunchScript(paths)
                if err != nil {
                        fmt.Printf("[!] Error updating launch script: %v\n", err)
                        return resultFailed
                }
                if paths.launchSh == "" {
                        fmt.Println("[-] projectzomboid.sh not found - skipped (alternate launch script missing).")
                } else {
                        reportChange(updatedBatch, "Updated "+paths.launchSh+" for alternative launcher mode", filepath_Base(paths.launchSh)+" already contains ZombieBuddy agent.")
                }
        }

        if targets.steamLaunchOptions {
                err = updateLaunchOptions(paths.steam)
                if err != nil {
                        fmt.Printf("[!] Error updating Steam launch options: %v\n", err)
                        return resultFailed
                }
        }

        return resultSucceeded
}

func uninstall() operationResult {
        paths, err := detectInstallPaths(false)
        if err != nil {
                fmt.Printf("[!] %v\n", err)
                return resultFailed
        }

        plan, err := buildUninstallPlan(paths)
        if err != nil {
                fmt.Printf("[!] Error planning uninstall: %v\n", err)
                return resultFailed
        }

        previewLines := uninstallPreview(plan)
        if len(previewLines) == 0 {
                fmt.Println("[-] Nothing to uninstall.")
                return resultCancelled
        }

        confirmed, err := confirmChanges(previewLines)
        if err != nil {
                fmt.Printf("[!] Error reading confirmation: %v\n", err)
                return resultFailed
        }
        if !confirmed {
                fmt.Println("[-] Uninstall cancelled.")
                return resultCancelled
        }

        // Same as install: Steam must not rewrite configs behind our back.
        ensureSteamClosed(paths.steam, paths.steamKind)

        if err := applyUninstallPlan(plan); err != nil {
                fmt.Printf("[!] Error applying uninstall changes: %v\n", err)
                return resultFailed
        }

        return resultSucceeded
}

func reportChange(changed bool, changedMessage string, unchangedMessage string) {
        if changed {
                fmt.Println("[.] " + changedMessage)
        } else if unchangedMessage != "" {
                fmt.Println("[-] " + unchangedMessage)
        }
}

func installPreview(paths installPaths, targets patchTargets) []string {
        lines := []string{
                fmt.Sprintf("copy ZombieBuddy.jar to %s", quotePath(paths.javaDir)),
        }
        if targets.normalJSON {
                lines = append(lines, fmt.Sprintf("add %s to %s", quote(ZB_LAUNCH_ARG), quotePath(jsonDisplayName(paths))))
        }
        if targets.alternateBatch {
                lines = append(lines, fmt.Sprintf("add %s to %s", quote(ZB_LAUNCH_ARG), quotePath(paths.launchSh)))
        }
        if targets.steamLaunchOptions {
                lines = append(lines, fmt.Sprintf("add %s to PZ Steam launch options", quote(ZB_LAUNCH_OPTIONS)))
        }
        return lines
}

func uninstallPreview(plan uninstallPlan) []string {
        var lines []string
        if plan.jsonLauncherPath != "" {
                lines = append(lines, fmt.Sprintf("remove %s from %s", quote(ZB_LAUNCH_ARG), quotePath(plan.jsonLauncherPath)))
        }
        if plan.batchFilePath != "" {
                lines = append(lines, fmt.Sprintf("remove %s from %s", quote(ZB_LAUNCH_ARG), quotePath(plan.batchFilePath)))
        }
        if plan.steamConfigPaths != nil {
                lines = append(lines, fmt.Sprintf("remove %s from PZ Steam launch options", quote(ZB_LAUNCH_ARG)))
        }
        for _, path := range plan.coreFilePaths {
                lines = append(lines, fmt.Sprintf("delete %s", quotePath(path)))
        }
        return lines
}

func quote(s string) string { return "\"" + s + "\"" }
func quotePath(p string) string {
        if p == "" {
                return "\"\""
        }
        return "\"" + p + "\""
}

func jsonDisplayName(paths installPaths) string {
        if paths.jsonPath != "" {
                return paths.jsonPath
        }
        return "ProjectZomboid64.json"
}

func filepath_Base(p string) string {
        if i := strings.LastIndexByte(p, '/'); i >= 0 {
                return p[i+1:]
        }
        return p
}

func buildUninstallPlan(paths installPaths) (uninstallPlan, error) {
        plan := uninstallPlan{
                coreFilePaths: coreFileDeletionPlan(paths),
        }

        if paths.jsonPath != "" {
                hasJSON, err := jsonLauncherHasZombieBuddy(paths.jsonPath)
                if err != nil {
                        return uninstallPlan{}, fmt.Errorf("checking ProjectZomboid64.json: %v", err)
                }
                if hasJSON {
                        plan.jsonLauncherPath = paths.jsonPath
                }
        }

        if paths.launchSh != "" {
                hasBatch, err := batchFileHasZombieBuddy(paths.launchSh)
                if err != nil {
                        return uninstallPlan{}, fmt.Errorf("checking launch script: %v", err)
                }
                if hasBatch {
                        plan.batchFilePath = paths.launchSh
                }
        }

        steamConfigPaths, err := steamLaunchOptionRemovalPlan(paths.steam)
        if err != nil {
                return uninstallPlan{}, err
        }
        plan.steamConfigPaths = steamConfigPaths

        return plan, nil
}

func applyUninstallPlan(plan uninstallPlan) error {
        if plan.jsonLauncherPath != "" {
                updated, err := updateJSONLauncherVMArgs(plan.jsonLauncherPath, false)
                if err != nil {
                        return fmt.Errorf("updating ProjectZomboid64.json: %v", err)
                }
                reportChange(updated, fmt.Sprintf("Removed %s from ProjectZomboid64.json", quote(ZB_LAUNCH_ARG)), "")
        }

        if plan.batchFilePath != "" {
                updated, err := updateBatchJavaOptions(plan.batchFilePath, false)
                if err != nil {
                        return fmt.Errorf("updating launch script: %v", err)
                }
                reportChange(updated, fmt.Sprintf("Removed %s from %s", quote(ZB_LAUNCH_ARG), filepath_Base(plan.batchFilePath)), "")
        }

        if err := removeCoreFiles(plan.coreFilePaths); err != nil {
                return fmt.Errorf("removing core files: %v", err)
        }

        if err := removeLaunchOptions(plan.steamConfigPaths); err != nil {
                return fmt.Errorf("removing Steam launch options: %v", err)
        }

        return nil
}

func removeCoreFiles(paths []string) error {
        for _, path := range paths {
                err := os.Remove(path)
                if err == nil {
                        fmt.Printf("[.] Removed %s\n", path)
                        continue
                }
                if os.IsNotExist(err) {
                        continue
                }
                return fmt.Errorf("failed to remove %s: %v", path, err)
        }
        return nil
}

func coreFileDeletionPlan(paths installPaths) []string {
        var out []string
        seen := map[string]bool{}
        candidates := []string{paths.javaDir, paths.pz}
        for _, dir := range candidates {
                if dir == "" {
                        continue
                }
                for _, filename := range coreFilesForRemoval() {
                        path := dir + "/" + filename
                        if seen[path] {
                                continue
                        }
                        seen[path] = true
                        if _, err := os.Stat(path); err == nil {
                                out = append(out, path)
                        }
                }
        }
        return out
}

func coreFilesForRemoval() []string {
        return []string{ZB_CORE_JAR, ZB_JAR_NEW, ZB_CORE_SO}
}
