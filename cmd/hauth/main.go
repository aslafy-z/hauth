// Command hauth brokers browser-free authentication for AI agent harness
// CLIs and syncs the resulting credentials through a per-user store.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
