package dictation

import (
	"archive/tar"
	"cmp"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

// Auto-download of the local engine + a default model (opt-in, behind a confirm
// in the F9 flow). Assets come from the official k2-fsa/sherpa-onnx GitHub
// releases and are verified against a SHA256 digest before use — the same trust
// boundary as installing them yourself, automated. Auto-downloading a native
// executable is a real trust decision, which is why it is opt-in and verified.
//
// Rather than freeze a single hardcoded version, the downloader resolves the
// release ASSETS via the GitHub API at download time and verifies each download
// against the API's per-asset digest — so a newer sherpa-onnx release (or
// "latest") works with no code change. A known-good version is still the DEFAULT
// (reproducible, smoke-tested), and for that default the resolved digest is
// cross-checked against a pinned value so a silently-changed release is caught.

// DefaultSherpaVersion is the pinned, smoke-tested release used unless
// stt.engineVersion overrides it. "latest" or any tag also works.
const DefaultSherpaVersion = "v1.13.3"

const (
	sherpaRepo      = "k2-fsa/sherpa-onnx"
	modelReleaseTag = "asr-models" // rolling release the ASR models live in
	modelAssetName  = "sherpa-onnx-moonshine-tiny-en-int8.tar.bz2"
	defaultAPIBase  = "https://api.github.com"
)

// engineAssetSuffix maps GOOS-GOARCH to the version-independent suffix of the
// sherpa-onnx executable bundle for that platform. Matching by suffix (not a
// full version-embedded name) is what lets any release resolve. The bundles are
// self-contained (RPATH $ORIGIN/../lib), so the extracted bin/ executables run
// with no library-path setup.
var engineAssetSuffix = map[string]string{
	"linux-amd64":   "linux-x64-shared-no-tts.tar.bz2",
	"darwin-arm64":  "osx-arm64-shared-no-tts.tar.bz2",
	"darwin-amd64":  "osx-x64-shared-no-tts.tar.bz2",
	"windows-amd64": "win-x64-shared-MT-Release-no-tts.tar.bz2",
}

// pinnedEngineDigest cross-checks the DefaultSherpaVersion engine assets (a
// silently-changed pinned release is refused). Empty for non-default versions,
// which are verified against the API digest only.
var pinnedEngineDigest = map[string]string{
	"linux-amd64":   "e5cbfd96f3666c25d2272d468e337764fce6a4d50ed074e4b8e5021bfd7fefc1",
	"darwin-arm64":  "ea73233bc250019ccf1f8a16feb29460efcd3425b50cac16980bcb76281d60bf",
	"darwin-amd64":  "563423887d63d8a831c473e0ab6815139fe2e7a3482936716c2a128071dd217f",
	"windows-amd64": "343c6c39cb4fe1880a9fb1abc8401d5174629ccbb147fca1fd53c440b2664570",
}

// pinnedModelDigest cross-checks the default Moonshine-tiny int8 model.
const pinnedModelDigest = "d5fe6ec4334fef36255b2a4010412cad4c007e33103fec62fb5d17cad88086f2"

// ModelVariant is a downloadable model the /stt-model picker offers, with a
// pinned SHA256 (the model release predates GitHub's per-asset digests, so these
// must be pinned). Batch variants use the offline binary; the streaming variant
// (a transducer) drives the websocket server for a live transcript.
type ModelVariant struct {
	ID          string
	Label       string
	Description string
	Bytes       int64
	AssetName   string
	Digest      string
	DirName     string
	Streaming   bool
	// Recommended marks a curated, checksum-pinned, validated model (shown first).
	Recommended bool
}

// ModelVariants returns the curated English models the download picker offers.
// This is a hand-picked shortlist (each pinned by SHA256 since the model release
// predates GitHub's per-asset digests), NOT the whole sherpa-onnx model zoo —
// many more models (other languages, sizes, and streaming/batch families) work
// via a manual stt.localModelPath. Streaming (live) options come first.
func ModelVariants() []ModelVariant {
	return []ModelVariant{
		// Live / streaming — transcript builds up as you speak.
		// DirName MUST match what ListModels derives from the asset ("model-"+id, id =
		// asset minus "sherpa-onnx-" prefix and ".tar.bz2" suffix, dates included) so a
		// model extracts to the same directory whether picked from the curated shortlist
		// or the full browse list — otherwise installed-detection disagrees between the
		// two views and the same model can be downloaded twice. Recommended matches the
		// full list too (recommendedModelIDs marks every curated variant), so the ★ and
		// ordering don't change when the background fetch swaps the curated list out.
		{
			ID: "streaming-zipformer-en-kroko", Label: "Kroko (newest, fastest)",
			Description: "smallest live model", Bytes: 54 << 20,
			AssetName:   "sherpa-onnx-streaming-zipformer-en-kroko-2025-08-06.tar.bz2",
			Digest:      "c8676e5ff9ac2a85296e53ee0fd4d5fb1db6770e7a7647166eeafe349ade6834",
			DirName:     "model-streaming-zipformer-en-kroko-2025-08-06",
			Streaming:   true,
			Recommended: true,
		},
		{
			ID: "streaming-zipformer-20m", Label: "Zipformer 20M",
			Description: "small, well-tested", Bytes: 121 << 20,
			AssetName: "sherpa-onnx-streaming-zipformer-en-20M-2023-02-17.tar.bz2",
			Digest:    "9c559283e8498d3fe95913c79ca1cb454bb26281ac2b102b41306c7d752765d9",
			DirName:   "model-streaming-zipformer-en-20M-2023-02-17",
			Streaming: true,
		},
		{
			ID: "streaming-zipformer", Label: "Zipformer",
			Description: "larger, more accurate", Bytes: 297 << 20,
			AssetName: "sherpa-onnx-streaming-zipformer-en-2023-06-26.tar.bz2",
			Digest:    "639e25b578e9e997131402199419c13a941f8e4e198e2da1ce57dbf5cf401282",
			DirName:   "model-streaming-zipformer-en-2023-06-26",
			Streaming: true,
		},
		{
			ID: "nemo-streaming-fast-conformer-transducer-en-80ms-int8", Label: "NeMo streaming (fast)",
			Description: "low-latency English", Bytes: 98 << 20,
			AssetName:   "sherpa-onnx-nemo-streaming-fast-conformer-transducer-en-80ms-int8.tar.bz2",
			Digest:      "7bd33a914e93370a1ba9c2066d9e841bdcad8613fa2a00537c1ae15d851a14d8",
			DirName:     "model-nemo-streaming-fast-conformer-transducer-en-80ms-int8",
			Streaming:   true,
			Recommended: true,
		},
		// Batch — transcribes once when you stop.
		{
			ID: "moonshine-tiny", Label: "Moonshine tiny",
			Description: "fast, smallest", Bytes: 103 << 20,
			AssetName: "sherpa-onnx-moonshine-tiny-en-int8.tar.bz2",
			Digest:    pinnedModelDigest,
			DirName:   "model-moonshine-tiny-en-int8",
		},
		{
			ID: "moonshine-base", Label: "Moonshine base",
			Description: "more accurate, larger", Bytes: 239 << 20,
			AssetName:   "sherpa-onnx-moonshine-base-en-int8.tar.bz2",
			Digest:      "21870cecaa2e44e4e2bf63e02d1072bed183ccd10284871353bd9d24dad14e5e",
			DirName:     "model-moonshine-base-en-int8",
			Recommended: true,
		},
		{
			ID: "sense-voice-zh-en-ja-ko-yue-int8-2025-09-09", Label: "SenseVoice",
			Description: "multilingual, accurate", Bytes: 158 << 20,
			AssetName:   "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09.tar.bz2",
			Digest:      "7305f7905bfcf77fa0b39388a313f3da35c68d971661a65475b56fb2162c8e63",
			DirName:     "model-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09",
			Recommended: true,
		},
		{
			// Heavy (~1 GB) but the best transcription quality — recommended when
			// accuracy matters more than speed/size. The asr-models release publishes
			// no digest for it, so (like every browse-listed model) it downloads with
			// TLS-only verification.
			ID: "whisper-large-v3", Label: "Whisper large v3",
			Description: "best accuracy, heavy", Bytes: 1019 << 20,
			AssetName:   "sherpa-onnx-whisper-large-v3.tar.bz2",
			DirName:     "model-whisper-large-v3",
			Recommended: true,
		},
	}
}

// recommendedModelIDs marks the curated variants flagged Recommended so the full
// browse list stars (and surfaces) exactly the ones the curated shortlist does —
// keyed off the same Recommended flag so the two views never disagree.
var recommendedModelIDs = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range ModelVariants() {
		if v.Recommended {
			m[v.AssetName] = true
		}
	}
	return m
}()

