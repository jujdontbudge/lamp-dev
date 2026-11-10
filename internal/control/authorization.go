package control

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type peerContextKey struct{}
type publicPeer struct {
	principal string
	owner     bool
}

func SharedAPIDir(path string) error {
	if err := os.Mkdir(path, 0755); err == nil {
		if err := os.Chmod(path, 0755); err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || int(stat.Uid) != os.Geteuid() || stat.Mode&0022 != 0 || stat.Mode&0055 != 0055 {
		return errors.New("shared API directory must be owned by this user, traversable and not writable by others")
	}
	return nil
}

func (s *Service) publicPeer(connection *net.UnixConn) (publicPeer, bool) {
	if AuthenticateLocal(connection) == nil {
		return publicPeer{principal: "pr_local", owner: true}, true
	}
	for uid, principal := range s.readers {
		if authenticatePeer(connection, int(uid), 0) == nil {
			return publicPeer{principal: principal}, true
		}
	}
	return publicPeer{}, false
}

func (s *Service) AuthenticatePublicPeer(connection *net.UnixConn) error {
	if _, ok := s.publicPeer(connection); !ok {
		return errors.New("public API peer is not enrolled")
	}
	return nil
}

func (s *Service) PeerContext(ctx context.Context, connection net.Conn) context.Context {
	peer := publicPeer{}
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		peer, _ = s.publicPeer(unixConnection)
	}
	return context.WithValue(ctx, peerContextKey{}, peer)
}

func (s *Service) authorizePublicRequest(ctx context.Context, method, path string) bool {
	peer, present := ctx.Value(peerContextKey{}).(publicPeer)
	if peer.owner || (!present && len(s.readers) == 0) {
		return true
	}
	if peer.principal == "" || method != "GET" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return false
	}
	for id, share := range s.journal.Shares {
		if share.State != "active" || share.SubjectId == nil || *share.SubjectId != peer.principal || share.ResourceKind == nil || *share.ResourceKind != "template" || share.ResourceId == nil {
			continue
		}
		template, ok := s.journal.Templates[*share.ResourceId]
		if !ok {
			continue
		}
		if path == "/v1/shares/"+id || path == "/v1/templates/"+template.TemplateID || path == "/v1/blobs/"+template.BlobID || path == "/v1/blobs/"+template.BlobID+"/content" {
			return true
		}
	}
	return false
}

func validPrincipal(principal string) bool {
	if !strings.HasPrefix(principal, "pr_") || len(principal) < 4 || len(principal) > 128 {
		return false
	}
	for _, value := range principal {
		if value != '_' && value != '-' && value != '.' && !(value >= 'a' && value <= 'z') && !(value >= 'A' && value <= 'Z') && !(value >= '0' && value <= '9') {
			return false
		}
	}
	return true
}

func (s *Service) enrolledShareReader(principal string) bool {
	for _, known := range s.readers {
		if known == principal {
			return true
		}
	}
	return false
}
