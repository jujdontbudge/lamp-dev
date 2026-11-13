package access

import (
	ssh "github.com/gliderlabs/ssh"
	xssh "golang.org/x/crypto/ssh"
	"io"
	"lamp.eilmer.si/internal/surface"
	"strconv"
	"strings"
)

// SSH local forwards explicitly address sandbox_id:port. They can only reach
// that sandbox's loopback; SSH never dials a network address on the access host.
func (s *Service) guestTCP(server *ssh.Server, connection *xssh.ServerConn, newChannel xssh.NewChannel, ctx ssh.Context) {
	var request struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if xssh.Unmarshal(newChannel.ExtraData(), &request) != nil || !strings.HasPrefix(request.Host, "sb_") || !safeID(request.Host) || request.Port == 0 || request.Port > 65535 {
		newChannel.Reject(xssh.Prohibited, "destination must be sandbox_id:port")
		return
	}
	user, ok := s.user(ctx.User())
	if !ok {
		newChannel.Reject(xssh.Prohibited, "unknown principal")
		return
	}
	credential, err := token(user.TokenFile)
	if err != nil {
		newChannel.Reject(xssh.Prohibited, "credential unavailable")
		return
	}
	channel, requests, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go xssh.DiscardRequests(requests)
	client := &surface.Client{Socket: s.config.APISocket, Token: credential}
	defer client.Close()
	// This program executes inside the sandbox over the public exec API.
	const relay = `import os,socket,sys,threading
s=socket.create_connection(('127.0.0.1',int(sys.argv[1])),10)
s.settimeout(None)
def upload():
 try:
  while data:=os.read(0,4096):s.sendall(data)
  s.shutdown(socket.SHUT_WR)
 except OSError:pass
threading.Thread(target=upload,daemon=True).start()
while data:=s.recv(32768):
 view=memoryview(data)
 while view:view=view[os.write(1,view):]
`
	_, _, _ = surface.ExecChannel(ctx, client, request.Host, []string{"python3", "-u", "-c", relay, strconv.Itoa(int(request.Port))}, channel, channel, io.Discard)
}
