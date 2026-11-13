package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

func containsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}
func setSecret(args, globals []string, out, errs io.Writer) int {
	f := flag.NewFlagSet("lamp secret set NAME", flag.ContinueOnError)
	f.SetOutput(errs)
	socket := f.String("socket", localconfig.Socket(), "private daemon socket")
	origin := f.String("api-url", os.Getenv("LAMP_API_URL"), "API origin")
	tokenFile := f.String("token-file", "", "owner token file")
	stdin := f.Bool("stdin", false, "read secret from private stdin")
	jsonOut := f.Bool("json", false, "JSON output")
	hosts := []string{}
	f.Func("host", "exact allowed HTTPS host (repeatable)", func(v string) error { hosts = append(hosts, v); return nil })
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if f.Parse(append(args, globals...)) != nil || name == "" || f.NArg() != 0 {
		fmt.Fprintln(errs, "usage: lamp secret set NAME [--stdin] [--socket PATH]")
		return 2
	}
	var value []byte
	var err error
	if *stdin {
		value, err = io.ReadAll(io.LimitReader(os.Stdin, 16385))
		value = []byte(strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r"))
	} else {
		fmt.Fprint(errs, "Secret (hidden): ")
		value, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(errs)
	}
	if err != nil {
		fmt.Fprintln(errs, "cannot read secret; use --stdin for a private pipe")
		return 1
	}
	token, err := localconfig.Token(*tokenFile, *socket, *origin)
	if err != nil {
		fmt.Fprintln(errs, "cannot read owner credential")
		return 1
	}
	client := &surface.Client{Socket: *socket, URL: *origin, Token: token}
	defer client.Close()
	data, status, err := client.Call(context.Background(), surface.ByName["secret_set"], map[string]any{"name": name, "value": string(value), "idempotency_key": "secret-entry", "hosts": hosts})
	for i := range value {
		value[i] = 0
	}
	if err != nil || status >= 300 {
		fmt.Fprintln(errs, "secret entry failed; check the owner credential and secret name")
		return 1
	}
	if *jsonOut {
		var v any
		if json.Unmarshal(data, &v) == nil {
			json.NewEncoder(out).Encode(v)
		}
	} else {
		fmt.Fprintln(out, "Stored host-held secret "+name)
	}
	return 0
}
