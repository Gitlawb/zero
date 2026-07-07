package dictation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Recorder captures microphone audio. Start/Stop is the batch path (used on
// Termux, and as the fallback everywhere); StartStreaming is the desktop-only
// continuous-PCM path.
type Recorder interface {
	Start() error
	// Stop ends a batch recording and returns the recorded audio bytes (WAV on
	// desktop, M4A on Termux — SniffFormat distinguishes them).
	Stop() ([]byte, error)
	// StartStreaming begins continuous capture of raw little-endian PCM16 mono
	// at the configured sample rate. Chunks arrive on the channel in ~50ms
	// units; the channel closes when capture ends (stop called, max duration
	// hit, or trailing-silence auto-stop). stop is idempotent.
	StartStreaming() (chunks <-chan []byte, stop func() error, err error)
}

// Platform selects the audio-capture toolchain (§9 of the design doc).
type Platform string

const (
	PlatformLinux       Platform = "linux"
	PlatformDarwin      Platform = "darwin"
	PlatformWindows     Platform = "windows"
	PlatformTermux      Platform = "termux"
	PlatformUnsupported Platform = ""
)

// DetectPlatform picks the capture toolchain for the current host. Termux is
// detected by its environment (it reports GOOS=linux/android): TERMUX_VERSION
// is exported to every Termux shell, and PREFIX carries the app package path.
func DetectPlatform() Platform {
	if os.Getenv("TERMUX_VERSION") != "" || strings.Contains(os.Getenv("PREFIX"), "com.termux") {
		return PlatformTermux
	}
	switch runtime.GOOS {
	case "linux":
		return PlatformLinux
	case "darwin":
		return PlatformDarwin
	case "windows":
		return PlatformWindows
	}
	return PlatformUnsupported
}

// StreamingSupported reports whether the platform's capture tool can write raw
// PCM to stdout. Termux's termux-microphone-record requires a regular output
// file — a hard tool limitation, so Termux is batch-only permanently.
func (p Platform) StreamingSupported() bool {
	switch p {
	case PlatformLinux, PlatformDarwin, PlatformWindows:
		return true
	}
	return false
}

const (
	// DefaultSampleRate is what the local engine and Deepgram consume.
	// OpenAI Realtime's pcm16 requires 24kHz; its transcriber constructs the
	// recorder accordingly.
	DefaultSampleRate = 16000
	// DefaultMaxDuration is the hard runaway-recording cap (§12), applied on
	// every platform, batch and streaming. Overridable via
	// stt.maxDurationSeconds.
	DefaultMaxDuration = 5 * time.Minute

	// Trailing-silence auto-stop (§9): stop ~2s after the signal falls below a
	// 3% amplitude threshold — SoX's own `silence 1 0.1 3% 1 2.0 3%` numbers.
	silenceThresholdPct = 3
	silenceHold         = 2 * time.Second

	chunkInterval = 50 * time.Millisecond
	stopGrace     = 3 * time.Second
)

// RecorderOptions configures a Recorder. Zero values pick sensible defaults.
type RecorderOptions struct {
	Platform        Platform
	SampleRate      int
	MaxDuration     time.Duration
	SilenceAutoStop bool
	// WindowsAudioDevice is the dshow capture device name; auto-detected from
	// `ffmpeg -list_devices` when empty.
	WindowsAudioDevice string

	// Test seams (unexported): fake the OS boundary, mirroring how
	// internal/lsp/manager_test.go stubs serverStarter.
	starter   processStarter
	runOutput commandOutputRunner
	tempDir   func() (string, error)
	now       func() time.Time
}

func (o RecorderOptions) withDefaults() RecorderOptions {
	if o.Platform == PlatformUnsupported {
		o.Platform = DetectPlatform()
	}
	if o.SampleRate <= 0 {
		o.SampleRate = DefaultSampleRate
	}
	if o.MaxDuration <= 0 {
		o.MaxDuration = DefaultMaxDuration
	}
	if o.starter == nil {
		o.starter = startProcess
	}
	if o.runOutput == nil {
		o.runOutput = runCommandOutput
	}
	if o.tempDir == nil {
		o.tempDir = audioSpillDir
	}
	if o.now == nil {
		o.now = time.Now
	}
	return o
}

// NewRecorder builds the platform recorder. Construction is cheap and
// side-effect free; tools are only exec'd on Start/StartStreaming.
func NewRecorder(opts RecorderOptions) Recorder {
	o := opts.withDefaults()
	return &recorder{opts: o}
}

type recorder struct {
	opts RecorderOptions

	mu        sync.Mutex
	proc      processHandle
	filePath  string
	recording bool
}

var errAlreadyRecording = errors.New("a recording is already in progress")

// Start begins a batch (record-to-file) capture.
func (r *recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recording {
		return errAlreadyRecording
	}
	if r.opts.Platform == PlatformUnsupported {
		return fmt.Errorf("dictation is not supported on %s", runtime.GOOS)
	}

	dir, err := r.opts.tempDir()
	if err != nil {
		return fmt.Errorf("preparing dictation temp dir: %w", err)
	}
	ext := ".wav"
	if r.opts.Platform == PlatformTermux {
		ext = ".m4a"
	}
	tmp, err := os.CreateTemp(dir, "rec-*"+ext)
	if err != nil {
		return fmt.Errorf("creating dictation temp file: %w", err)
	}
	path := tmp.Name()
	_ = tmp.Close()

	if r.opts.Platform == PlatformTermux {
		// termux-microphone-record starts the recording in the Termux:API
		// companion app and exits immediately; there is no long-lived process
		// to hold. Stop is a separate `-q` invocation.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := r.opts.runOutput(ctx, "termux-microphone-record",
			"-l", strconv.Itoa(maxSeconds(r.opts.MaxDuration)), "-f", path)
		if err != nil {
			_ = os.Remove(path)
			return startError("termux-microphone-record", r.opts.Platform, err, out)
		}
		r.filePath = path
		r.recording = true
		return nil
	}

	spec, err := batchCommand(r.opts, path)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	proc, _, err := r.opts.starter(spec)
	if err != nil {
		_ = os.Remove(path)
		return startError(spec.name, r.opts.Platform, err, nil)
	}
	r.proc = proc
	r.filePath = path
	r.recording = true
	return nil
}

