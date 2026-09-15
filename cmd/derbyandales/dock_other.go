//go:build !darwin || !cgo

package main

import "os"

// Without Cocoa there is no Dock to join: the server simply runs until it is
// stopped. This is what a build with cgo off, or on another platform, gets.

var dockHooks struct {
	quit        func()
	coordinator func()
	devices     func()
}

func runApp(serve func() error, onErr func(error)) {
	if err := serve(); err != nil {
		onErr(err)
		os.Exit(1)
	}
}
