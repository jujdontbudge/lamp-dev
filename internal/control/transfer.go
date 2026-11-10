package control

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/JohanSardar/lamp/pkg/api"
	"golang.org/x/sys/unix"
)

var exportBlobID = regexp.MustCompile(`^bl_[0-9a-f]{64}$`)

func (s *Service) cleanExportAllowed(r *http.Request, export cleanExport) bool {
	a := requestCaller(r)
	if a.Principal == "" {
		return true // The outer router already checks an enrolled reader's projection.
	}
	return s.current(a) && s.own(a, export.OwnerID) && s.resourceAllowed(a,
		api.ResourceRef{Kind: "template", Id: export.TemplateID},
		api.ResourceRef{Kind: "blob", Id: export.BlobID})
}

func (s *Service) serveExport(w http.ResponseWriter, r *http.Request, requestID string) {
	s.readLock()
	if s.broken {
		s.mu.Unlock()
		writeError(w, requestID, "DATA_UNAVAILABLE", "journal unavailable", nil)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/templates/") {
		id := strings.TrimPrefix(r.URL.Path, "/v1/templates/")
		value, ok := s.journal.Templates[id]
		if !ok || !s.cleanExportAllowed(r, value) {
			s.mu.Unlock()
			writeError(w, requestID, "NOT_FOUND", "clean template not found", nil)
			return
		}
		template := api.Template{Id: id, Kind: "template", FleetId: "fl_local", OwnerId: value.OwnerID, Revision: 1, CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt, State: "ready", Labels: api.StringMap{}, Visibility: "private", Name: pointer(id), RootObjects: &[]string{value.BlobID}}
		send := prepareJSON(w, 200, api.TemplateGetResult{Data: template, RequestId: requestID})
		s.mu.Unlock()
		send()
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/blobs/")
	content := strings.HasSuffix(id, "/content")
	if content {
		id = strings.TrimSuffix(id, "/content")
	}
	var object cleanExport
	for _, candidate := range s.journal.Templates {
		if candidate.BlobID == id && s.cleanExportAllowed(r, candidate) {
			object = candidate
			break
		}
	}
	s.mu.Unlock()
	if !exportBlobID.MatchString(id) || object.BlobID == "" {
		writeError(w, requestID, "NOT_FOUND", "clean export not found", nil)
		return
	}
	if s.exportsDir == "" {
		writeError(w, requestID, "DATA_UNAVAILABLE", "clean export transport is not configured", nil)
		return
	}
	file, err := os.OpenFile(filepath.Join(s.exportsDir, id+".tar"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		writeError(w, requestID, "DATA_UNAVAILABLE", "clean export unavailable", nil)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, requestID, "DATA_UNAVAILABLE", "invalid export object", nil)
		return
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil || hex.EncodeToString(digest.Sum(nil)) != strings.TrimPrefix(id, "bl_") {
		writeError(w, requestID, "DATA_UNAVAILABLE", "clean export digest mismatch", nil)
		return
	}
	if !content {
		blob := api.Blob{Id: id, Kind: "blob", FleetId: "fl_local", OwnerId: object.OwnerID, Revision: 1, CreatedAt: object.CreatedAt, UpdatedAt: object.CreatedAt, State: "ready", Labels: api.StringMap{}, Visibility: "private", Sha256: pointer(strings.TrimPrefix(id, "bl_")), SizeBytes: pointer(uint64(info.Size())), MediaType: pointer("application/x-tar")}
		prepareJSON(w, 200, api.BlobGetResult{Data: blob, RequestId: requestID})()
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeError(w, requestID, "DATA_UNAVAILABLE", "clean export unavailable", nil)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}
