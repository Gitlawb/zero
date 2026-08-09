//go:build windows

package peermsg

import (
	"context"
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

type windowsPipeTransport struct{}

func newPlatformTransport() localTransport { return windowsPipeTransport{} }

func (windowsPipeTransport) Endpoint(_ string, nonce string, pid int) (string, error) {
	return fmt.Sprintf(`\\.\pipe\zero-peer-%d-%s`, pid, nonce), nil
}

func (windowsPipeTransport) Listen(endpoint string) (net.Listener, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, fmt.Errorf("peer messaging: query current process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("peer messaging: query current user: %w", err)
	}
	userSID := user.User.Sid.String()
	return winio.ListenPipe(endpoint, &winio.PipeConfig{
		// Limit the pipe to LocalSystem and the exact user running this process.
		// The receiver still treats every peer message as untrusted agent input,
		// never as user authority.
		SecurityDescriptor: fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)", userSID),
		MessageMode:        false,
		InputBufferSize:    maxFrameBytes,
		OutputBufferSize:   4096,
	})
}

func (windowsPipeTransport) Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func (windowsPipeTransport) Remove(string) error { return nil }
