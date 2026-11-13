package control

// The shared-file backend speaks 9P2000.L only on a one-use private Unix
// socket relayed to the guest over its hypervisor link. No host filesystem
// path or network listener is exposed to a sandbox. os.Root confines every
// pathname operation, including symlinks, to this resource's directory.
import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	pb "lamp.eilmer.si/pkg/workerpb"
)

type p9fid struct {
	path   string
	file   *os.File
	append bool
}
type p9group struct {
	mu      sync.Mutex
	servers map[*p9server]bool
}
type p9server struct {
	group    *p9group
	root     *os.Root
	quota    uint64
	readonly bool
	mu       *sync.Mutex
	fids     map[uint32]*p9fid
}
type p9buf struct {
	*bytes.Reader
	err error
}

func (b *p9buf) u8() uint8 {
	v, e := b.ReadByte()
	if e != nil {
		b.err = e
	}
	return v
}
func (b *p9buf) u16() uint16 { var v uint16; b.read(&v); return v }
func (b *p9buf) u32() uint32 { var v uint32; b.read(&v); return v }
func (b *p9buf) u64() uint64 { var v uint64; b.read(&v); return v }
func (b *p9buf) read(v any) {
	if e := binary.Read(b.Reader, binary.LittleEndian, v); e != nil {
		b.err = e
	}
}
func (b *p9buf) str() string {
	n := b.u16()
	v := make([]byte, n)
	if _, e := io.ReadFull(b, v); e != nil {
		b.err = e
	}
	return string(v)
}
func p9put(w io.Writer, v ...any) {
	for _, x := range v {
		if s, ok := x.(string); ok {
			binary.Write(w, binary.LittleEndian, uint16(len(s)))
			io.WriteString(w, s)
		} else {
			binary.Write(w, binary.LittleEndian, x)
		}
	}
}
func p9name(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, "/\x00")
}
func p9qid(w io.Writer, info os.FileInfo) {
	var kind uint8
	if info.IsDir() {
		kind = 0x80
	}
	if info.Mode()&os.ModeSymlink != 0 {
		kind = 2
	}
	ino := info.Sys().(*syscall.Stat_t).Ino
	p9put(w, kind, uint32(info.ModTime().UnixNano()), uint64(ino))
}
func p9mode(info os.FileInfo) uint32 {
	m := uint32(info.Mode().Perm())
	switch {
	case info.IsDir():
		m |= syscall.S_IFDIR
	case info.Mode()&os.ModeSymlink != 0:
		m |= syscall.S_IFLNK
	default:
		m |= syscall.S_IFREG
	}
	return m
}
func (s *p9server) used() (uint64, error) {
	var n uint64
	err := fs.WalkDir(s.root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		n += 512
		if i.Mode().IsRegular() {
			n += uint64(i.Size())
		}
		return nil
	})
	return n, err
}
func (s *p9server) room(growth uint64) error {
	n, e := s.used()
	if e != nil {
		return e
	}
	if growth > s.quota || n > s.quota-growth {
		return syscall.ENOSPC
	}
	return nil
}
func (s *p9server) fid(id uint32) (*p9fid, error) {
	f := s.fids[id]
	if f == nil {
		return nil, syscall.EBADF
	}
	return f, nil
}
func (s *p9server) handle(kind uint8, b *p9buf) ([]byte, error) {
	var out bytes.Buffer
	if kind == 100 {
		size := b.u32()
		version := b.str()
		if version != "9P2000.L" {
			return nil, syscall.EPROTONOSUPPORT
		}
		p9put(&out, min(size, uint32(256<<10)), "9P2000.L")
		return out.Bytes(), b.err
	}
	if kind == 108 {
		b.u16()
		return nil, b.err
	}
	if kind == 104 {
		id := b.u32()
		b.u32()
		b.str()
		b.str()
		b.u32()
		if s.fids[id] != nil {
			return nil, syscall.EEXIST
		}
		i, e := s.root.Stat(".")
		if e != nil {
			return nil, e
		}
		s.fids[id] = &p9fid{path: "."}
		p9qid(&out, i)
		return out.Bytes(), b.err
	}
	if kind == 74 { // renameat: both paths are confined to the same resource
		from, e := s.fid(b.u32())
		if e != nil {
			return nil, e
		}
		name := b.str()
		to, e := s.fid(b.u32())
		if e != nil {
			return nil, e
		}
		next := b.str()
		if !p9name(name) || !p9name(next) || s.readonly {
			return nil, syscall.EPERM
		}
		oldPath, newPath := path.Join(from.path, name), path.Join(to.path, next)
		if e := s.root.Rename(oldPath, newPath); e != nil {
			return nil, e
		}
		update := func(server *p9server) {
			for _, fid := range server.fids {
				if fid.path == oldPath || strings.HasPrefix(fid.path, oldPath+"/") {
					fid.path = newPath + strings.TrimPrefix(fid.path, oldPath)
				}
			}
		}
		if s.group == nil {
			update(s)
		} else {
			for server := range s.group.servers {
				update(server)
			}
		}
		return nil, b.err
	}
	id := b.u32()
	f, e := s.fid(id)
	if e != nil {
		return nil, e
	}
	mutating := kind == 14 || kind == 16 || kind == 20 || kind == 26 || kind == 70 || kind == 72 || kind == 76 || kind == 118
	if mutating && s.readonly {
		return nil, syscall.EROFS
	}
	switch kind {
	case 8: // statfs reports this resource's enforced quota
		used, e := s.used()
		if e != nil {
			return nil, e
		}
		free := uint64(0)
		if s.quota > used {
			free = s.quota - used
		}
		p9put(&out, uint32(0x01021997), uint32(4096), s.quota/4096, free/4096, free/4096, s.quota/512, free/512, uint64(1), uint32(255))
	case 110: // walk; '..' at the exported root stays at that root
		next := b.u32()
		count := b.u16()
		if count > 256 {
			return nil, syscall.EINVAL
		}
		if next != id && s.fids[next] != nil {
			return nil, syscall.EEXIST
		}
		p := f.path
		var q bytes.Buffer
		var walked uint16
		for range count {
			n := b.str()
			if n == ".." {
				p = path.Dir(p)
			} else if p9name(n) {
				p = path.Join(p, n)
			} else {
				return nil, syscall.EINVAL
			}
			i, e := s.root.Lstat(p)
			if e != nil {
				if walked == 0 {
					return nil, e
				}
				break
			}
			p9qid(&q, i)
			walked++
		}
		p9put(&out, walked)
		out.Write(q.Bytes())
		if walked == count {
			s.fids[next] = &p9fid{path: p}
		}
	case 12: // lopen: strip Linux-only flags, never allow special device opens
		flags := b.u32()
		i, e := s.root.Lstat(f.path)
		if e != nil {
			return nil, e
		}
		if !i.IsDir() && !i.Mode().IsRegular() {
			return nil, syscall.EPERM
		}
		mode := int(flags & 3)
		if flags&512 != 0 {
			mode |= os.O_TRUNC
		}
		f.append = flags&1024 != 0
		if s.readonly && mode != os.O_RDONLY {
			return nil, syscall.EROFS
		}
		file, e := s.root.OpenFile(f.path, mode, 0)
		if e != nil {
			return nil, e
		}
		if f.file != nil {
			f.file.Close()
		}
		f.file = file
		p9qid(&out, i)
		p9put(&out, uint32(0))
	case 14, 72: // create, mkdir
		n := b.str()
		if !p9name(n) {
			return nil, syscall.EINVAL
		}
		flags := uint32(0)
		if kind == 14 {
			flags = b.u32()
		}
		mode := b.u32()
		b.u32()
		if e := s.room(512); e != nil {
			return nil, e
		}
		p := path.Join(f.path, n)
		if kind == 72 {
			if e := s.root.Mkdir(p, os.FileMode(mode&0777)); e != nil {
				return nil, e
			}
		} else {
			file, e := s.root.OpenFile(p, os.O_CREATE|os.O_EXCL|int(flags&3), os.FileMode(mode&0777))
			if e != nil {
				return nil, e
			}
			if f.file != nil {
				f.file.Close()
			}
			f.file = file
			f.append = flags&1024 != 0
			f.path = p
		}
		i, e := s.root.Lstat(p)
		if e != nil {
			return nil, e
		}
		p9qid(&out, i)
		if kind == 14 {
			p9put(&out, uint32(0))
		}
	case 16: // symlink
		n, target := b.str(), b.str()
		b.u32()
		if !p9name(n) {
			return nil, syscall.EINVAL
		}
		if e := s.room(uint64(len(target)) + 512); e != nil {
			return nil, e
		}
		p := path.Join(f.path, n)
		if e := s.root.Symlink(target, p); e != nil {
			return nil, e
		}
		i, e := s.root.Lstat(p)
		if e != nil {
			return nil, e
		}
		p9qid(&out, i)
	case 22:
		target, e := s.root.Readlink(f.path)
		if e != nil {
			return nil, e
		}
		p9put(&out, target)
	case 24: // getattr, Linux wire layout
		b.u64()
		i, e := s.root.Lstat(f.path)
		if e != nil {
			return nil, e
		}
		p9put(&out, uint64(0x7ff))
		p9qid(&out, i)
		// Ownership is in the guest's task identity, never the host daemon's.
		// Linux's collector uses 65537; VZ guest commands run as root.
		uid := uint32(65537)
		if runtime.GOOS == "darwin" {
			uid = 0
		}
		p9put(&out, p9mode(i), uid, uid, uint64(1), uint64(0), uint64(i.Size()), uint64(4096), uint64((i.Size()+511)/512))
		for range 3 {
			p9put(&out, uint64(i.ModTime().Unix()), uint64(i.ModTime().Nanosecond()))
		}
		for range 4 {
			p9put(&out, uint64(0))
		}
	case 26: // setattr
		valid := b.u32()
		mode := b.u32()
		b.u32()
		b.u32()
		size := b.u64()
		for range 4 {
			b.u64()
		}
		if b.err != nil {
			return nil, b.err
		}
		if valid&8 != 0 {
			if size > math.MaxInt64 {
				return nil, syscall.EFBIG
			}
			i, e := s.root.Stat(f.path)
			if e != nil {
				return nil, e
			}
			if size > uint64(i.Size()) {
				if e := s.room(size - uint64(i.Size())); e != nil {
					return nil, e
				}
			}
			file, e := s.root.OpenFile(f.path, os.O_WRONLY, 0)
			if e != nil {
				return nil, e
			}
			e = file.Truncate(int64(size))
			if e == nil {
				e = file.Sync()
			}
			file.Close()
			if e != nil {
				return nil, e
			}
		}
		if valid&1 != 0 {
			if e := s.root.Chmod(f.path, os.FileMode(mode&0777)); e != nil {
				return nil, e
			}
		}
	case 40: // readdir, offsets are entry indexes, no cached host descriptors
		offset, count := b.u64(), b.u32()
		if count > 256<<10 {
			return nil, syscall.EINVAL
		}
		d, e := s.root.Open(f.path)
		if e != nil {
			return nil, e
		}
		entries, e := d.ReadDir(-1)
		d.Close()
		if e != nil {
			return nil, e
		}
		var list bytes.Buffer
		for j := offset; j < uint64(len(entries)); j++ {
			i, e := entries[j].Info()
			if e != nil {
				return nil, e
			}
			var item bytes.Buffer
			p9qid(&item, i)
			typ := uint8(8)
			if i.IsDir() {
				typ = 4
			}
			if i.Mode()&os.ModeSymlink != 0 {
				typ = 10
			}
			p9put(&item, j+1, typ, entries[j].Name())
			if list.Len()+item.Len() > int(count) {
				break
			}
			list.Write(item.Bytes())
		}
		p9put(&out, uint32(list.Len()))
		out.Write(list.Bytes())
	case 116:
		offset, count := b.u64(), b.u32()
		if count > 256<<10 || offset > math.MaxInt64 || f.file == nil {
			return nil, syscall.EINVAL
		}
		buf := make([]byte, count)
		n, e := f.file.ReadAt(buf, int64(offset))
		if e != nil && e != io.EOF {
			return nil, e
		}
		p9put(&out, uint32(n))
		out.Write(buf[:n])
	case 118:
		offset, count := b.u64(), b.u32()
		if count > 256<<10 || uint64(count)+offset > math.MaxInt64 || f.file == nil {
			return nil, syscall.EINVAL
		}
		buf := make([]byte, count)
		if _, e := io.ReadFull(b, buf); e != nil {
			return nil, e
		}
		i, e := f.file.Stat()
		if e != nil {
			return nil, e
		}
		if f.append {
			offset = uint64(i.Size())
		}
		end := offset + uint64(count)
		if end > uint64(i.Size()) {
			if e := s.room(end - uint64(i.Size())); e != nil {
				return nil, e
			}
		}
		n, e := f.file.WriteAt(buf, int64(offset))
		if e == nil {
			e = f.file.Sync()
		}
		if e != nil {
			return nil, e
		}
		p9put(&out, uint32(n))
	case 50:
		b.u32()
		if f.file != nil {
			if e := f.file.Sync(); e != nil {
				return nil, e
			}
		}
	case 70:
		old, e := s.fid(b.u32())
		if e != nil {
			return nil, e
		}
		n := b.str()
		if !p9name(n) {
			return nil, syscall.EINVAL
		}
		if e := s.room(512); e != nil {
			return nil, e
		}
		return nil, s.root.Link(old.path, path.Join(f.path, n))
	case 76:
		n := b.str()
		b.u32()
		if !p9name(n) {
			return nil, syscall.EINVAL
		}
		return nil, s.root.Remove(path.Join(f.path, n))
	case 120:
		if f.file != nil {
			f.file.Close()
		}
		delete(s.fids, id)
	case 52: // single host filesystem; POSIX locks are not offered
		return nil, syscall.ENOSYS
	default:
		return nil, syscall.EOPNOTSUPP
	}
	return out.Bytes(), b.err
}
func (s *p9server) serve(c net.Conn) {
	if s.group != nil {
		s.mu.Lock()
		s.group.servers[s] = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.group.servers, s); s.mu.Unlock() }()
	}
	defer c.Close()
	defer s.root.Close()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, f := range s.fids {
			if f.file != nil {
				f.file.Close()
			}
		}
	}()
	for {
		var size uint32
		if binary.Read(c, binary.LittleEndian, &size) != nil {
			return
		}
		if size < 7 || size > 256<<10 {
			return
		}
		raw := make([]byte, size-4)
		if _, e := io.ReadFull(c, raw); e != nil {
			return
		}
		kind, tag := raw[0], binary.LittleEndian.Uint16(raw[1:])
		b := &p9buf{Reader: bytes.NewReader(raw[3:])}
		s.mu.Lock()
		body, e := s.handle(kind, b)
		if e == nil && b.err != nil {
			e = b.err
		}
		if e == nil {
			if d, err := s.root.Open("."); err == nil {
				e = d.Sync()
				d.Close()
			}
		}
		s.mu.Unlock()
		reply := kind + 1
		if e != nil {
			reply = 7
			errno := syscall.EIO
			var se syscall.Errno
			if errors.As(e, &se) {
				errno = se
			}
			body = make([]byte, 4)
			binary.LittleEndian.PutUint32(body, p9errno(errno))
		}
		var frame bytes.Buffer
		p9put(&frame, uint32(len(body)+7), reply, tag)
		frame.Write(body)
		if _, e := c.Write(frame.Bytes()); e != nil {
			return
		}
	}
}

