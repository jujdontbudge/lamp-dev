// lamp is a buildable CLI boundary. Product commands arrive in T3.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "version" && os.Args[2] == "--json" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"version": "0.1.0-dev", "protocol": "lamp.worker/1"})
		return
	}
	fmt.Fprintln(os.Stderr, "Lamp CLI skeleton: only version --json is available")
	os.Exit(2)
}
