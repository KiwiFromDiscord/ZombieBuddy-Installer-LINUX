package main

// Steam auto-shutdown control.
//
// The Steam client rewrites userdata/<uid>/config/localconfig.vdf when it
// exits, which silently reverts launch-option patches made while it was
// running. The original Windows installer told the user to close Steam
// manually; on Linux (and especially on the Steam Deck, where Steam's
// keyboard is often needed to type into the installer) we can close Steam
// ourselves right after the user confirms the changes:
//
//   1. Flatpak install  -> flatpak run com.valvesoftware.Steam -shutdown
//   2. Snap install     -> snap run steam -shutdown
//   3. Native/custom    -> steam -shutdown (PATH) or <steamroot>/steam.sh -shutdown
//   4. Last resort      -> SIGTERM to the main "steam" client process
//
// Every attempt is guarded by a process check first (running "steam
// -shutdown" with no Steam running would *start* Steam) and followed by a
// polling wait. If nothing works we fall back to asking the user to close
// Steam manually and press Enter, like the original installer did.

import (
        "context"
        "fmt"
        "io"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "syscall"
        "time"
)

// How long to wait for Steam to exit after a shutdown request before
// moving on to the next method (Steam can take a few seconds to exit).
const (
        steamShutdownWait   = 25 * time.Second
        steamShutdownCmdTO  = 20 * time.Second // max runtime of the shutdown helper itself
        steamTermWait       = 12 * time.Second
        steamPollInterval   = 500 * time.Millisecond
        steamConfigFlushSec = 1500 * time.Millisecond // let Steam finish writing configs
)

// detectedSteamDir / detectedSteamKind remember the Steam installation found
// during detection so late safety-net calls (waitForSteamToClose) can pick
// the right shutdown command.
var (
        detectedSteamDir  string
        detectedSteamKind string
)

// steamCloseProceededAnyway is set when the user (or a drained stdin)
// chose to continue despite Steam still running. Later safety-net calls
// then only warn instead of retrying the whole shutdown dance.
var steamCloseProceededAnyway bool

// ensureSteamClosed closes Steam before changes are applied. It is a no-op
// when Steam is not running.
func ensureSteamClosed(steamDir, steamKind string) {
        if !isSteamRunning() {
                return
        }

        if steamCloseProceededAnyway {
                fmt.Println("[!] Steam is still running; its exit may overwrite the launch")
                fmt.Println("    option changes made now.")
                return
        }

        fmt.Println()
        fmt.Println("[i] Steam is currently running.")
        fmt.Println("    Steam rewrites its config files when it exits, so it must be closed")
        fmt.Println("    before the installer continues. Closing Steam automatically...")

        if shutdownSteam(steamDir, steamKind) {
                fmt.Println("[.] Steam is closed. Continuing in a moment...")
                time.Sleep(steamConfigFlushSec)
                fmt.Println()
                return
        }

        // Automatic shutdown failed - ask the user to do it manually.
        fmt.Println()
        fmt.Println("[!] Could not close Steam automatically.")
        fmt.Println("    Please close Steam completely (Steam menu -> Exit, or right-click the")
        fmt.Println("    Steam tray icon -> Exit), then press Enter to continue.")
        for isSteamRunning() {
                fmt.Print("\nPress Enter after closing Steam... ")
                line, err := stdinReader.ReadBytes('\n')
                if err == io.EOF && len(line) == 0 {
                        // No interactive stdin available (piped input exhausted):
                        // proceed with a warning instead of looping forever.
                        fmt.Println()
                        fmt.Println("[!] Proceeding with Steam still running - Steam launch option")
                        fmt.Println("    changes may be overwritten when Steam exits.")
                        steamCloseProceededAnyway = true
                        return
                }
        }
        fmt.Printf("[.] Steam is now closed. Continuing...\n\n")
        time.Sleep(steamConfigFlushSec)
}

