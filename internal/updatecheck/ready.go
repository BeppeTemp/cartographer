package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A release that exists is not a release the user's channel can install
// (D1350): the tap's cask names the previous version, the release has no
// binaries yet, the image tag is not pushed. Check announces a newer release
// only when the channel it was asked about can install it; the checks below
// run only when a newer release exists, so a current binary costs no request.

const (
	// DefaultCaskURL is the cask file in the tap (.goreleaser.yaml
	// homebrew_casks: repository BeppeTemp/homebrew-tap, directory Casks).
	DefaultCaskURL = "https://raw.githubusercontent.com/BeppeTemp/homebrew-tap/main/Casks/cartographer.rb"
	// DefaultRegistryURL is the registry host of the server image.
	DefaultRegistryURL = "https://ghcr.io"
	// imageRepo is the image repository path on that registry.
	imageRepo = "beppetemp/cartographer"

	// EnvCaskURL and EnvRegistryURL override the two defaults, for tests
	// only, like EnvAPIURL.
	EnvCaskURL     = "CARTOGRAPHER_UPDATE_CASK_URL"
	EnvRegistryURL = "CARTOGRAPHER_UPDATE_REGISTRY_URL"

	// maxReadyBytes bounds a cask or token body.
	maxReadyBytes = 256 << 10
)

// ErrNotInstallable means the newest release exists but the channel cannot
// install it yet, or that could not be verified. Check returns it with no
// cache written, so the next run asks again.
var ErrNotInstallable = errors.New("newest release not installable through this channel yet")

// notInstallableError carries the tag that was found but could not be
// confirmed installable; it is ErrNotInstallable for errors.Is.
type notInstallableError struct {
	Tag   string
	Cause error // nil: the channel answered "not yet"
}

func (e *notInstallableError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%v: %s: %v", ErrNotInstallable, e.Tag, e.Cause)
	}
	return fmt.Sprintf("%v: %s", ErrNotInstallable, e.Tag)
}

func (e *notInstallableError) Is(target error) bool { return target == ErrNotInstallable }

// pendingEntry is the short negative cache, beside the cache file: the tag
// found not installable, for which channel, and when.
type pendingEntry struct {
	Tag     string    `json:"tag"`
	Channel Channel   `json:"channel"`
	At      time.Time `json:"at"`
}

func readPending(path string, ch Channel) (pendingEntry, bool) {
	if path == "" {
		return pendingEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pendingEntry{}, false
	}
	var p pendingEntry
	if json.Unmarshal(data, &p) != nil || !stableTag(p.Tag) || p.Channel != ch || p.At.IsZero() {
		return pendingEntry{}, false
	}
	return p, true
}

// writePending is best effort, like writeCache: a failure only means the
// next run asks again.
func writePending(path string, p pendingEntry) {
	if path == "" {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
	}
}

var caskVersion = regexp.MustCompile(`(?m)^\s*version\s+"([^"]+)"`)

// releaseAsset is the part of a GitHub release asset the check reads.
type releaseAsset struct {
	Name string `json:"name"`
}

// installable reports whether the channel can install tag. A false answer
// with a nil error is "not yet"; an error is "could not tell": both mean no
// notice.
func installable(ctx context.Context, tag string, assets []releaseAsset, opts Options, getenv func(string) string, hc *http.Client, ua string) (bool, error) {
	switch opts.Channel {
	case ChannelHomebrew:
		return caskNames(ctx, tag, opts, getenv, hc, ua)
	case ChannelInstallSh, ChannelInstallPS1:
		return hasPlatformAssets(assets, opts), nil
	case ChannelContainer:
		return imageExists(ctx, tag, opts, getenv, hc, ua)
	}
	return true, nil // go-install, unknown: the tag is all it needs
}

// hasPlatformAssets: the binary install.sh / install.ps1 download (the names
// .goreleaser.yaml's archives produce) and the checksum manifest both
// installers refuse to install without.
func hasPlatformAssets(assets []releaseAsset, opts Options) bool {
	want := fmt.Sprintf("cartographer-%s-%s", opts.GOOS, opts.GOARCH)
	if opts.GOOS == "windows" {
		want += ".zip"
	}
	var bin, sums bool
	for _, a := range assets {
		switch a.Name {
		case want:
			bin = true
		case "sha256sums.txt":
			sums = true
		}
	}
	return bin && sums
}

func doReq(ctx context.Context, hc *http.Client, method, u, ua string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return hc.Do(req)
}

func override(opt, envKey, def string, getenv func(string) string) string {
	if opt != "" {
		return strings.TrimRight(opt, "/")
	}
	if v := getenv(envKey); v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// caskNames reads the cask and compares its version with tag. The tap is a
// public repository and raw.githubusercontent.com is not rate-limited like
// the API, so no token is sent.
func caskNames(ctx context.Context, tag string, opts Options, getenv func(string) string, hc *http.Client, ua string) (bool, error) {
	resp, err := doReq(ctx, hc, http.MethodGet, override(opts.CaskURL, EnvCaskURL, DefaultCaskURL, getenv), ua, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("cask: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReadyBytes))
	if err != nil {
		return false, err
	}
	m := caskVersion.FindSubmatch(body)
	if m == nil {
		return false, errors.New("cask: no version line")
	}
	return strings.TrimPrefix(string(m[1]), "v") == strings.TrimPrefix(tag, "v"), nil
}

// imageExists asks the registry for the tag's manifest: an anonymous pull
// token, then a HEAD. 404 is "not pushed yet"; anything else but 200 is an
// error.
func imageExists(ctx context.Context, tag string, opts Options, getenv func(string) string, hc *http.Client, ua string) (bool, error) {
	base := override(opts.RegistryURL, EnvRegistryURL, DefaultRegistryURL, getenv)
	tokURL := base + "/token?service=ghcr.io&scope=" + url.QueryEscape("repository:"+imageRepo+":pull")
	resp, err := doReq(ctx, hc, http.MethodGet, tokURL, ua, nil)
	if err != nil {
		return false, err
	}
	var tok struct {
		Token string `json:"token"`
	}
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(io.LimitReader(resp.Body, maxReadyBytes)).Decode(&tok)
	} else {
		err = fmt.Errorf("registry token: HTTP %d", resp.StatusCode)
	}
	resp.Body.Close()
	if err != nil {
		return false, err
	}
	if tok.Token == "" {
		return false, errors.New("registry token: empty")
	}
	accept := strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", ")
	resp, err = doReq(ctx, hc, http.MethodHead, base+"/v2/"+imageRepo+"/manifests/"+url.PathEscape(tag), ua,
		map[string]string{"Authorization": "Bearer " + tok.Token, "Accept": accept})
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("image manifest: HTTP %d", resp.StatusCode)
}