// pinnedModelDigests maps a model asset name to its pinned SHA256 (the curated
// variants), so browse-listed models we happen to know get verified too.
var pinnedModelDigests = func() map[string]string {
	m := map[string]string{}
	for _, v := range ModelVariants() {
		m[v.AssetName] = v.Digest
	}
	return m
}()

// curatedLabels maps a curated model's asset name to its friendly label, so the
// recommended rows read nicely in the full browse list too.
var curatedLabels = func() map[string]string {
	m := map[string]string{}
	for _, v := range ModelVariants() {
		m[v.AssetName] = v.Label
	}
	return m
}()

// browseLabel turns a raw asset id into a shorter display label: it drops the
// redundant "-int8"/quantization noise and the "streaming-" prefix (the group
// header already says live vs batch).
func browseLabel(id string) string {
	label := strings.TrimPrefix(id, "streaming-")
	for _, drop := range []string{"-int8", ".int8", "-quantized", "-fp16"} {
		label = strings.ReplaceAll(label, drop, "")
	}
	return label
}

// ListModels fetches every transcription model in the sherpa-onnx asr-models
// release and returns them as variants (the curated ones flagged Recommended and
// carrying a pinned digest; the rest with an empty digest, downloaded with
// TLS-only verification since that release predates GitHub's per-asset digests).
// Non-ASR assets (VAD, TTS, punctuation, speaker/keyword/…): filtered out.
func ListModels(ctx context.Context, client *http.Client, apiBase string) ([]ModelVariant, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	// Resolve the release id, then page its assets (a release can have hundreds).
	relURL := fmt.Sprintf("%s/repos/%s/releases/tags/%s", strings.TrimRight(apiBase, "/"), sherpaRepo, modelReleaseTag)
	var rel struct {
		ID     int64 `json:"id"`
		Assets []githubAsset
	}
	if err := getJSON(ctx, client, relURL, &rel); err != nil {
		return nil, fmt.Errorf("listing dictation models: %w", err)
	}
	assets := rel.Assets
	// If the inline assets look capped, page the assets endpoint for the rest.
	for page := 1; len(assets)%100 == 0 && len(assets) > 0; page++ {
		var more []githubAsset
		u := fmt.Sprintf("%s/repos/%s/releases/%d/assets?per_page=100&page=%d", strings.TrimRight(apiBase, "/"), sherpaRepo, rel.ID, page+1)
		if err := getJSON(ctx, client, u, &more); err != nil || len(more) == 0 {
			break
		}
		assets = append(assets, more...)
		if len(more) < 100 {
			break
		}
	}

	seen := map[string]bool{}
	var out []ModelVariant
	for _, a := range assets {
		if seen[a.Name] || !isTranscriptionModelAsset(a.Name) {
			continue
		}
		seen[a.Name] = true
		id := strings.TrimSuffix(strings.TrimPrefix(a.Name, "sherpa-onnx-"), ".tar.bz2")
		digest := strings.TrimPrefix(a.Digest, "sha256:")
		if digest == "" {
			digest = pinnedModelDigests[a.Name] // may still be empty (browse/unverified)
		}
		label := browseLabel(id)
		desc := modelFamily(a.Name) // readable language, e.g. "French"
		if nice, ok := curatedLabels[a.Name]; ok {
			label = nice
			desc = curatedDescriptions[a.Name] // curated blurb ("fast, smallest")
		}
		out = append(out, ModelVariant{
			ID:          id,
			Label:       label,
			Description: desc,
			Bytes:       a.Size,
			AssetName:   a.Name,
			Digest:      digest,
			DirName:     "model-" + id,
			Streaming:   strings.Contains(strings.ToLower(a.Name), "streaming-"),
			Recommended: recommendedModelIDs[a.Name],
		})
	}
	return out, nil
}

type githubAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// isTranscriptionModelAsset reports whether an asset is a speech-to-text model
// (as opposed to a VAD/TTS/punctuation/speaker/keyword/enhancement tool).
func isTranscriptionModelAsset(name string) bool {
	n := strings.ToLower(name)
	if !strings.HasPrefix(n, "sherpa-onnx-") || !strings.HasSuffix(n, ".tar.bz2") {
		return false
	}
	for _, bad := range []string{
		"-vad", "vad-", "tts", "punct", "speaker", "diariz", "kws", "keyword",
		"audio-tagging", "denois", "source-separation", "speech-enhancement",
		"spoken-language", "language-identification", "-lid", "melo", "kokoro",
		"vits", "matcha", "piper", "wespeaker",
		// Rockchip NPU (RKNN) builds for specific ARM boards — not CPU/ONNX, so
		// the standard sherpa-onnx binary can't run them.
		"rknn", "rk3566", "rk3568", "rk3562", "rk3588", "rk3576", "rk3528", "rk3399",
	} {
		if strings.Contains(n, bad) {
			return false
		}
	}
	for _, fam := range []string{
		"zipformer", "moonshine", "whisper", "paraformer", "sense-voice",
		"sensevoice", "conformer", "nemo", "canary", "parakeet", "fire-red",
		"fireredasr", "dolphin", "telespeech", "wenet", "omnilingual", "funasr",
		"medasr", "cohere", "qwen", "citrinet", "transducer", "-ctc-", "zipvoice",
	} {
		if strings.Contains(n, fam) {
			return true
		}
	}
	return false
}

// langNames maps ISO-ish language tokens in a model name to readable names.
var langNames = map[string]string{
	"en": "English", "zh": "Chinese", "yue": "Cantonese", "fr": "French",
	"de": "German", "es": "Spanish", "ru": "Russian", "ja": "Japanese",
	"ko": "Korean", "bn": "Bengali", "ar": "Arabic", "hi": "Hindi",
	"pt": "Portuguese", "nl": "Dutch", "vi": "Vietnamese", "th": "Thai",
	"uk": "Ukrainian", "fa": "Persian", "pl": "Polish", "tr": "Turkish",
	"cs": "Czech", "sv": "Swedish", "fil": "Filipino", "tl": "Tagalog",
	"fi": "Finnish", "el": "Greek", "he": "Hebrew", "ro": "Romanian",
}

// modelFamily is a short readable descriptor for a model, favoring the language
// (the most useful thing not already obvious from the group header) and falling
// back to the family. E.g. "French", "Chinese", "Multilingual".
func modelFamily(name string) string {
	n := strings.ToLower(name)
	if strings.Contains(n, "multi") || strings.Contains(n, "bilingual") ||
		strings.Contains(n, "trilingual") || strings.Contains(n, "polyglot") ||
		strings.Contains(n, "omnilingual") {
		return "Multilingual"
	}
	var langs []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			langs = append(langs, name)
		}
	}
	for _, tok := range strings.Split(strings.TrimSuffix(n, ".tar.bz2"), "-") {
		add(langNames[tok])
	}
	// Full language words spelled out in some names (e.g. "-cantonese-").
	for word, name := range map[string]string{
		"cantonese": "Cantonese", "mandarin": "Chinese", "english": "English",
		"chinese": "Chinese", "korean": "Korean", "japanese": "Japanese",
		"russian": "Russian", "arabic": "Arabic", "spanish": "Spanish",
		"french": "French", "german": "German", "vietnamese": "Vietnamese",
		"thai": "Thai", "farsi": "Persian", "persian": "Persian",
		"portuguese": "Portuguese", "italian": "Italian", "dutch": "Dutch",
		"ukrainian": "Ukrainian", "polish": "Polish", "turkish": "Turkish",
	} {
		if strings.Contains(n, word) {
			add(name)
		}
	}
	switch {
	case len(langs) == 0:
		return ""
	case len(langs) > 2:
		return "Multilingual"
	default:
		return strings.Join(langs, " + ")
	}
}

// curatedDescriptions maps a curated model's asset name to its quality blurb.
var curatedDescriptions = func() map[string]string {
	m := map[string]string{}
	for _, v := range ModelVariants() {
		m[v.AssetName] = v.Description
	}
	return m
}()

// EngineComponents identifies the resolved local-engine paths.
type EngineComponents struct {
	BinaryPath string // extracted sherpa-onnx-offline
	ServerPath string // extracted sherpa-onnx-online-websocket-server
	ModelPath  string // extracted model directory
}

// DownloadOptions configures EnsureLocalEngine.
type DownloadOptions struct {
	// DestRoot is where extracted engines/models live (e.g. ~/.config/zero/stt).
	DestRoot string
	// EngineVersion selects the sherpa-onnx release tag ("" → DefaultSherpaVersion;
	// "latest" and any tag also work).
	EngineVersion string
	// HTTPClient is injectable for tests; nil uses a plain client.
	HTTPClient *http.Client
	// APIBase overrides the GitHub API base (tests). "" → api.github.com.
	APIBase string
	// Model selection (default: the Moonshine-tiny int8 batch model). Set these
	// from a ModelVariant to download a different model.
	ModelAssetName    string
	ModelPinnedDigest string
	ModelDirName      string
	// ModelLabel is the friendly model name shown in progress ("Zipformer 20M").
	ModelLabel string
	// Progress receives live status strings for the UI, including a percentage
	// while downloading (e.g. "Engine 45% · 57/126 MB").
	Progress func(status string)
	// platformKey overrides runtime detection (tests).
	platformKey string
	// skipPinned disables the pinned-digest cross-check (tests, which use fixture
	// assets); the API-digest verification still runs.
	skipPinned bool
}