// shutdownSteam tries every graceful shutdown method in order and reports
// whether Steam actually exited.
func shutdownSteam(steamDir, steamKind string) bool {
        for _, argv := range steamShutdownCommands(steamDir, steamKind) {
                if !isSteamRunning() {
                        return true
                }
                fmt.Printf("[.] Requesting Steam shutdown: %s\n", strings.Join(argv, " "))
                if runShutdownCommand(argv) {
                        if waitSteamExited(steamShutdownWait) {
                                return true
                        }
                        fmt.Println("[!] Steam did not close in time.")
                } else {
                        fmt.Println("[!] That shutdown method is not available.")
                }
        }

        // Nothing worked so far: politely terminate the main Steam client
        // process (it saves its configuration on the way out).
        if isSteamRunning() {
                fmt.Println("[.] Sending terminate signal to the Steam client...")
                if terminateSteamMain() {
                        if waitSteamExited(steamTermWait) {
                                return true
                        }
                        fmt.Println("[!] Steam did not react to the terminate signal.")
                }
        }

        return !isSteamRunning()
}

// steamShutdownCommands builds the list of shutdown commands to try, in
// order, for the detected installation type. Only commands whose launcher
// actually exists are included.
func steamShutdownCommands(steamDir, steamKind string) [][]string {
        var cmds [][]string

        switch steamKind {
        case "flatpak":
                if _, err := exec.LookPath("flatpak"); err == nil {
                        cmds = append(cmds, []string{"flatpak", "run", "com.valvesoftware.Steam", "-shutdown"})
                }
        case "snap":
                if _, err := exec.LookPath("snap"); err == nil {
                        cmds = append(cmds, []string{"snap", "run", "steam", "-shutdown"})
                }
        }

        // Native / custom / unknown installs: the "steam" launcher on PATH,
        // then the steam.sh wrapper inside the Steam root itself.
        if steamKind == "" || steamKind == "native" || steamKind == "custom" {
                if path, err := exec.LookPath("steam"); err == nil {
                        cmds = append(cmds, []string{path, "-shutdown"})
                }
                if steamDir != "" {
                        if sh := filepath.Join(steamDir, "steam.sh"); fileExists(sh) {
                                cmds = append(cmds, []string{sh, "-shutdown"})
                        }
                }
        }

        return cmds
}

// runShutdownCommand executes one shutdown helper with a hard timeout and
// reports whether it ran successfully. Output is discarded so the installer
// UI stays clean.
func runShutdownCommand(argv []string) bool {
        ctx, cancel := context.WithTimeout(context.Background(), steamShutdownCmdTO)
        defer cancel()

        cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
        cmd.Stdout = io.Discard
        cmd.Stderr = io.Discard
        cmd.Stdin = nil
        return cmd.Run() == nil
}

// waitSteamExited polls until every Steam process is gone or the timeout
// hits. Progress is shown as dots so the user sees something is happening.
func waitSteamExited(timeout time.Duration) bool {
        fmt.Print("[.] Waiting for Steam to close")
        deadline := time.Now().Add(timeout)
        for isSteamRunning() {
                if time.Now().After(deadline) {
                        fmt.Println(" (timed out)")
                        return false
                }
                fmt.Print(".")
                time.Sleep(steamPollInterval)
        }
        fmt.Println(" done")
        return true
}

// terminateSteamMain sends SIGTERM to the main Steam client processes
// (comm == "steam" / "steam.old" / "steamreset"). Steam handles SIGTERM by
// shutting down cleanly and saving its configuration. steamwebhelper is
// deliberately left alone - Steam cleans it up itself.
func terminateSteamMain() bool {
        entries, err := os.ReadDir("/proc")
        if err != nil {
                return false
        }

        self := os.Getpid()
        signalled := false
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
                if name != "steam" && name != "steam.old" && name != "steamreset" {
                        continue
                }
                if proc, err := os.FindProcess(int(pid)); err == nil {
                        if err := proc.Signal(syscall.SIGTERM); err == nil {
                                signalled = true
                        }
                }
        }
        return signalled
}

// processAlive reports whether the given /proc entry is a live process.
// Zombie processes (already exited, waiting to be reaped by their parent)
// are not alive - they can no longer rewrite any config files.
func processAlive(procDirEntry string) bool {
        data, err := os.ReadFile("/proc/" + procDirEntry + "/stat")
        if err != nil {
                return false
        }
        s := string(data)
        // Layout: pid (comm) state ...  comm may contain spaces/parens, so the
        // state character sits right after the LAST closing parenthesis.
        if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
                return s[i+2] != 'Z'
        }
        return false
}

func fileExists(path string) bool {
        _, err := os.Stat(path)
        return err == nil
}