// A shared lock covers quota accounting and mutations from every attachment.
var storageLocks sync.Map

func (s *Service) mountStorage(ctx context.Context, at map[string]any, quota uint64) error {
	id, resource, sandbox := textField(at, "id"), textField(at, "resource_id"), textField(at, "sandbox_id")
	rootPath := filepath.Join(s.dir, "storage", resource)
	if err := os.MkdirAll(rootPath, 0700); err != nil {
		return err
	}
	os.Chmod(rootPath, 0777)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	socketDir := filepath.Join(s.dir, "mounts")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		root.Close()
		return err
	}
	socket := filepath.Join(socketDir, id+".sock")
	os.Remove(socket)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		root.Close()
		return err
	}
	os.Chmod(socket, 0600)
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	lock, _ := storageLocks.LoadOrStore(rootPath, &p9group{servers: map[*p9server]bool{}})
	go func() {
		c, e := listener.Accept()
		stop()
		listener.Close()
		os.Remove(socket)
		if e != nil {
			root.Close()
			return
		}
		stopConn := context.AfterFunc(s.context(), func() { c.Close() })
		defer stopConn()
		(&p9server{root: root, quota: quota, readonly: textField(at, "mode") == "read_only", mu: &lock.(*p9group).mu, group: lock.(*p9group), fids: map[uint32]*p9fid{}}).serve(c)
	}()
	r := s.workerRequest(ctx, "mount", sandbox, s.sandboxGeneration(sandbox))
	r.Command = &pb.Request_StorageMount{StorageMount: &pb.StorageMount{SocketPath: socket, MountPath: textField(at, "mount_path"), ReadOnly: textField(at, "mode") == "read_only"}}
	err = s.worker.perform(ctx, r, "mounted")
	if err != nil {
		listener.Close()
	}
	if err != nil {
		return err
	}
	// Prove the kernel mounted it before advertising ready.
	observed, err := s.guestRun(ctx, sandbox, "python3", "-c", `import os,sys,time
p=sys.argv[1]
for _ in range(100):
 if any(line.split()[1]==p and line.split()[2]=='9p' for line in open('/proc/mounts')): sys.exit(0)
 time.sleep(.05)
sys.exit(1)`, textField(at, "mount_path"))
	if err != nil {
		return err
	}
	if observed.Exit != 0 {
		return reject("ENGINE_UNQUALIFIED", "guest did not mount 9p storage; use a seed with CONFIG_9P_FS and CONFIG_NET_9P_FD")
	}
	return nil
}