// AutoDownloadSupported reports whether the current platform has a known engine
// asset (so the F9 flow can offer the download). Termux/Android and unusual
// arches install manually or use a cloud provider.
func AutoDownloadSupported() bool {
	_, ok := engineAssetSuffix[platformKey()]
	return ok
}

func platformKey() string { return runtime.GOOS + "-" + runtime.GOARCH }

// EnsureLocalEngine downloads (if absent) and verifies the sherpa-onnx engine and
// the default model into DestRoot, returning the resolved paths. Idempotent: an
// already-extracted, still-present engine/model is reused without re-downloading.
// Every archive is SHA256-verified against the GitHub API digest before extraction.
func EnsureLocalEngine(ctx context.Context, opts DownloadOptions) (EngineComponents, error) {
	if strings.TrimSpace(opts.DestRoot) == "" {
		return EngineComponents{}, fmt.Errorf("dictation download: DestRoot is required")
	}
	key := opts.platformKey
	if key == "" {
		key = platformKey()
	}
	suffix, ok := engineAssetSuffix[key]
	if !ok {
		return EngineComponents{}, &SetupError{
			Tool: "sherpa-onnx auto-download",
			Hint: fmt.Sprintf("no prebuilt engine is known for %s — install sherpa-onnx manually or use a cloud provider (see docs/dictation.md)", key),
		}
	}
	version := strings.TrimSpace(opts.EngineVersion)
	if version == "" {
		version = DefaultSherpaVersion
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	apiBase := opts.APIBase
	if apiBase == "" {
		apiBase = defaultAPIBase
	}

	engineName := "engine-" + version + "-" + key
	engineDir := filepath.Join(opts.DestRoot, engineName)
	targetWindows := strings.HasPrefix(key, "windows-")
	// Resolve through the tarball's flattened subdir so an ALREADY-extracted
	// engine is found and not needlessly re-downloaded (the idempotency check).
	enginePublished := func(dir string) bool {
		bin, _ := resolveEnginePaths(dir, targetWindows)
		return fileExists(bin)
	}
	// The engine lock covers recovery, the decision to download, and the
	// promotion, and it is released before the model lock is taken. Holding
	// both would give two concurrent installs of different models an ordering
	// to get wrong for no gain.
	if err := withDestinationLock(ctx, opts.DestRoot, engineName, enginePublished, func(txn *destTxn) error {
		// A previous run may have been stopped mid-promotion, leaving the only
		// install in a holder beside engineDir. Put it back before deciding
		// whether anything needs downloading.
		restoreInterruptedPromotion(txn, engineDir, enginePublished, progress)
		if enginePublished(engineDir) {
			return nil
		}
		pinned := ""
		if version == DefaultSherpaVersion && !opts.skipPinned {
			pinned = pinnedEngineDigest[key]
		}
		asset, err := resolveAsset(ctx, client, apiBase, version, "sherpa-onnx-", suffix)
		if err != nil {
			return err
		}
		return downloadVerifyExtract(ctx, client, asset, pinned, false, "Engine", engineDir, txn, progress)
	}); err != nil {
		return EngineComponents{}, err
	}
	binPath, serverPath := resolveEnginePaths(engineDir, targetWindows)
	if !fileExists(binPath) {
		return EngineComponents{}, fmt.Errorf("dictation download: engine binary not found after extraction in %s", engineDir)
	}

	modelName := opts.ModelAssetName
	if modelName == "" {
		modelName = modelAssetName
	}
	modelDirName := opts.ModelDirName
	if modelDirName == "" {
		modelDirName = "model-moonshine-tiny-en-int8"
	}
	modelDir := filepath.Join(opts.DestRoot, modelDirName)
	if err := withDestinationLock(ctx, opts.DestRoot, modelDirName, dirHasModel, func(txn *destTxn) error {
		// promoteStagedDir is shared with the model, so a stop mid-promotion
		// leaves the model in a holder too. Put it back before deciding
		// anything is missing: without this an offline user has no download to
		// fall back on.
		restoreInterruptedPromotion(txn, modelDir, dirHasModel, progress)
		if dirHasModel(modelDir) {
			return nil
		}
		asset, err := resolveAsset(ctx, client, apiBase, modelReleaseTag, modelName, "")
		if err != nil {
			return err
		}
		modelPinned := opts.ModelPinnedDigest
		// Only fall back to the built-in digest for the DEFAULT model — a different
		// (browse-listed) model must never be checked against the default's digest.
		if modelPinned == "" && opts.ModelAssetName == "" {
			modelPinned = pinnedModelDigest
		}
		if opts.skipPinned {
			modelPinned = ""
		}
		modelLabel := opts.ModelLabel
		if modelLabel == "" {
			modelLabel = "Model"
		}
		// Models are data files from the official release: verify against a digest
		// when we have one (curated/pinned or API-provided), else allow a TLS-only
		// download (the model release predates GitHub's per-asset digests). The
		// engine BINARY above never allows this — a native executable is always
		// digest-verified.
		return downloadVerifyExtract(ctx, client, asset, modelPinned, true, modelLabel, modelDir, txn, progress)
	}); err != nil {
		return EngineComponents{}, err
	}
	resolvedModel := modelDir
	if !hasTokensFile(resolvedModel) {
		if child, err := flattenSingleChild(modelDir); err == nil {
			resolvedModel = child
		}
	}
	if !hasTokensFile(resolvedModel) {
		return EngineComponents{}, fmt.Errorf("dictation download: model files not found after extraction in %s", modelDir)
	}
	return EngineComponents{BinaryPath: binPath, ServerPath: serverPath, ModelPath: resolvedModel}, nil
}

type resolvedAsset struct {
	url    string
	sha256 string
	size   int64
	name   string
}

// resolveAsset queries the GitHub release for tag and returns the asset whose
// name has the given prefix and suffix, plus its SHA256 digest and size.
func resolveAsset(ctx context.Context, client *http.Client, apiBase, tag, namePrefix, nameSuffix string) (resolvedAsset, error) {
	tagPath := "tags/" + tag
	if tag == "" || tag == "latest" {
		tagPath = "latest"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/%s", strings.TrimRight(apiBase, "/"), sherpaRepo, tagPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return resolvedAsset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return resolvedAsset{}, fmt.Errorf("resolving sherpa-onnx release %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resolvedAsset{}, fmt.Errorf("resolving sherpa-onnx release %s: GitHub API HTTP %d", tag, resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
			Digest      string `json:"digest"`
			Size        int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return resolvedAsset{}, fmt.Errorf("parsing sherpa-onnx release %s: %w", tag, err)
	}
	for _, a := range release.Assets {
		if !strings.HasPrefix(a.Name, namePrefix) || !strings.HasSuffix(a.Name, nameSuffix) {
			continue
		}
		// The API digest may be empty for older assets (GitHub only populates it for
		// assets uploaded after ~2024 — the ASR models predate that). An empty
		// digest is fine here; downloadVerifyExtract requires the API digest OR a
		// pinned one and refuses only when neither exists.
		digest := strings.TrimPrefix(a.Digest, "sha256:")
		return resolvedAsset{url: a.DownloadURL, sha256: digest, size: a.Size, name: a.Name}, nil
	}
	return resolvedAsset{}, &SetupError{
		Tool: "sherpa-onnx auto-download",
		Hint: fmt.Sprintf("release %s has no asset matching %s*%s — try a different stt.engineVersion or install manually", tag, namePrefix, nameSuffix),
	}
}

// downloadVerifyExtract fetches the asset to a temp file, verifies its SHA256
// (against the API digest, and — when pinned is set — a cross-check that the
// resolved digest equals the audited value), and extracts the tar.bz2. A
// mismatch aborts before anything is extracted or run.
func downloadVerifyExtract(ctx context.Context, client *http.Client, asset resolvedAsset, pinned string, allowUnverified bool, label, destDir string, txn *destTxn, progress func(string)) error {
	// Refused here as well as in promoteStagedDir, so a caller that forgot the
	// lock does not get a full download and extraction before finding out.
	if !txn.holds(destDir) {
		return fmt.Errorf("dictation download: refusing to install %s: no install lock is held for %s", label, destDir)
	}
	// Determine the digest to verify against: the API digest and the pinned digest
	// must agree when both exist; at least one must exist unless allowUnverified
	// (models are data files from the official release — TLS-only is acceptable;
	// a native executable never sets allowUnverified).
	expected := asset.sha256
	if pinned != "" {
		if expected != "" && !strings.EqualFold(expected, pinned) {
			return fmt.Errorf("dictation download: %s digest %s does not match the pinned known-good value %s — refusing (the release may have changed)", asset.name, asset.sha256, pinned)
		}
		expected = pinned
	}
	if expected == "" && !allowUnverified {
		return fmt.Errorf("dictation download: no SHA256 available for %s (neither the GitHub API nor a pinned value) — refusing an unverifiable download", asset.name)
	}
	// Ensure the parent of destDir exists; the final destDir is created by
	// the atomic rename at the end of the function.
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}
	// Stage the extraction into a sibling temp dir and only swap it into
	// destDir on success. An interrupted run that left a truncated engine
	// binary in destDir would otherwise pass fileExists() on the next start
	// and break the local recorder silently.
	stageDir, err := os.MkdirTemp(filepath.Dir(destDir), filepath.Base(destDir)+".stage-*")
	if err != nil {
		return err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	tmp, err := os.CreateTemp(filepath.Dir(destDir), "stt-dl-*.tar.bz2")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.url, nil)
	if err != nil {
		tmp.Close()
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", asset.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("downloading %s: HTTP %d", asset.name, resp.StatusCode)
	}
	hasher := sha256.New()
	total := asset.size
	if total <= 0 {
		total = resp.ContentLength
	}
	src := io.Reader(resp.Body)
	if total > 0 {
		src = &progressReader{r: resp.Body, total: total, label: label, report: progress, lastPct: -1}
	}
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), src); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", asset.name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if expected != "" && !strings.EqualFold(got, expected) {
		return fmt.Errorf("dictation download: checksum mismatch for %s (want %s, got %s) — refusing to extract", asset.name, expected, got)
	}
	if err := extractTarBz2(tmpPath, stageDir, "Extracting "+label, progress); err != nil {
		return err
	}
	if err := promoteStagedDir(txn, stageDir, destDir, label, progress); err != nil {
		return err
	}
	cleanupStage = false
	return nil
}

