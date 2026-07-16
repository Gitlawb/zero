package provideroauth

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const copilotInspectTokenLimit = 128
const copilotInspectDepthLimit = 256

type copilotJSONFrame struct {
	kind         byte
	state        byte
	key          string
	sectionArray bool
	messageItem  bool
}

func inspectCopilotRequest(src io.Reader) (lastRole string, hasImages bool, ok bool) {
	reader := bufio.NewReaderSize(src, 32*1024)
	stack := make([]copilotJSONFrame, 0, 8)
	rootDone := false

	closeFrame := func() bool {
		stack = stack[:len(stack)-1]
		if len(stack) == 0 {
			rootDone = true
		}
		return true
	}

	var startValue func(byte) bool
	startValue = func(first byte) bool {
		parentIndex := len(stack) - 1
		parent := &stack[parentIndex]
		key := parent.key
		messageItem := parent.messageItem
		sectionArray := len(stack) == 1 && parent.kind == '{' &&
			(key == "messages" || key == "input") && first == '['

		switch parent.kind {
		case '{':
			parent.state = 3
		case '[':
			parent.state = 1
		default:
			return false
		}

		switch first {
		case '{':
			if len(stack) >= copilotInspectDepthLimit {
				return false
			}
			stack = append(stack, copilotJSONFrame{
				kind:        '{',
				messageItem: parent.sectionArray,
			})
			return true
		case '[':
			if len(stack) >= copilotInspectDepthLimit {
				return false
			}
			stack = append(stack, copilotJSONFrame{
				kind:         '[',
				sectionArray: sectionArray,
			})
			return true
		case '"':
			value, captured, err := readCopilotJSONString(reader)
			if err != nil {
				return false
			}
			if captured {
				if messageItem && key == "role" {
					lastRole = value
				}
				if key == "type" {
					switch strings.ToLower(value) {
					case "image", "image_url", "input_image":
						hasImages = true
					}
				}
			}
			return true
		default:
			return readCopilotJSONPrimitive(reader, first)
		}
	}

	for {
		next, err := readCopilotJSONNonSpace(reader)
		if errors.Is(err, io.EOF) {
			return lastRole, hasImages, rootDone && len(stack) == 0
		}
		if err != nil {
			return "", false, false
		}
		if len(stack) == 0 {
			if rootDone || next != '{' {
				return "", false, false
			}
			stack = append(stack, copilotJSONFrame{kind: '{'})
			continue
		}

		frame := &stack[len(stack)-1]
		switch frame.kind {
		case '{':
			switch frame.state {
			case 0:
				if next == '}' {
					closeFrame()
					continue
				}
				if next != '"' {
					return "", false, false
				}
				key, captured, err := readCopilotJSONString(reader)
				if err != nil {
					return "", false, false
				}
				if captured {
					frame.key = key
					if key == "image_url" || key == "input_image" {
						hasImages = true
					}
				} else {
					frame.key = ""
				}
				frame.state = 1
			case 1:
				if next != ':' {
					return "", false, false
				}
				frame.state = 2
			case 2:
				if !startValue(next) {
					return "", false, false
				}
			case 3:
				switch next {
				case ',':
					frame.state = 0
					frame.key = ""
				case '}':
					closeFrame()
				default:
					return "", false, false
				}
			}
		case '[':
			switch frame.state {
			case 0:
				if next == ']' {
					closeFrame()
					continue
				}
				if !startValue(next) {
					return "", false, false
				}
			case 1:
				switch next {
				case ',':
					frame.state = 0
				case ']':
					closeFrame()
				default:
					return "", false, false
				}
			}
		default:
			return "", false, false
		}
	}
}

func readCopilotJSONNonSpace(reader *bufio.Reader) (byte, error) {
	for {
		next, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		switch next {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return next, nil
		}
	}
}

func readCopilotJSONString(reader *bufio.Reader) (value string, captured bool, err error) {
	raw := make([]byte, 0, 32)
	raw = append(raw, '"')
	captured = true
	for {
		next, readErr := reader.ReadByte()
		if readErr != nil {
			return "", false, readErr
		}
		if next < 0x20 {
			return "", false, errors.New("invalid control character in JSON string")
		}
		if captured {
			if len(raw) >= copilotInspectTokenLimit {
				captured = false
				raw = nil
			} else {
				raw = append(raw, next)
			}
		}
		switch next {
		case '"':
			if !captured {
				return "", false, nil
			}
			var decoded string
			if unmarshalErr := json.Unmarshal(raw, &decoded); unmarshalErr != nil {
				return "", false, unmarshalErr
			}
			return decoded, true, nil
		case '\\':
			escaped, escapeErr := reader.ReadByte()
			if escapeErr != nil {
				return "", false, escapeErr
			}
			if !strings.ContainsRune(`"\/bfnrtu`, rune(escaped)) {
				return "", false, errors.New("invalid JSON escape")
			}
			if captured {
				if len(raw) >= copilotInspectTokenLimit {
					captured = false
					raw = nil
				} else {
					raw = append(raw, escaped)
				}
			}
			if escaped == 'u' {
				for range 4 {
					hex, hexErr := reader.ReadByte()
					if hexErr != nil {
						return "", false, hexErr
					}
					if !isCopilotJSONHex(hex) {
						return "", false, errors.New("invalid JSON unicode escape")
					}
					if captured {
						if len(raw) >= copilotInspectTokenLimit {
							captured = false
							raw = nil
						} else {
							raw = append(raw, hex)
						}
					}
				}
			}
		}
	}
}

func readCopilotJSONPrimitive(reader *bufio.Reader, first byte) bool {
	token := make([]byte, 0, 16)
	token = append(token, first)
	captured := true
	for {
		next, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return !captured || json.Valid(token)
		}
		if err != nil {
			return false
		}
		switch next {
		case ' ', '\t', '\r', '\n':
			if captured {
				return json.Valid(token)
			}
			return true
		case ',', ']', '}':
			if unreadErr := reader.UnreadByte(); unreadErr != nil {
				return false
			}
			if captured {
				return json.Valid(token)
			}
			return true
		default:
			if captured {
				if len(token) >= copilotInspectTokenLimit {
					captured = false
					token = nil
				} else {
					token = append(token, next)
				}
			}
		}
	}
}

func isCopilotJSONHex(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}
