//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
void dnaRunApp(void);
void dnaServerStopped(void);
*/
import "C"

// The minimum macOS version is set for the whole build by packaging/make-app.sh
// rather than here, so everything linked — Go's own runtime included — agrees
// on it, and a plain `go build` on a newer Mac does not warn.

import (
	"os"
	"runtime"
	"strings"
)

// Cocoa's event loop has to own the main thread, and the only goroutine Go
// guarantees is on it is the one running main — so it is pinned there before
// main starts.
func init() { runtime.LockOSThread() }

// dockHooks are set by run once there is a server to quit and a page to open.
var dockHooks struct {
	quit        func()
	coordinator func()
	devices     func()
}

//export dnaQuitRequested
func dnaQuitRequested() {
	if dockHooks.quit != nil {
		dockHooks.quit()
	}
}

//export dnaOpenCoordinator
func dnaOpenCoordinator() {
	if dockHooks.coordinator != nil {
		go dockHooks.coordinator()
	}
}

//export dnaOpenDevices
func dnaOpenDevices() {
	if dockHooks.devices != nil {
		go dockHooks.devices()
	}
}

// inAppBundle reports whether this is the .app rather than a binary run from a
// terminal. Only the .app gets a Dock icon: `go run` from a shell should stay a
// shell program, stopped with Ctrl-C.
func inAppBundle() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// runApp runs the server. From the .app it runs it alongside the Dock: the
// server on its own goroutine, Cocoa on the main thread, and whichever stops
// first takes the other with it.
func runApp(serve func() error, onErr func(error)) {
	if !inAppBundle() {
		if err := serve(); err != nil {
			onErr(err)
			os.Exit(1)
		}
		return
	}
	go func() {
		if err := serve(); err != nil {
			onErr(err)
		}
		C.dnaServerStopped()
	}()
	C.dnaRunApp()
}