// Stop ends a batch capture, reads the recorded file, deletes it, and returns
// the audio bytes.
func (r *recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return nil, errors.New("no recording in progress")
	}
	proc, path := r.proc, r.filePath
	// Termux: Start's termux-microphone-record branch leaves r.proc nil because
	// there's no long-lived process to hold (Stop is a separate `-q` invocation
	// via runOutput). Treat that as legitimate; only reject a nil proc that
	// came from StartStreaming (which owns its process via the returned
	// closure).
	if proc == nil && r.opts.Platform != PlatformTermux {
		return nil, errors.New("recording was started with StartStreaming; call the stop closure returned from StartStreaming instead of Stop()")
	}
	r.proc, r.filePath, r.recording = nil, "", false
	defer os.Remove(path)

	if r.opts.Platform == PlatformTermux {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if out, err := r.opts.runOutput(ctx, "termux-microphone-record", "-q"); err != nil {
			return nil, fmt.Errorf("stopping termux recording: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return readRecordingWithRetry(path, r.opts.now)
	}

	if err := proc.StopGracefully(); err != nil {
		_ = proc.Kill()
	}
	// A signal-terminated capture tool reports a non-zero exit — expected, not
	// an error. The recorded file's contents are the real success signal.
	_ = waitWithTimeout(proc, stopGrace)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading recorded audio: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("no audio was captured (is the microphone available?)")
	}
	return data, nil
}

