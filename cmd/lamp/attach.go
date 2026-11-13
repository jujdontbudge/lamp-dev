package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lamp.eilmer.si/internal/localconfig"
	"lamp.eilmer.si/internal/surface"
)

// Managed harnesses accept one prompt per line; output remains the durable
// public session stream, so reconnecting also shows earlier turns.
func attachSession(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lamp session attach", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", localconfig.Socket(), "lampd socket")
	origin := flags.String("api-url", os.Getenv("LAMP_API_URL"), "API origin")
	tokenFile := flags.String("token-file", "", "private token file")
	id := flags.String("session-id", "", "session ID")
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		*id = args[0]
		args = args[1:]
	}
	if flags.Parse(args) != nil || *id == "" {
		return 2
	}
	token, err := localconfig.Token(*tokenFile, *socket, *origin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	client := &surface.Client{Socket: *socket, URL: *origin, Token: token}
	defer client.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	call := func(name string, in map[string]any) (map[string]any, error) {
		return surface.Invoke(ctx, client, name, in)
	}
	get, err := call("session_get", map[string]any{"session_id": *id})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	resource, _ := get["data"].(map[string]any)
	epoch := resource["location_epoch"]
	result, err := call("lease_create", map[string]any{"type": "input", "resource_id": *id, "expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	lease := result["lease_id"]
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		v, e := surface.Invoke(clean, client, "lease_get", map[string]any{"lease_id": lease})
		if e == nil {
			_, _ = surface.Invoke(clean, client, "lease_release", map[string]any{"lease_id": lease, "expected_revision": v["data"].(map[string]any)["revision"]})
		}
	}()
	_, err = call("session_attach", map[string]any{"session_id": *id, "lease_id": lease, "expected_revision": resource["revision"]})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	failure := make(chan error, 1)
	go func() {
		var seq uint64
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			current, e := call("session_get", map[string]any{"session_id": *id})
			if e != nil {
				failure <- e
				return
			}
			r := current["data"].(map[string]any)
			if fmt.Sprint(r["location_epoch"]) != fmt.Sprint(epoch) {
				failure <- fmt.Errorf("session moved; reconnect")
				return
			}
			_, e = call("session_input", map[string]any{"session_id": *id, "lease_id": lease, "location_epoch": epoch, "sequence": seq, "expected_revision": r["revision"], "data_base64": base64.StdEncoding.EncodeToString([]byte(scanner.Text() + "\n"))})
			if e != nil {
				failure <- e
				return
			}
			seq++
		}
		if e := scanner.Err(); e != nil {
			failure <- e
		}
	}()
	cursor := ""
	renew := time.NewTicker(2 * time.Minute)
	defer renew.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case err = <-failure:
			fmt.Fprintln(stderr, err)
			return 1
		case <-renew.C:
			v, e := call("lease_get", map[string]any{"lease_id": lease})
			if e == nil {
				_, e = call("lease_renew", map[string]any{"lease_id": lease, "expected_revision": v["data"].(map[string]any)["revision"], "expires_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)})
			}
			if e != nil {
				fmt.Fprintln(stderr, e)
				return 1
			}
		default:
		}
		in := map[string]any{"session_id": *id, "wait_ms": uint64(1000)}
		if cursor != "" {
			in["cursor"] = cursor
		}
		m, e := call("session_watch", in)
		if e != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(stderr, e)
			return 1
		}
		items, _ := m["items"].([]any)
		for _, item := range items {
			f, _ := item.(map[string]any)
			if f["channel"] != "stdout" && f["channel"] != "stderr" {
				continue
			}
			raw, _ := f["data_base64"].(string)
			data, e := base64.StdEncoding.DecodeString(raw)
			if e != nil {
				return 1
			}
			if _, e = stdout.Write(data); e != nil {
				return 1
			}
		}
		cursor, _ = m["next_cursor"].(string)
		v, e := call("session_get", map[string]any{"session_id": *id})
		if e != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(stderr, e)
			return 1
		}
		state := v["data"].(map[string]any)["state"]
		if (state == "ended" || state == "failed") && cursor == "" {
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(150 * time.Millisecond):
		}
	}
}
