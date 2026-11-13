package access

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	ssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	"lamp.eilmer.si/internal/surface"
)

// SFTP paths are sandbox_id:/guest/path. All operations use the caller's
// existing public API credential; the SSH host's filesystem is never exposed.
func (s *Service) sftp(session ssh.Session) {
	user, ok := s.user(session.User())
	if !ok {
		session.Exit(1)
		return
	}
	credential, err := token(user.TokenFile)
	if err != nil {
		session.Exit(1)
		return
	}
	client := &surface.Client{Socket: s.config.APISocket, Token: credential}
	defer client.Close()
	handler := &sandboxSFTP{ctx: session.Context(), client: client, temporary: s.config.StateDir}
	server := sftp.NewRequestServer(session, sftp.Handlers{FileGet: handler, FilePut: handler, FileCmd: handler, FileList: handler})
	defer server.Close()
	if err = server.Serve(); err != nil && err != io.EOF {
		session.Exit(1)
		return
	}
	session.Exit(0)
}

type sandboxSFTP struct {
	ctx       context.Context
	client    *surface.Client
	temporary string
}

func sftpPath(value string) (string, string, error) {
	id, p, ok := strings.Cut(strings.TrimPrefix(value, "/"), ":")
	if !ok || !strings.HasPrefix(id, "sb_") || !safeID(id) || !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", "", fmt.Errorf("path must be sandbox_id:/absolute/guest/path")
	}
	return id, path.Clean(p), nil
}

type stagedSFTP struct {
	*os.File
	finish func() error
	failed bool
}

func (f *stagedSFTP) Close() error {
	defer os.Remove(f.Name())
	defer f.File.Close()
	if f.failed {
		return fmt.Errorf("transfer interrupted")
	}
	if f.finish != nil {
		return f.finish()
	}
	return nil
}
func (f *stagedSFTP) TransferError(err error) { f.failed = true }
func (f *stagedSFTP) WriteAt(bytes []byte, offset int64) (int, error) {
	if offset < 0 || offset+int64(len(bytes)) > 4<<30 {
		return 0, fmt.Errorf("file exceeds 4 GiB limit")
	}
	return f.File.WriteAt(bytes, offset)
}
func (h *sandboxSFTP) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	id, p, err := sftpPath(r.Filepath)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(h.temporary, "sftp-")
	if err != nil {
		return nil, err
	}
	if err = surface.DownloadFile(r.Context(), h.client, id, p, file); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, sftpError(err)
	}
	return &stagedSFTP{File: file}, nil
}
func (h *sandboxSFTP) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	id, p, err := sftpPath(r.Filepath)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(h.temporary, "sftp-")
	if err != nil {
		return nil, err
	}
	flags := r.Pflags()
	if flags.Excl || !flags.Creat {
		_, statErr := surface.Invoke(r.Context(), h.client, "sandbox_file_stat", map[string]any{"sandbox_id": id, "path": p})
		if (statErr == nil && flags.Excl) || (statErr != nil && (!flags.Creat || !strings.Contains(statErr.Error(), "NOT_FOUND"))) {
			file.Close()
			os.Remove(file.Name())
			if statErr == nil {
				return nil, os.ErrExist
			}
			return nil, sftpError(statErr)
		}
	}
	if !flags.Trunc {
		if err = surface.DownloadFile(r.Context(), h.client, id, p, file); err != nil && !(flags.Creat && strings.Contains(err.Error(), "NOT_FOUND")) {
			file.Close()
			os.Remove(file.Name())
			return nil, sftpError(err)
		}
	}
	staged := &stagedSFTP{File: file}
	staged.finish = func() error {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return surface.UploadFile(h.ctx, h.client, id, p, file)
	}
	return staged, nil
}
func (h *sandboxSFTP) Filecmd(r *sftp.Request) error {
	id, p, err := sftpPath(r.Filepath)
	if err != nil {
		return err
	}
	data, err := surface.Invoke(r.Context(), h.client, "sandbox_get", map[string]any{"sandbox_id": id})
	if err != nil {
		return err
	}
	sandbox, _ := data["data"].(map[string]any)
	in := map[string]any{"sandbox_id": id, "path": p, "expected_revision": sandbox["revision"]}
	action := "sandbox_file_delete"
	switch r.Method {
	case "Remove", "Rmdir":
	case "Rename":
		destID, dest, err := sftpPath(r.Target)
		if err != nil {
			return err
		}
		if destID != id {
			return fmt.Errorf("rename must stay in the same sandbox")
		}
		in["destination"] = dest
		action = "sandbox_file_rename"
	case "Setstat":
		flags, attributes := r.AttrFlags(), r.Attributes()
		if flags.UidGid || flags.Size {
			return sftp.ErrSSHFxOpUnsupported
		}
		update := map[string]any{}
		if flags.Permissions {
			update["mode"] = attributes.Mode & 0777
		}
		if flags.Acmodtime {
			update["atime"] = attributes.Atime
			update["mtime"] = attributes.Mtime
		}
		raw, _ := json.Marshal(update)
		started, err := surface.Invoke(r.Context(), h.client, "exec_create", map[string]any{"sandbox_id": id, "argv": []string{"python3", "-c", `import os,sys,json
p=sys.argv[1];a=json.loads(sys.argv[2])
if 'mode' in a: os.chmod(p,a['mode'])
if 'atime' in a: os.utime(p,(a['atime'],a['mtime']))`, p, string(raw)}})
		if err != nil {
			return err
		}
		command, _ := started["exec_id"].(string)
		cursor := ""
		for {
			args := map[string]any{"exec_id": command, "wait_ms": uint64(1000)}
			if cursor != "" {
				args["cursor"] = cursor
			}
			page, err := surface.Invoke(r.Context(), h.client, "exec_watch", args)
			if err != nil {
				return err
			}
			cursor, _ = page["next_cursor"].(string)
			if cursor == "" {
				break
			}
		}
		ended, err := surface.Invoke(r.Context(), h.client, "exec_get", map[string]any{"exec_id": command})
		if err != nil {
			return err
		}
		execution, _ := ended["data"].(map[string]any)
		code, err := strconv.Atoi(fmt.Sprint(execution["exit_code"]))
		if err != nil || execution["state"] != "exited" || code != 0 {
			return fmt.Errorf("guest metadata update failed")
		}
		return nil
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
	_, err = surface.Invoke(r.Context(), h.client, action, in)
	return err
}

type guestFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f guestFileInfo) Name() string       { return f.name }
func (f guestFileInfo) Size() int64        { return f.size }
func (f guestFileInfo) Mode() os.FileMode  { return f.mode }
func (f guestFileInfo) ModTime() time.Time { return time.Time{} }
func (f guestFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f guestFileInfo) Sys() any           { return nil }

