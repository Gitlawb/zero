// Test seams: helpers only test code uses, kept out of the production binary.
package acp

import "context"

// Serve runs the connection read loop until the stream closes or ctx is done.
func (a *Agent) Serve(ctx context.Context) error { return a.conn.Serve(ctx) }

func ImageBlock(base64Data, mimeType string) ContentBlock {
	return ContentBlock{Type: "image", Data: base64Data, MimeType: mimeType}
}