// readRecordingWithRetry reads the Termux recording, retrying briefly: the
// companion app finalizes the file asynchronously after `-q` returns.
func readRecordingWithRetry(path string, now func() time.Time) ([]byte, error) {
	deadline := now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if now().After(deadline) {
			if err != nil {
				return nil, fmt.Errorf("reading recorded audio: %w", err)
			}
			return nil, errors.New("no audio was captured (check Termux:API app and microphone permission)")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// batchCommand builds the record-to-file argv for desktop platforms (§9).
// The max-duration cap is enforced by the tool itself so a runaway recording
// ends even if Zero never calls Stop.
func batchCommand(opts RecorderOptions, path string) (commandSpec, error) {
	rate := strconv.Itoa(opts.SampleRate)
	maxSec := strconv.Itoa(maxSeconds(opts.MaxDuration))
	switch opts.Platform {
	case PlatformLinux:
		return commandSpec{
			name: "arecord",
			args: []string{"-q", "-f", "S16_LE", "-r", rate, "-c", "1", "-t", "wav", "-d", maxSec, path},
		}, nil
	case PlatformDarwin:
		args := []string{"-q", "-d", "-r", rate, "-c", "1", "-b", "16", "-e", "signed-integer", path, "trim", "0", maxSec}
		if opts.SilenceAutoStop {
			// SoX's built-in trailing-silence auto-stop: end ~2s after the
			// signal falls below 3% amplitude. A backstop for a forgotten
			// manual stop, not VAD — recording still starts on an explicit key.
			args = append(args, "silence", "1", "0.1", "3%", "1", "2.0", "3%")
		}
		return commandSpec{name: "sox", args: args}, nil
	case PlatformWindows:
		device := opts.WindowsAudioDevice
		if device == "" {
			detected, err := detectWindowsAudioDevice(opts.runOutput)
			if err != nil {
				return commandSpec{}, err
			}
			device = detected
		}
		return commandSpec{
			name: "ffmpeg",
			args: []string{"-hide_banner", "-loglevel", "error", "-f", "dshow", "-i", "audio=" + device,
				"-ac", "1", "-ar", rate, "-c:a", "pcm_s16le", "-t", maxSec, "-y", path},
			stopViaStdin: "q",
		}, nil
	}
	return commandSpec{}, fmt.Errorf("no batch capture tool for platform %q", opts.Platform)
}

// streamCommand builds the raw-PCM-to-stdout argv for desktop platforms (§9).
// Same tools as batch, invoked with their stdout flag — Termux has no stdout
// mode, which is why it is batch-only.
func streamCommand(opts RecorderOptions) (commandSpec, error) {
	rate := strconv.Itoa(opts.SampleRate)
	switch opts.Platform {
	case PlatformLinux:
		return commandSpec{
			name:       "arecord",
			args:       []string{"-q", "-t", "raw", "-f", "S16_LE", "-r", rate, "-c", "1", "-"},
			wantStdout: true,
		}, nil
	case PlatformDarwin:
		return commandSpec{
			name:       "sox",
			args:       []string{"-q", "-d", "-t", "raw", "-r", rate, "-e", "signed", "-b", "16", "-c", "1", "-"},
			wantStdout: true,
		}, nil
	case PlatformWindows:
		device := opts.WindowsAudioDevice
		if device == "" {
			detected, err := detectWindowsAudioDevice(opts.runOutput)
			if err != nil {
				return commandSpec{}, err
			}
			device = detected
		}
		return commandSpec{
			name: "ffmpeg",
			args: []string{"-hide_banner", "-loglevel", "error", "-f", "dshow", "-i", "audio=" + device,
				"-ac", "1", "-ar", rate, "-f", "s16le", "-"},
			wantStdout:   true,
			stopViaStdin: "q",
		}, nil
	}
	return commandSpec{}, fmt.Errorf("streaming capture is not supported on platform %q", opts.Platform)
}

// StartStreaming begins continuous PCM capture with the §12 safety nets: a
// hard max-duration cap and Go-side trailing-silence auto-stop (the streaming
// analog of SoX's silence effect — we see the samples, so we measure them).
func (r *recorder) StartStreaming() (<-chan []byte, func() error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recording {
		return nil, nil, errAlreadyRecording
	}
	spec, err := streamCommand(r.opts)
	if err != nil {
		return nil, nil, err
	}
	proc, stdout, err := r.opts.starter(spec)
	if err != nil {
		return nil, nil, startError(spec.name, r.opts.Platform, err, nil)
	}
	if stdout == nil {
		_ = proc.Kill()
		return nil, nil, errors.New("capture process has no stdout pipe")
	}
	r.recording = true

	chunkBytes := chunkSizeBytes(r.opts.SampleRate)
	chunks := make(chan []byte, 8)
	done := make(chan struct{})
	var stopOnce sync.Once
	stop := func() error {
		stopOnce.Do(func() {
			close(done)
			if err := proc.StopGracefully(); err != nil {
				_ = proc.Kill()
			}
			_ = waitWithTimeout(proc, stopGrace)
			_ = stdout.Close()
			r.mu.Lock()
			r.recording = false
			r.mu.Unlock()
		})
		return nil
	}

	// Max-duration cap: streaming has no tool-side -d/-t equivalent that also
	// flushes stdout cleanly everywhere, so the cap runs on our side of the pipe.
	capTimer := time.AfterFunc(r.opts.MaxDuration, func() { _ = stop() })

	go func() {
		defer close(chunks)
		defer capTimer.Stop()
		// Tear down on ANY exit — including the capture process ending on its own
		// (EOF/crash), which the loop below returns on without going through an
		// explicit stop(). stop() is idempotent (sync.Once), so this is safe even
		// when the session was already stopped externally; it clears r.recording and
		// closes proc/stdout so a self-terminating device doesn't wedge the recorder.
		defer func() { _ = stop() }()
		threshold := int16(32767 * silenceThresholdPct / 100)
		silentChunks, speechSeen := 0, false
		silenceLimit := int(silenceHold / chunkInterval)
		for {
			buf := make([]byte, chunkBytes)
			n, err := readFullOrEOF(stdout, buf)
			if n > 0 {
				chunk := buf[:n]
				if r.opts.SilenceAutoStop {
					if chunkPeak(chunk) >= threshold {
						speechSeen = true
						silentChunks = 0
					} else if speechSeen {
						silentChunks++
						if silentChunks >= silenceLimit {
							go func() { _ = stop() }()
						}
					}
				}
				select {
				case chunks <- chunk:
				case <-done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return chunks, stop, nil
}

// chunkSizeBytes is 50ms of PCM16 mono at the given rate — small chunks mean
// faster time-to-first-partial (§6a).
func chunkSizeBytes(sampleRate int) int {
	return sampleRate * 2 / int(time.Second/chunkInterval)
}

// readFullOrEOF fills buf from r, returning what it read and any terminal
// error. A short final read (pipe closing mid-chunk) still returns its bytes.
func readFullOrEOF(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// chunkPeak returns the loudest absolute sample in a little-endian PCM16 chunk.
func chunkPeak(chunk []byte) int16 {
	var peak int16
	for i := 0; i+1 < len(chunk); i += 2 {
		s := int16(uint16(chunk[i]) | uint16(chunk[i+1])<<8)
		if s == -32768 {
			return 32767
		}
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak
}

// levelFloorDB and levelCeilDB bound the RMS→meter mapping. Speech RMS is small
// (roughly -45 dBFS quiet to -15 dBFS loud), so a LINEAR scale barely moves the
// bars — a dB (logarithmic) scale, like real audio meters, is what makes normal
// speech span the meter.
const (
	levelFloorDB = -50.0 // ≈ near-silence → empty
	levelCeilDB  = -15.0 // loud speech → full
)

// ChunkLevel returns a perceptual 0..1 loudness for a little-endian PCM16 chunk,
// driving the live recording waveform from the actual microphone input. It uses
// an RMS→dBFS→linear mapping over [levelFloorDB, levelCeilDB].
func ChunkLevel(chunk []byte) float64 {
	var sumSq float64
	n := 0
	for i := 0; i+1 < len(chunk); i += 2 {
		s := int16(uint16(chunk[i]) | uint16(chunk[i+1])<<8)
		f := float64(s) / 32768.0
		sumSq += f * f
		n++
	}
	if n == 0 {
		return 0
	}
	rms := math.Sqrt(sumSq / float64(n))
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms) // dBFS (negative)
	level := (db - levelFloorDB) / (levelCeilDB - levelFloorDB)
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	return level
}

func maxSeconds(d time.Duration) int {
	sec := int(d / time.Second)
	if sec < 1 {
		sec = 1
	}
	return sec
}

// detectWindowsAudioDevice picks the first dshow audio capture device from
// `ffmpeg -list_devices`. dshow has no "default" alias, so a concrete device
// name is required; stt.windowsAudioDevice overrides the auto-pick.
func detectWindowsAudioDevice(runOutput commandOutputRunner) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := runOutput(ctx, "ffmpeg", "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	// Listing always "fails" (dummy isn't a real input); the device table is
	// in the output regardless. Lines look like: [dshow @ ...] "Mic name" (audio)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "(audio)") {
			continue
		}
		start := strings.Index(line, `"`)
		if start < 0 {
			continue
		}
		rest := line[start+1:]
		end := strings.Index(rest, `"`)
		if end <= 0 {
			continue
		}
		return rest[:end], nil
	}
	return "", &SetupError{
		Tool: "ffmpeg",
		Hint: "no dshow audio capture device found — set stt.windowsAudioDevice to your microphone's name (list with: ffmpeg -list_devices true -f dshow -i dummy)",
	}
}

// startError maps a failed capture-tool launch to setup guidance: a missing
// binary is an expected unconfigured state (matching internal/lsp's
// missing-server philosophy), not an internal error.
func startError(tool string, platform Platform, err error, output []byte) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &SetupError{Tool: tool, Hint: installHint(platform)}
	}
	if len(output) > 0 {
		return fmt.Errorf("starting %s: %w (%s)", tool, err, strings.TrimSpace(string(output)))
	}
	return fmt.Errorf("starting %s: %w", tool, err)
}

func installHint(platform Platform) string {
	switch platform {
	case PlatformLinux:
		return "install alsa-utils to enable dictation (e.g. sudo dnf install alsa-utils / sudo apt install alsa-utils)"
	case PlatformDarwin:
		return "install sox to enable dictation (brew install sox)"
	case PlatformWindows:
		return "install ffmpeg and ensure it is on PATH to enable dictation"
	case PlatformTermux:
		return "run `pkg install termux-api` and install the Termux:API companion app to enable dictation"
	}
	return "dictation is not supported on this platform"
}
