package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lamp.eilmer.si/internal/release"
)

const releaseSource = "https://lamp.eilmer.si"

func releaseHTTP(source string) (*http.Client, error) {
	u, err := url.Parse(source)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("release source must be an HTTPS URL without credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, fmt.Errorf("release source requires HTTPS")
	}
	return &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many release redirects")
		}
		if r.URL.Scheme != "https" && r.URL.Host != u.Host {
			return fmt.Errorf("insecure release redirect")
		}
		if u.Scheme == "https" && r.URL.Scheme != "https" {
			return fmt.Errorf("insecure release redirect")
		}
		return nil
	}}, nil
}

func fetchRelease(p, source string) (string, func(), error) {
	cleanup := func() {}
	st, err := loadState(p)
	if err != nil {
		return "", cleanup, err
	}
	client, err := releaseHTTP(source)
	if err != nil {
		return "", cleanup, err
	}
	defer client.CloseIdleConnections()
	source = strings.TrimRight(source, "/")
	response, err := client.Get(source + "/releases/latest.json")
	if err != nil {
		return "", cleanup, fmt.Errorf("check latest release: %w", err)
	}
	var latest struct {
		Version string `json:"version"`
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return "", cleanup, fmt.Errorf("latest release: HTTP %d", response.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&latest)
	response.Body.Close()
	if err != nil {
		return "", cleanup, err
	}
	if _, err := release.ParseVersion(latest.Version); err != nil {
		return "", cleanup, err
	}
	if !release.Newer(latest.Version, st.Current) {
		say("already up to date (%s)", st.Current)
		return "", cleanup, nil
	}
	// The selector is only a hint. The installed, pinned key must verify the
	// downloaded index, manifest and every payload before applyUpdate runs.
	work, err := os.MkdirTemp("", "lamp-update-")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { os.RemoveAll(work) }
	response, err = client.Get(fmt.Sprintf("%s/releases/v%s/lamp-%s-%s.tar.gz", source, latest.Version, latest.Version, release.Platform()))
	if err != nil {
		return "", cleanup, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", cleanup, fmt.Errorf("download release: HTTP %d", response.StatusCode)
	}
	gz, err := gzip.NewReader(response.Body)
	if err != nil {
		return "", cleanup, err
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, 16<<30))
	root := "lamp-" + latest.Version
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", cleanup, err
		}
		name := filepath.Clean(h.Name)
		if name != root && !strings.HasPrefix(name, root+"/") {
			return "", cleanup, fmt.Errorf("unsafe release archive path")
		}
		dst := filepath.Join(work, name)
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dst, 0700)
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
				return "", cleanup, err
			}
			var f *os.File
			f, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode)&0777)
			if err == nil {
				_, err = io.Copy(f, tr)
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
			}
		default:
			return "", cleanup, fmt.Errorf("release archive contains a non-regular file")
		}
		if err != nil {
			return "", cleanup, err
		}
	}
	key, err := readTrust(filepath.Join(p, "config", "release-trust.pub"))
	if err != nil {
		return "", cleanup, err
	}
	dir := filepath.Join(work, root)
	ix, _, _, err := release.VerifyPlatform(dir, key)
	if err != nil {
		return "", cleanup, err
	}
	if ix.Version != latest.Version {
		return "", cleanup, fmt.Errorf("signed release differs from latest selector")
	}
	return dir, cleanup, nil
}