// progressReader reports download progress as a percentage on each whole-percent
// change, formatted "<label> NN% · X/Y MB".
type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	label   string
	report  func(string)
	lastPct int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 {
		pct := int(p.read * 100 / p.total)
		if pct != p.lastPct {
			p.lastPct = pct
			p.report(fmt.Sprintf("%s %d%% · %d/%d MB", p.label, pct, p.read>>20, p.total>>20))
		}
	}
	return n, err
}

// enginePaths builds the engine executable paths. The ".exe" suffix follows the
// TARGET platform (the one the engine was downloaded for), not the host — they
// match in production (you download your own platform), but keying off the target
// keeps resolution correct when the two differ (e.g. a cross-platform test).
func enginePaths(engineDir string, targetWindows bool) (bin, server string) {
	ext := ""
	if targetWindows {
		ext = ".exe"
	}
	return filepath.Join(engineDir, "bin", "sherpa-onnx-offline"+ext),
		filepath.Join(engineDir, "bin", "sherpa-onnx-online-websocket-server"+ext)
}

// resolveEnginePaths finds the executables, flattening the tarball's single
// top-level directory when present.
func resolveEnginePaths(engineDir string, targetWindows bool) (bin, server string) {
	bin, server = enginePaths(engineDir, targetWindows)
	if fileExists(bin) {
		return bin, server
	}
	if child, err := flattenSingleChild(engineDir); err == nil && child != engineDir {
		return enginePaths(child, targetWindows)
	}
	return bin, server
}

