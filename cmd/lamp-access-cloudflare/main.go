package main

import (
	"github.com/JohanSardar/lamp/internal/access"
	"os"
)

func main() {
	if access.RunCloudflarePlugin(os.Stdin, os.Stdout) != nil {
		os.Exit(1)
	}
}
