package tools

import (
	"bufio"
	"io"
)

func readRawLine(reader *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if line != nil || err == bufio.ErrBufferFull {
				line = append(line, fragment...)
			} else {
				line = fragment
			}
		}
		switch err {
		case nil:
			return line, true, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				return line, false, nil
			}
			return nil, false, io.EOF
		default:
			return nil, false, err
		}
	}
}

func trimLineBreak(raw []byte, ended bool) []byte {
	if !ended || len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return raw
	}
	raw = raw[:len(raw)-1]
	if len(raw) > 0 && raw[len(raw)-1] == '\r' {
		raw = raw[:len(raw)-1]
	}
	return raw
}