// holderSuffix is what promoteStagedDir appends to an install's own name for the
// holder it sets that install aside in. An ordering number goes in the name
// because recovery has to pick the NEWEST holder when a failed cleanup left an
// older one behind, and nothing else records that order: Glob sorts lexically
// and a directory mtime tracks the install's contents, not its promotion. The
// number is one past the highest already beside this install, so a clock moving
// backward cannot invert it the way the wall-clock stamp it replaces could.
// fsOps is the filesystem seam the promotion path, the holder allocator, and
// recovery take every step through. A step that calls os directly cannot be made
// to fail, and the crash each of these steps exists to survive is then only
// reasoned about. Every field is one call, so a test can fail the second remove
// or the rename whose source is one particular holder and leave the rest real.
type fsOps struct {
	rename     func(from, to string) error
	removeAll  func(path string) error
	stat       func(name string) (os.FileInfo, error)
	lstat      func(name string) (os.FileInfo, error)
	readDir    func(name string) ([]os.DirEntry, error)
	readFile   func(name string) ([]byte, error)
	mkdir      func(name string, perm os.FileMode) error
	writeFile  func(name string, data []byte, perm os.FileMode) error
	create     func(name string, flag int, perm os.FileMode) (*os.File, error)
	createTemp func(dir, pattern string) (*os.File, error)
}

// holderFS is the seam every filesystem step in the promotion path goes through.
// Tests swap a field and restore it; nothing else writes to it.
var holderFS = fsOps{
	rename:     os.Rename,
	removeAll:  os.RemoveAll,
	stat:       os.Stat,
	lstat:      os.Lstat,
	readDir:    os.ReadDir,
	readFile:   os.ReadFile,
	mkdir:      os.Mkdir,
	writeFile:  os.WriteFile,
	create:     os.OpenFile,
	createTemp: os.CreateTemp,
}

// errInstallInProgress reports that another process held this destination's
// Install lock for the whole wait budget and the destination is still not
// usable. It is not an install failure: nothing was attempted, and the caller
// reports it and moves on.
var errInstallInProgress = errors.New("another install of this component is already running")

// installLockWait bounds how long a caller waits for another process to finish
// with a destination. It covers a download, so a budget shorter than the
// critical section it guards is a scheduled failure. A var so a test can shorten
// it; nothing else assigns to it.
var installLockWait = 2 * time.Minute

// installLockPoll is the retry interval while waiting. The lock is advisory and
// held by an open file handle, so there is nothing to wake us and polling is how
// the wait ends.
const installLockPoll = 50 * time.Millisecond

// installLockDir holds one lock file per destination. It is a sibling of the
// destinations rather than a file beside each one, so a lock file is never
// mistaken for part of an install and never moves with one.
const installLockDir = ".install-locks"

// destTxn is the handle proving its holder owns the Install lock for one
// destination. Recovery and promotion move and delete whole installs, so both
// take one: without it a second process can delete the copy this one is about
// to roll back to.
type destTxn struct {
	root string
	dest string
	lock *lockutil.FileLock
}

// destDir is the destination this handle locks. Callers keep passing their own
// destDir alongside the handle so a handle for a DIFFERENT destination is
// representable, which is what makes the mismatch testable rather than assumed.
func (t *destTxn) destDir() string { return filepath.Join(t.root, t.dest) }

// holds reports whether this handle actually locks destDir. A nil handle and a
// handle for another destination both fail closed.
func (t *destTxn) holds(destDir string) bool {
	return t != nil && t.lock != nil && t.destDir() == destDir
}

// release drops the Install lock. Idempotent, so a caller can release early and
// still defer it.
func (t *destTxn) release() {
	if t == nil || t.lock == nil {
		return
	}
	_ = t.lock.Release()
}

// lockDestination takes the Install lock for one destination under destRoot,
// waiting out another process that holds it until ctx ends or the budget runs
// out. The budget expiring is errInstallInProgress, which the caller answers by
// re-checking the destination rather than by failing.
func lockDestination(ctx context.Context, destRoot, dest string) (*destTxn, error) {
	// A destination is one path component. Any other shape puts the lock file
	// somewhere else, and two callers for the same destination would then lock
	// different inodes and both proceed.
	if dest == "" || dest != filepath.Base(dest) || dest == "." || dest == ".." {
		return nil, fmt.Errorf("dictation download: %q is not an install destination name", dest)
	}
	// destRoot is otherwise created lazily by the download this lock covers, and
	// lockutil opens the root with O_DIRECTORY, so on a first run there would be
	// no directory to take the lock in.
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(destRoot, installLockDir)
	if err := holderFS.mkdir(lockDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	lockPath := filepath.Join(lockDir, dest+".lock")
	deadline := time.Now().Add(installLockWait)
	for {
		lock, err := lockutil.TryAcquireFileLockAt(destRoot, lockPath)
		if err == nil {
			return &destTxn{root: destRoot, dest: dest, lock: lock}, nil
		}
		if !errors.Is(err, lockutil.ErrLockHeld) {
			return nil, err
		}
		// Waiting is the normal case: the holder is almost always another
		// process installing this very component, and failing on the first
		// contended try would turn that into a user-visible install failure.
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w: %s is locked by another process", errInstallInProgress, dest)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(installLockPoll):
		}
	}
}

