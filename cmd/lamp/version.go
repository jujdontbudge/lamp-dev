package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"lamp.eilmer.si/internal/buildversion"
	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

func versionCommand(globals []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lamp version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", localconfig.Socket(), "daemon socket")
	url := flags.String("api-url", os.Getenv("LAMP_API_URL"), "daemon origin")
	tokenFile := flags.String("token-file", "", "private bearer file")
	flags.Bool("json", false, "JSON output")
	if err := flags.Parse(globals); err != nil {
		return 2
	}
	_, socketErr := os.Stat(*socket)
	if socketErr != nil && os.IsNotExist(socketErr) && *url == "" && !explicitConnection(globals) {
		json.NewEncoder(stdout).Encode(map[string]string{"version": buildversion.Current(), "protocol": "lamp.worker/2"})
		return 0
	}
	token, err := localconfig.Token(*tokenFile, *socket, *url)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	client := &surface.Client{Socket: *socket, URL: *url, Token: token}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, status, err := client.Call(ctx, surface.ByName["version_get"], map[string]any{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if status != 200 {
		fmt.Fprintln(stderr, string(raw))
		return 1
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	out["client_version"] = buildversion.Current()
	json.NewEncoder(stdout).Encode(out)
	return 0
}

func explicitConnection(args []string) bool {
	for _, arg := range args {
		if arg != "--json" {
			return true
		}
	}
	return false
}
