package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"lamp.eilmer.si/internal/surface"
	"os"
	"path/filepath"
	"strings"
)

func copyFile(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lamp sandbox cp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := surface.DefaultSocket()
	flags.StringVar(&socket, "socket", socket, "lampd socket")
	origin := flags.String("api-url", os.Getenv("LAMP_API_URL"), "API origin")
	tokenFile := flags.String("token-file", "", "private token file")
	// Accept the familiar cp positional arguments followed by connection flags.
	var paths []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		paths = append(paths, args[0])
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	paths = append(paths, flags.Args()...)
	if len(paths) != 2 {
		fmt.Fprintln(stderr, "usage: lamp sandbox cp ./local sb_id:/absolute/path (or the reverse)")
		return 2
	}
	token, err := surface.Credential(*tokenFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	client := &surface.Client{Socket: socket, URL: *origin, Token: token}
	defer client.Close()
	remote := func(p string) (string, string, bool) {
		id, path, ok := strings.Cut(p, ":")
		return id, path, ok && strings.HasPrefix(id, "sb_") && strings.HasPrefix(path, "/")
	}
	sourceID, sourcePath, sourceRemote := remote(paths[0])
	destID, destPath, destRemote := remote(paths[1])
	if sourceRemote == destRemote {
		fmt.Fprintln(stderr, "exactly one path must be sb_id:/absolute/guest/path")
		return 2
	}
	ctx := context.Background()
	err = nil
	if destRemote {
		var file *os.File
		file, err = os.Open(paths[0])
		if err == nil {
			defer file.Close()
			err = surface.UploadFile(ctx, client, destID, destPath, file)
		}
	} else {
		var file *os.File
		file, err = os.CreateTemp(filepath.Dir(paths[1]), ".lamp-download-")
		if err == nil {
			defer os.Remove(file.Name())
			err = surface.DownloadFile(ctx, client, sourceID, sourcePath, file)
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				err = os.Rename(file.Name(), paths[1])
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "copied")
	return 0
}