// withDestinationLock runs fn under dest's Install lock. A wait that runs out is
// not by itself a failure: the likeliest reason is that the other process
// finished this exact install, so usable re-checks the destination before the
// caller is told anything is wrong.
func withDestinationLock(ctx context.Context, destRoot, dest string, usable func(string) bool, fn func(*destTxn) error) error {
	txn, err := lockDestination(ctx, destRoot, dest)
	if err != nil {
		if errors.Is(err, errInstallInProgress) && usable(filepath.Join(destRoot, dest)) {
			return nil
		}
		return err
	}
	defer txn.release()
	return fn(txn)
}

const holderSuffix = ".previous-"

// holderStamp reads back the ordering number in a holder name, reporting false
// for a name it cannot order (one this package did not write). New names carry a
// per-install sequence; names written by released versions carry wall-clock
// nanoseconds. Both compare the same way.
func holderStamp(destDir, holder string) (int64, bool) {
	rest := strings.TrimPrefix(filepath.Base(holder), filepath.Base(destDir)+holderSuffix)
	digits, _, found := strings.Cut(rest, "-")
	if !found {
		return 0, false
	}
	stamp, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return stamp, true
}

// holderSeqSuffix closes a sequenced holder name. holderStamp cuts on the first
// '-' after the digits, so a name ending at the digits reads back as unstamped,
// which is silent: an unstamped holder sorts last and is still restorable, so
// the ordering key would stop existing with nothing failing. os.MkdirTemp used
// to supply this separator with its random suffix.
const holderSeqSuffix = "-seq"

// holderSeqAttempts bounds the walk up from a taken number, in the spirit of the
// retry limit os.MkdirTemp applies to its own random names.
const holderSeqAttempts = 10000

// nextHolderSeq is the number a new holder should claim: one past the highest
// already set aside for this install. That is what makes the order survive a
// clock that moves backward, since a promotion that reads an existing holder
// always allocates above it. Holders for a different install are a separate
// sequence and are never compared against this one.
func nextHolderSeq(destDir string) (int64, error) {
	entries, err := holderFS.readDir(filepath.Dir(destDir))
	if err != nil {
		return 0, err
	}
	prefix := filepath.Base(destDir) + holderSuffix
	var high int64
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if stamp, ok := holderStamp(destDir, entry.Name()); ok && stamp > high {
			high = stamp
		}
	}
	if high == math.MaxInt64 {
		// The addition below would wrap negative, and %020d of a negative
		// renders a '-' the parser reads as empty digits, so the holder would
		// drop out of the ordering without saying so. Refuse instead.
		return 0, fmt.Errorf("a previous install of %s is named at the maximum sequence; remove it before installing again", filepath.Base(destDir))
	}
	return high + 1, nil
}

// createSequencedHolder claims the first free holder name from n upward. Nothing
// locks this directory, so exclusive creation is what arbitrates: two promotions
// racing cannot both win a name, and the loser walks up above the winner.
func createSequencedHolder(destDir string, n int64) (string, error) {
	if n < 1 {
		return "", fmt.Errorf("refusing to allocate holder sequence %d", n)
	}
	for i := 0; i < holderSeqAttempts; i++ {
		path := fmt.Sprintf("%s%s%020d%s", destDir, holderSuffix, n, holderSeqSuffix)
		// The writer and the reader agree on the format or nothing is written.
		if stamp, ok := holderStamp(destDir, path); !ok || stamp != n {
			return "", fmt.Errorf("holder name %q does not read back as sequence %d", filepath.Base(path), n)
		}
		err := holderFS.mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		if n == math.MaxInt64 {
			return "", fmt.Errorf("holder sequence exhausted for %s", filepath.Base(destDir))
		}
		n++
	}
	return "", fmt.Errorf("could not claim a holder name for %s after %d attempts", filepath.Base(destDir), holderSeqAttempts)
}

// restoreInterruptedPromotion puts back an install that promoteStagedDir set
// aside but never replaced, which is what a process stop between its two renames
// leaves behind: destDir absent and the only usable copy in a .previous-* holder
// nothing else looks at. Anything already at destDir wins, and the check for it
// is explicit rather than leaning on os.Rename refusing an existing directory.
// Best effort by design, since the caller can still download a fresh engine.
// published reports whether destDir holds an install this caller can actually
// use. Recovery needs it because "there is something at destDir" and "a
// promotion published there" are different claims, and only the second one
// makes a holder beside it superseded.
func restoreInterruptedPromotion(txn *destTxn, destDir string, published func(string) bool, report func(string)) {
	// Recovery moves and deletes whole installs. Without destDir's Install lock
	// it can restore a holder a promotion in another process is parked on and
	// remove it, leaving that promotion's rollback nothing to put back.
	if !txn.holds(destDir) {
		if report != nil {
			report(fmt.Sprintf("Skipping recovery of %s: no install lock is held for it", filepath.Base(destDir)))
		}
		return
	}
	if _, err := holderFS.lstat(destDir); err == nil {
		// destDir is live. A holder is only ever filled by renaming destDir
		// aside, so a destDir holding a USABLE install means a later promotion
		// published over every holder beside it. Those are superseded copies of
		// whole installs, and promoteStagedDir's cleanup is the only thing that
		// removes them: when it fails the copy is stranded for good, and each
		// later replacement strands another.
		//
		// Merely non-empty is not that evidence. An empty husk, or a half
		// populated directory left by something outside this transaction, can
		// sit at destDir while the only usable copy is in the holder; reaping on
		// either would delete exactly what the caller is about to need, and an
		// offline caller has no download to fall back on. So the holder only
		// loses to a destination that is genuinely usable.
		//
		// A removal that fails is left for the next call, which reaches this
		// same branch and tries again.
		if published != nil && published(destDir) {
			for _, holder := range holdersBeside(destDir) {
				_ = holderFS.removeAll(holder)
			}
		}
		return
	}
	// ReadDir and a prefix rather than filepath.Glob: destDir is a real path,
	// and a '[' anywhere in it opens a character class to Glob, which then
	// matches nothing and strands the install this exists to put back.
	holders := holdersBeside(destDir)
	// Newest first: an unstamped holder is the least recent thing we can claim
	// to know about, so it is only reached once every stamped one has failed.
	slices.SortStableFunc(holders, func(a, b string) int {
		sa, oka := holderStamp(destDir, a)
		sb, okb := holderStamp(destDir, b)
		if oka != okb {
			if oka {
				return -1
			}
			return 1
		}
		return cmp.Compare(sb, sa)
	})
	for _, holder := range holders {
		install := filepath.Join(holder, "install")
		if _, err := holderFS.stat(install); err != nil {
			continue
		}
		if err := holderFS.rename(install, destDir); err != nil {
			continue
		}
		// Only the holder this install came out of is removed; an older one is
		// left for a human, never deleted on a guess about which is current.
		_ = holderFS.removeAll(holder)
		return
	}
}