type guestFileList []os.FileInfo

func (l guestFileList) ListAt(out []os.FileInfo, offset int64) (int, error) {
	if offset < 0 || offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[offset:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}
func (h *sandboxSFTP) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	id, p, err := sftpPath(r.Filepath)
	if err != nil {
		return nil, err
	}
	action := "sandbox_file_list"
	if r.Method == "Stat" || r.Method == "Lstat" {
		action = "sandbox_file_stat"
	} else if r.Method != "List" {
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	in := map[string]any{"sandbox_id": id, "path": p}
	var items guestFileList
	for {
		result, err := surface.Invoke(r.Context(), h.client, action, in)
		if err != nil {
			return nil, sftpError(err)
		}
		raw, _ := result["items"].([]any)
		for _, item := range raw {
			m, _ := item.(map[string]any)
			size, _ := m["size_bytes"].(json.Number)
			n, _ := size.Int64()
			mode := os.FileMode(0644)
			if m["type"] == "directory" {
				mode = 0755 | os.ModeDir
			}
			items = append(items, guestFileInfo{name: fmt.Sprint(m["name"]), size: n, mode: mode})
		}
		cursor, _ := result["next_cursor"].(string)
		if cursor == "" {
			break
		}
		in["cursor"] = cursor
	}
	return items, nil
}

func sftpError(err error) error {
	if strings.Contains(err.Error(), "NOT_FOUND") {
		return os.ErrNotExist
	}
	if strings.Contains(err.Error(), "SCOPE_DENIED") {
		return os.ErrPermission
	}
	return err
}
