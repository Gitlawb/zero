package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type messageReader struct {
	reader *bufio.Reader
}

type messageWriter struct {
	writer *bufio.Writer
}

func newMessageReader(reader io.Reader) *messageReader {
	return &messageReader{reader: bufio.NewReader(reader)}
}

func newMessageWriter(writer io.Writer) *messageWriter {
	return &messageWriter{writer: bufio.NewWriter(writer)}
}

func (reader *messageReader) read() (rpcMessage, error) {
	contentLength := 0
	for {
		line, err := reader.reader.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "content-length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed <= 0 {
				return rpcMessage{}, fmt.Errorf("invalid MCP content length %q", value)
			}
			contentLength = parsed
		}
	}
	if contentLength <= 0 {
		return rpcMessage{}, fmt.Errorf("missing MCP content length")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader.reader, body); err != nil {
		return rpcMessage{}, err
	}
	var message rpcMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return rpcMessage{}, fmt.Errorf("invalid MCP JSON-RPC message: %w", err)
	}
	return message, nil
}

func (writer *messageWriter) write(message rpcMessage) error {
	if message.JSONRPC == "" {
		message.JSONRPC = "2.0"
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer.writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := writer.writer.Write(body); err != nil {
		return err
	}
	return writer.writer.Flush()
}
