package control

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/JohanSardar/lamp/pkg/workerpb"
)

// Lamp's own guest work (idle observation, file transfer, materializing
// caches and volumes, git change sets) runs over the private control channel
// as worker Start/Inspect commands. It never appears as task exec, so it is
// exempt infrastructure rather than task activity.

const guestReadChunk = 45 << 10  // base64 of this fits the 64 KiB output tail
const guestWriteChunk = 64 << 10 // one argv element stays below 128 KiB

type guestResult struct {
	Exit   int32
	Stdout []byte
	Stderr []byte
}

func (s *Service) workerRequest(ctx context.Context, step, sandbox string, generation uint64) *pb.Request {
	deadline, _ := ctx.Deadline()
	return &pb.Request{Context: &pb.Context{RequestId: newID("req"), OperationId: newID("op") + "." + step, DeadlineMs: uint64(deadline.UnixMilli()), SandboxId: sandbox, Generation: generation, GrantId: s.worker.config.GrantID, PolicyRevision: 1}}
}

func (s *Service) sandboxGeneration(id string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return value(s.journal.Sandboxes[id].Generation)
}

// guestRun starts argv in the guest and waits for its observed completion.
func (s *Service) guestRun(ctx context.Context, sandbox string, argv ...string) (guestResult, error) {
	generation := s.sandboxGeneration(sandbox)
	r := s.workerRequest(ctx, "lamp", sandbox, generation)
	commandID := r.Context.OperationId
	r.Command = &pb.Request_Start{Start: &pb.Start{Argv: argv, Cwd: "/"}}
	if err := s.worker.perform(ctx, r, "command_started"); err != nil {
		return guestResult{}, err
	}
	for {
		q := s.workerRequest(ctx, "observe", sandbox, generation)
		q.Command = &pb.Request_Inspect{Inspect: &pb.Inspect{CommandOperationId: commandID}}
		receipt, err := s.worker.call(ctx, q)
		if err != nil {
			return guestResult{}, err
		}
		if c := receipt.Command; c != nil && c.FinishedAtUnixNs != 0 {
			if c.Truncated {
				return guestResult{}, reject("DATA_UNAVAILABLE", "guest output exceeded the control channel tail")
			}
			return guestResult{Exit: c.ExitCode, Stdout: c.StdoutTail, Stderr: c.StderrTail}, nil
		}
		if state := receipt.Detail["command_state"]; state != "running" {
			return guestResult{}, &workerFailure{code: "OUTCOME_UNKNOWN", message: "guest command completion unavailable", unknown: true}
		}
		timer := time.NewTimer(2 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return guestResult{}, &workerFailure{code: "OUTCOME_UNKNOWN", message: "deadline before guest command completed", unknown: true}
		case <-timer.C:
		}
	}
}

// guestPython runs a short Python program in the guest; nonzero exit fails.
func (s *Service) guestPython(ctx context.Context, sandbox, program string, args ...string) ([]byte, error) {
	res, err := s.guestRun(ctx, sandbox, append([]string{"python3", "-c", program}, args...)...)
	if err != nil {
		return nil, err
	}
	if res.Exit != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return res.Stdout, fmt.Errorf("guest command failed (exit %d): %s", res.Exit, msg)
	}
	return res.Stdout, nil
}

const pyStat = `import os,sys,json
p=sys.argv[1]
try: st=os.stat(p)
except FileNotFoundError: print(json.dumps({"exists":False}));raise SystemExit
print(json.dumps({"exists":True,"dir":os.path.isdir(p),"size":st.st_size}))`

const pyRead = `import sys,base64
f=open(sys.argv[1],"rb");f.seek(int(sys.argv[2]));sys.stdout.write(base64.b64encode(f.read(int(sys.argv[3]))).decode())`

const pyAppend = `import sys,base64,os
mode="ab" if sys.argv[3]=="1" else "wb"
os.makedirs(os.path.dirname(sys.argv[1]) or "/",exist_ok=True)
with open(sys.argv[1],mode) as f: f.write(base64.b64decode(sys.argv[2]))`

// pyTar archives paths (relative to /) into a private temporary file and
// prints its name. Symlinks are archived as links and never followed.
const pyTar = `import tarfile,sys,os,tempfile
fd,name=tempfile.mkstemp(prefix="lamp-xfer-",suffix=".tar");os.close(fd)
with tarfile.open(name,"w",format=tarfile.PAX_FORMAT) as t:
  for p in sys.argv[1:]:
    if os.path.lexists(p): t.add(p,arcname=p.lstrip("/"),recursive=True)
print(name)`

// pyExtract unpacks an archive with the data filter (no absolute paths,
// traversal, devices or links that escape the destination).
const pyExtract = `import tarfile,sys,os,shutil
src,dest,replace=sys.argv[1],sys.argv[2],sys.argv[3]=="1"
if replace and os.path.isdir(dest): shutil.rmtree(dest)
os.makedirs(dest,exist_ok=True)
with tarfile.open(src) as t: t.extractall(dest,filter="data")
os.remove(src)`