// holdersBeside lists the holders promoteStagedDir may have left for destDir.
// The prefix is the attribution: a holder for a different install is a separate
// concern and is never touched on this one's account.
func holdersBeside(destDir string) []string {
	// ReadDir and a prefix rather than filepath.Glob: destDir is a real path,
	// and a '[' anywhere in it opens a character class to Glob, which then
	// matches nothing and strands the install this exists to put back.
	parent := filepath.Dir(destDir)
	entries, err := holderFS.readDir(parent)
	if err != nil {
		return nil
	}
	prefix := filepath.Base(destDir) + holderSuffix
	holders := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			holders = append(holders, filepath.Join(parent, entry.Name()))
		}
	}
	return holders
}

// promoteStagedDir moves stageDir into place at destDir. os.Rename refuses to
// overwrite a non-empty directory, so any previous install has to move out of
// the way first; it is set aside rather than deleted, and put back if the
// promotion fails, so destDir is never left holding no install at all. If the
// restore fails too, the set-aside copy is kept and named in the error.
func promoteStagedDir(txn *destTxn, stageDir, destDir, label string, report func(string)) error {
	// Same reason recovery refuses: the window between the two renames has the
	// only copy of the install in a holder, and nothing else keeps a concurrent
	// recovery out of it.
	if !txn.holds(destDir) {
		if report != nil {
			report(fmt.Sprintf("Refusing to install %s: no install lock is held for %s", label, filepath.Base(destDir)))
		}
		return fmt.Errorf("promoting staged %s: no install lock is held for %s", label, destDir)
	}
	holder := ""
	cleanupHolder := true
	defer func() {
		if holder != "" && cleanupHolder {
			_ = holderFS.removeAll(holder)
		}
	}()

	restore := func() error { return nil }
	if _, err := holderFS.lstat(destDir); err == nil {
		var seq int64
		seq, err = nextHolderSeq(destDir)
		if err != nil {
			return fmt.Errorf("setting aside previous %s install: %w", label, err)
		}
		holder, err = createSequencedHolder(destDir, seq)
		if err != nil {
			return fmt.Errorf("setting aside previous %s install: %w", label, err)
		}
		previous := filepath.Join(holder, "install")
		if err := holderFS.rename(destDir, previous); err != nil {
			return fmt.Errorf("setting aside previous %s install: %w", label, err)
		}
		restore = func() error { return holderFS.rename(previous, destDir) }
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking previous %s install: %w", label, err)
	}

	if err := holderFS.rename(stageDir, destDir); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			cleanupHolder = false
			return fmt.Errorf("promoting staged %s: %w (previous install left in %s: %v)", label, err, holder, restoreErr)
		}
		return fmt.Errorf("promoting staged %s: %w", label, err)
	}
	return nil
}

// extractTarBz2 unpacks a bzip2-compressed tar into destDir, guarding against
// path-traversal entries. It reports extraction progress by how much of the
// (compressed) archive it has consumed — the same MB scale as the download, so
// "Extracting … 50% · 520/1041 MB" lines up with the download that preceded it.
func extractTarBz2(archivePath, destDir, label string, progress func(string)) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	var src io.Reader = f
	if progress != nil {
		if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
			src = &progressReader{r: f, total: info.Size(), label: label, report: progress, lastPct: -1}
		}
	}
	tr := tar.NewReader(bzip2.NewReader(src))
	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, hdr.Name)
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if absTarget != destAbs && !strings.HasPrefix(absTarget, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("dictation download: archive entry %q escapes destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

// flattenSingleChild returns the sole subdirectory of dir (the tarball's
// top-level folder) when dir contains exactly one directory entry.
func flattenSingleChild(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		return filepath.Join(dir, dirs[0]), nil
	}
	return dir, nil
}

// ModelDownloaded reports whether a model variant is already extracted under
// destRoot (so the picker can mark it as installed). dirName is the variant's
// DirName.
func ModelDownloaded(destRoot, dirName string) bool {
	if destRoot == "" || dirName == "" {
		return false
	}
	return dirHasModel(filepath.Join(destRoot, dirName))
}

// EngineDownloaded reports whether the shared sherpa-onnx engine is already on
// disk for this platform (one engine serves every model), so the picker can drop
// the engine's size from a model's download total once it's been fetched.
func EngineDownloaded(destRoot, version string) bool {
	if destRoot == "" {
		return false
	}
	if version == "" {
		version = DefaultSherpaVersion
	}
	engineDir := filepath.Join(destRoot, "engine-"+version+"-"+platformKey())
	bin, _ := resolveEnginePaths(engineDir, runtime.GOOS == "windows")
	return fileExists(bin)
}

func dirHasModel(dir string) bool {
	if hasTokensFile(dir) {
		return true
	}
	if child, err := flattenSingleChild(dir); err == nil && child != dir {
		return hasTokensFile(child)
	}
	return false
}

// hasTokensFile reports whether dir holds a sherpa tokens file — usually
// "tokens.txt", but some families (Whisper) prefix it as "<model>-tokens.txt".
func hasTokensFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "tokens.txt") {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
