package main

import (
	"lamp.eilmer.si/internal/access"
	"os"
)

func main() {
	if access.RunCloudflarePlugin(os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
}
