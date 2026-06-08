package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const providerCommandTimeout = 5 * time.Second

func LoadProviderCommand(command string) (FileConfig, error) {
	stdout, stderr, err := runProviderCommand(command, providerCommandTimeout)
	if err != nil {
		if errors.Is(err, errProviderCommandTimeout) {
			return FileConfig{}, fmt.Errorf("provider command timed out after 5s")
		}
		return FileConfig{}, fmt.Errorf("provider command failed: %w%s", err, commandOutput(stderr))
	}

	cfg, err := parseProviderCommandJSON(stdout)
	if err != nil {
		return FileConfig{}, err
	}
	if len(cfg.Providers) == 0 {
		return FileConfig{}, fmt.Errorf("provider command returned no providers")
	}

	providers, _, err := normalizeProvidersWithoutModelDefaults(cfg.Providers, cfg.ActiveProvider, map[string]string{})
	if err != nil {
		return FileConfig{}, err
	}
	cfg.Providers = providers
	return cfg, nil
}

var errProviderCommandTimeout = errors.New("provider command timeout")

func runProviderCommand(command string, timeout time.Duration) ([]byte, []byte, error) {
	cmd := shellCommand(command)
	configureCommandProcess(cmd)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return stdout.Bytes(), stderr.Bytes(), err
	case <-timer.C:
		terminateCommandProcess(cmd)
		<-done
		return stdout.Bytes(), stderr.Bytes(), errProviderCommandTimeout
	}
}

func shellCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(strings.TrimSpace(command), `"`) {
			command = "call " + command
		}
		return exec.Command("cmd", "/C", command)
	}
	return exec.Command("sh", "-c", command)
}

func commandOutput(stderr []byte) string {
	output := strings.TrimSpace(string(stderr))
	if output == "" {
		return ""
	}
	return ": " + redactSecrets(output)
}

func parseProviderCommandJSON(data []byte) (FileConfig, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return FileConfig{}, fmt.Errorf("provider command returned empty JSON")
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err == nil && (len(cfg.Providers) > 0 || cfg.ActiveProvider != "" || cfg.MaxTurns > 0) {
		if cfg.ActiveProvider == "" && len(cfg.Providers) == 1 {
			cfg.ActiveProvider = cfg.Providers[0].Name
		}
		return cfg, nil
	}

	var profile ProviderProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return FileConfig{}, fmt.Errorf("invalid provider command JSON: %w", err)
	}
	if profile.Name == "" {
		profile.Name = string(ProviderKindOpenAI)
	}
	return FileConfig{
		ActiveProvider: profile.Name,
		Providers:      []ProviderProfile{profile},
	}, nil
}
