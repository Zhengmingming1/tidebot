package main

import "sync"

// initialWindowStart returns the app-start callback. Native creation must finish
// before reading screen geometry, and sizing must finish before showing anything.
// Even if the start event repeats, never reset a size the user has since chosen.
func initialWindowStart(hidden bool, create, configure, show func()) func() {
	return sync.OnceFunc(func() {
		create()
		configure()
		if !hidden {
			show()
		}
	})
}
