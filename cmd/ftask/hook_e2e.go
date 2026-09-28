//go:build e2e_hooks

package main

import "os"

// Test builds only: FTASK_E2E_PANIC forces a panic once the process is set
// up, so e2e can check that a crash exits 134, not 2.
func init() {
	startHook = func() {
		if os.Getenv("FTASK_E2E_PANIC") != "" {
			panic("forced by FTASK_E2E_PANIC")
		}
	}
}