func (s *Service) guestStat(ctx context.Context, sandbox, p string) (exists, dir bool, size int64, err error) {
	out, err := s.guestPython(ctx, sandbox, pyStat, p)
	if err != nil {
		return false, false, 0, err
	}
	var st struct {
		Exists bool  `json:"exists"`
		Dir    bool  `json:"dir"`
		Size   int64 `json:"size"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return false, false, 0, errors.New("unreadable guest stat")
	}
	return st.Exists, st.Dir, st.Size, nil
}

// guestReadFile copies a guest file's bytes to the host in bounded chunks.
func (s *Service) guestReadFile(ctx context.Context, sandbox, p string, size int64, limit int64) ([]byte, error) {
	if size > limit {
		return nil, reject("NO_CAPACITY", fmt.Sprintf("guest file is %d bytes; limit is %d", size, limit))
	}
	var buf bytes.Buffer
	for off := int64(0); off < size; off += guestReadChunk {
		out, err := s.guestPython(ctx, sandbox, pyRead, p, strconv.FormatInt(off, 10), strconv.Itoa(guestReadChunk))
		if err != nil {
			return nil, err
		}
		chunk, err := base64.StdEncoding.DecodeString(string(out))
		if err != nil {
			return nil, errors.New("invalid guest chunk")
		}
		buf.Write(chunk)
	}
	if int64(buf.Len()) != size {
		return nil, reject("DATA_UNAVAILABLE", "guest file changed while it was read")
	}
	return buf.Bytes(), nil
}

// guestWriteFile writes bytes to a guest path in bounded chunks.
func (s *Service) guestWriteFile(ctx context.Context, sandbox, p string, data []byte) error {
	appendFlag := "0"
	for off := 0; off < len(data) || off == 0; off += guestWriteChunk {
		end := min(off+guestWriteChunk, len(data))
		if _, err := s.guestPython(ctx, sandbox, pyAppend, p, base64.StdEncoding.EncodeToString(data[off:end]), appendFlag); err != nil {
			return err
		}
		appendFlag = "1"
		if end == len(data) {
			break
		}
	}
	return nil
}

// guestArchive returns a tar of guest paths.
func (s *Service) guestArchive(ctx context.Context, sandbox string, limit int64, paths ...string) ([]byte, error) {
	out, err := s.guestPython(ctx, sandbox, pyTar, paths...)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(string(out))
	defer s.guestRun(context.WithoutCancel(ctx), sandbox, "rm", "-f", name)
	_, _, size, err := s.guestStat(ctx, sandbox, name)
	if err != nil {
		return nil, err
	}
	return s.guestReadFile(ctx, sandbox, name, size, limit)
}

// guestExtract writes a tar into the guest and unpacks it at dest.
func (s *Service) guestExtract(ctx context.Context, sandbox string, archive []byte, dest string, replace bool) error {
	name := "/tmp/lamp-xfer-" + newID("in") + ".tar"
	if err := s.guestWriteFile(ctx, sandbox, name, archive); err != nil {
		return err
	}
	flag := "0"
	if replace {
		flag = "1"
	}
	_, err := s.guestPython(ctx, sandbox, pyExtract, name, dest, flag)
	return err
}

// --- trees -----------------------------------------------------------------

// archiveTree stores every regular file of an archive as an object and
// returns the tree, rooted at prefix (which is removed from names).
func (s *Service) archiveTree(archive []byte, prefix string) (tree, int64, error) {
	t := tree{}
	var total int64
	prefix = strings.Trim(prefix, "/")
	r := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, errors.New("invalid guest archive")
		}
		name := strings.Trim(path.Clean("/"+h.Name), "/")
		if prefix != "" {
			if name != prefix && !strings.HasPrefix(name, prefix+"/") {
				continue
			}
			name = strings.TrimPrefix(strings.TrimPrefix(name, prefix), "/")
		}
		if name == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			t[name] = treeEntry{Dir: true, Mode: h.Mode & 0o7777}
		case tar.TypeSymlink:
			t[name] = treeEntry{Link: h.Linkname, Mode: 0o777}
		case tar.TypeReg:
			data, err := io.ReadAll(r)
			if err != nil {
				return nil, 0, err
			}
			sum, err := s.putObject(data)
			if err != nil {
				return nil, 0, err
			}
			t[name] = treeEntry{SHA256: sum, Size: int64(len(data)), Mode: h.Mode & 0o7777}
			total += int64(len(data))
		}
	}
	return t, total, nil
}

// treeArchive rebuilds a tar from stored objects, verifying every byte.
func (s *Service) treeArchive(t tree) ([]byte, error) {
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, n := range names {
		e := t[n]
		switch {
		case e.Dir:
			w.WriteHeader(&tar.Header{Name: n + "/", Typeflag: tar.TypeDir, Mode: e.Mode})
		case e.Link != "":
			w.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeSymlink, Linkname: e.Link, Mode: 0o777})
		default:
			data, err := s.readObject(e.SHA256)
			if err != nil {
				return nil, err
			}
			w.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: e.Mode, Size: int64(len(data))})
			w.Write(data)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// treeDigest is the canonical content digest of a tree.
func treeDigest(t tree) string {
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		e := t[n]
		fmt.Fprintf(h, "%q %s %d %o %q %t\n", n, e.SHA256, e.Size, e.Mode, e.Link, e.Dir)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func treeBytes(t tree) (n int64) {
	for _, e := range t {
		n += e.Size
	}
	return n
}

// collectTree reads one guest directory into a stored tree.
func (s *Service) collectTree(ctx context.Context, sandbox, dir string, limit int64) (tree, error) {
	archive, err := s.guestArchive(ctx, sandbox, limit+limit/4+(1<<20), dir)
	if err != nil {
		return nil, err
	}
	t, total, err := s.archiveTree(archive, dir)
	if err != nil {
		return nil, err
	}
	if total > limit {
		return nil, reject("NO_CAPACITY", fmt.Sprintf("%s holds %d bytes; quota is %d", dir, total, limit))
	}
	return t, nil
}

// materializeTree replaces a guest directory with a stored tree.
func (s *Service) materializeTree(ctx context.Context, sandbox string, t tree, dest string) error {
	archive, err := s.treeArchive(t)
	if err != nil {
		return err
	}
	return s.guestExtract(ctx, sandbox, archive, dest, true)
}
