package main

import (
	"event-forwarder/pkg/version"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		info := version.Info()
		fmt.Printf("fwd version %s, commit %s, built %s\n", info.Version, info.Commit, info.Built)
		return
	}

	fmt.Println("Hello from event-forwarder!")
	fmt.Printf("Version: %s\n", version.Info().Version)
}
