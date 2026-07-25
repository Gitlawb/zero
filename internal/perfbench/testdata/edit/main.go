// Package edit is a small workspace for the edit-class benchmark tasks: each
// task asks the agent to make one targeted edit, verified by a grep/build
// command in the manifest.
package edit

import (
	"fmt"
	"os"
)

// Port is the default listen port.
const Port = 8080

// MaxRetries is the maximum number of retries before giving up.
const MaxRetries = 3

// Config holds runtime configuration.
type Config struct {
	Name string
}

// greet returns a greeting for the named user.
//
// This receieves a name and formats a hello message.
func greet(name string) string {
	return "hello"
}

// load reads a config file and returns its bytes, or an error on failure.
func load(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func main() {
	fmt.Println("debug: starting")
	cfg := Config{Name: "demo"}
	fmt.Println(greet(cfg.Name))
}