func (s *Service) unmountStorage(ctx context.Context, at map[string]any) error {
	sandbox := textField(at, "sandbox_id")
	r := s.workerRequest(ctx, "unmount", sandbox, s.sandboxGeneration(sandbox))
	r.Command = &pb.Request_StorageMount{StorageMount: &pb.StorageMount{MountPath: textField(at, "mount_path"), Unmount: true}}
	return s.worker.perform(ctx, r, "unmounted")
}

func p9errno(e syscall.Errno) uint32 {
	switch e {
	case syscall.ENOSYS:
		return 38
	case syscall.EOPNOTSUPP:
		return 95
	case syscall.EPROTONOSUPPORT:
		return 93
	case syscall.ENOTEMPTY:
		return 39
	case syscall.ELOOP:
		return 40
	default:
		return uint32(e)
	}
}

// Mount channels cannot survive a daemon restart or a VM saved-state restore.
// Reconnect each attachment to the same durable directory before readiness.
func (s *Service) remountStorage(ctx context.Context, sandbox string) error {
	type mount struct {
		record map[string]any
		quota  uint64
	}
	var mounts []mount
	s.mu.Lock()
	for _, at := range s.life.Attachments {
		if textField(at.R, "sandbox_id") != sandbox || textField(at.R, "state") != "ready" {
			continue
		}
		record := map[string]any{}
		for k, v := range at.R {
			record[k] = v
		}
		resource := textField(at.R, "resource_id")
		var quota uint64
		if textField(at.R, "resource_kind") == "volume" {
			quota = uintField(s.life.Volumes[resource].R, "quota_bytes")
		} else {
			quota = uintField(s.life.Caches[resource].R, "quota_bytes")
		}
		mounts = append(mounts, mount{record, quota})
	}
	s.mu.Unlock()
	for _, m := range mounts {
		if err := s.unmountStorage(ctx, m.record); err != nil {
			return err
		}
		if err := s.mountStorage(ctx, m.record, m.quota); err != nil {
			return err
		}
	}
	return nil
}

