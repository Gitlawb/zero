package peermsg

import (
	"context"
	"net"
)

type localTransport interface {
	Endpoint(root, nonce string, pid int) (string, error)
	Listen(endpoint string) (net.Listener, error)
	Dial(context.Context, string) (net.Conn, error)
	Remove(string) error
}

func platformTransport() localTransport { return newPlatformTransport() }