// Populate restored snapshots before exposing the filesystem, including for
// read-only mounts. Never remove or extract over a guest's mounted root.
func (s *Service) populateStorage(resource string, t tree, quota uint64) error {
	if len(t) == 0 {
		return nil
	}
	dest := filepath.Join(s.dir, "storage", resource)
	group, _ := storageLocks.LoadOrStore(dest, &p9group{servers: map[*p9server]bool{}})
	g := group.(*p9group)
	g.mu.Lock()
	defer g.mu.Unlock()
	// An existing root is initialized even when guests deleted every file.
	// Preserve its inode: attached servers may still hold an open root handle.
	_, err := os.Stat(dest)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	root, err := os.OpenRoot(staging)
	if err != nil {
		return err
	}
	defer root.Close()
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names)
	var used uint64
	for _, n := range names {
		e := t[n]
		if n == "." && e.Dir {
			continue
		}
		if n == "" || n == "." || path.Clean(n) != n || strings.HasPrefix(n, "/") || n == ".." || strings.HasPrefix(n, "../") {
			return reject("DATA_UNAVAILABLE", "invalid stored path")
		}
		used += 512
		if !e.Dir {
			if e.Size < 0 {
				return reject("DATA_UNAVAILABLE", "invalid stored size")
			}
			used += uint64(e.Size)
		}
		if used > quota {
			return syscall.ENOSPC
		}
		if err := root.MkdirAll(path.Dir(n), 0755); err != nil {
			return err
		}
		switch {
		case e.Dir:
			if err := root.MkdirAll(n, os.FileMode(e.Mode)&0777); err != nil {
				return err
			}
		case e.Link != "":
			if err := root.Symlink(e.Link, n); err != nil {
				return err
			}
		default:
			data, err := s.readObject(e.SHA256)
			if err != nil {
				return err
			}
			f, err := root.OpenFile(n, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(e.Mode)&0777)
			if err != nil {
				return err
			}
			_, err = f.Write(data)
			if err == nil {
				err = f.Sync()
			}
			f.Close()
			if err != nil {
				return err
			}
		}
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	return os.Rename(staging, dest)
}
