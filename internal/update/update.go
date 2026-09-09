// Package update implements self-update from GitHub Releases: releases are
// tagged vX.Y.Z with one raw binary per platform and a checksums.txt beside
// them, which is exactly what .github/workflows/release.yml publishes.
//
// The repository is public, so no credential is needed and none is required. A
// token is used when one happens to be available — it raises the API rate limit
// from 60 requests an hour to 5000, and it lets Repo point at a private fork.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultRepo is where the releases are. It is the source repository
	// itself, which is only possible because that repository is public — see
	// decision 27 for the separate-dist-repo alternative and why pgctl does not
	// need it.
	DefaultRepo = "richarddavenport/pgctl"

	// TagPrefix selects release tags. A tag without it is not a release: the
	// repository has had `docs/landscape`-style refs pushed, and a listing that
	// took whatever sorted first would have offered one as an upgrade.
	TagPrefix = "v"

	// ChecksumsAsset is the file every release carries, one `<sha256>  <name>`
	// line per binary.
	ChecksumsAsset = "checksums.txt"
)

// apiBase and executable are variables so a test can point them somewhere
// local. Nothing else writes them.
//
// executable in particular: Apply replaces the file os.Executable names, and a
// test of that cannot be allowed to replace the test binary.
var (
	apiBase    = "https://api.github.com"
	executable = os.Executable
)

// Release is one published pgctl version.
type Release struct {
	Version string // "v0.2.0"
	Assets  []Asset
}

// Asset is one downloadable file on a release.
type Asset struct {
	Name string `json:"name"`
	// URL is the API url, not the browser one: downloading through the API with
	// an Accept of application/octet-stream is the form that works for a
	// private repository too, and it is identical for a public one.
	URL string `json:"url"`
}

// OptionalToken finds a token if there is one and reports no error when there
// is not.
//
// Updating a public tool must not depend on being logged in to GitHub. The
// version of this in the tool it was ported from used a token-or-nothing check,
// so the update notice never appeared for anybody who had not run `gh auth
// login` — which is most people who would install a release binary.
func OptionalToken(ctx context.Context) string {
	t, err := Token(ctx)
	if err != nil {
		return ""
	}
	return t
}

// Token finds a GitHub token: the environment first, then the gh CLI's stored
// auth. For a caller that genuinely needs one; the update path uses
// OptionalToken.
func Token(ctx context.Context) (string, error) {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(k)); t != "" {
			return t, nil
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no GH_TOKEN set and gh auth token failed — run gh auth login")
	}
	if t := strings.TrimSpace(string(out)); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("gh auth token returned nothing — run gh auth login")
}

// Latest is the newest published release in repo ("" means DefaultRepo).
//
// Newest by the order GitHub returns, which is by creation date, and filtered
// to real releases: a draft is not published and a prerelease is not what
// somebody running `pgctl update` is asking for.
func Latest(ctx context.Context, token, repo string) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		apiBase+"/repos/"+repo+"/releases?per_page=30", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// A 404 is what GitHub says about a repository you cannot read, which
		// is indistinguishable from one that does not exist. The bare status
		// sends people looking for a network problem, so name the likely cause.
		if resp.StatusCode == http.StatusNotFound {
			hint := "no releases visible in " + repo
			if token == "" {
				hint += " (no GitHub credential in use — is it private?)"
			}
			if repo != DefaultRepo {
				hint += fmt.Sprintf("\n  the repo is set to %s; the default is the public %s",
					repo, DefaultRepo)
			}
			return nil, fmt.Errorf("%s", hint)
		}
		return nil, fmt.Errorf("listing releases: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var releases []struct {
		TagName    string  `json:"tag_name"`
		Draft      bool    `json:"draft"`
		Prerelease bool    `json:"prerelease"`
		Assets     []Asset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for _, r := range releases {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.TagName, TagPrefix) {
			continue
		}
		return &Release{Version: r.TagName, Assets: r.Assets}, nil
	}
	return nil, fmt.Errorf("no %s* release found in %s", TagPrefix, repo)
}

// Newer reports whether version a is newer than version b, both "vX.Y.Z".
// A version that does not parse is treated as older than anything.
func Newer(a, b string) bool {
	av, aok := parseSemver(a)
	bv, bok := parseSemver(b)
	if !aok {
		return false
	}
	if !bok {
		return true
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}

// Released reports whether a version string is a plain release version rather
// than a local build like "v0.2.0-9-g48155d1" or "dev".
//
// It exists because Newer treats an unparseable version as older, which is
// right for comparing two releases and wrong for deciding whether to replace
// the binary somebody is developing with. `make install` stamps a git describe,
// so an unguarded update happily overwrites a newer local build with the last
// release.
func Released(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, TagPrefix), ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// AssetName is the binary this platform needs, named the way release.yml names
// it. Changing one without the other is an update that reports an unsupported
// platform on the platform it was built for.
func AssetName() string {
	return fmt.Sprintf("pgctl-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func pickAsset(rel *Release, name string) (Asset, error) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no asset %q (unsupported platform?)", rel.Version, name)
}

// HomebrewManaged reports whether the running binary was installed by Homebrew,
// and where it lives.
//
// Updating in place works there — Apply replaces the file the PATH symlink
// points at — but it leaves Homebrew's own records untouched: `brew list
// --versions` keeps reporting what it installed, and the next `brew upgrade`
// overwrites the binary with whatever the formula pins. Worth saying out loud
// rather than leaving somebody to find it the confusing way. The tap and the
// release come from one script, so `brew upgrade` gets the identical binary.
func HomebrewManaged() (string, bool) {
	exe, err := executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, fromCellar(exe)
}

// fromCellar is split out so a test can check the path rule without installing
// anything. Homebrew keeps every version under <prefix>/Cellar/<formula>/<ver>,
// on the Intel prefix, the Apple Silicon one and Linuxbrew alike.
func fromCellar(path string) bool { return strings.Contains(path, "/Cellar/") }

// Apply downloads the release binary for this platform, checks it against the
// release's checksums.txt, and atomically replaces the running executable.
//
// The checksum is not optional here, and install.sh is the reason: it verifies,
// with a comment saying a truncated download is the failure that produces a
// binary which runs and then does something surprising. This path replaces the
// binary you are running, so it cannot be the laxer of the two.
func Apply(ctx context.Context, token string, rel *Release) error {
	name := AssetName()
	asset, err := pickAsset(rel, name)
	if err != nil {
		return err
	}

	exe, err := executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	want, err := checksum(ctx, token, rel, name)
	if err != nil {
		return err
	}

	dlCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	body, err := fetch(dlCtx, token, asset.URL)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", asset.Name, err)
	}
	defer func() { _ = body.Close() }()

	// Written next to the target, so the rename is on one filesystem and
	// therefore atomic: at no point is there a half-written pgctl on the PATH.
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".pgctl-update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, sum), body); err != nil {
		_ = tmp.Close()
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		_ = tmp.Close()
		return fmt.Errorf("%s does not match the release checksum — got %s, want %s. "+
			"Nothing was replaced", asset.Name, got[:12], want[:12])
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe)
}

// checksum is the expected sha256 of one asset, read from the release's
// checksums.txt.
func checksum(ctx context.Context, token string, rel *Release, name string) (string, error) {
	asset, err := pickAsset(rel, ChecksumsAsset)
	if err != nil {
		return "", fmt.Errorf("release %s publishes no %s, so the download cannot be verified",
			rel.Version, ChecksumsAsset)
	}
	body, err := fetch(ctx, token, asset.URL)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", ChecksumsAsset, err)
	}
	defer func() { _ = body.Close() }()

	scan := bufio.NewScanner(io.LimitReader(body, 1<<16))
	for scan.Scan() {
		// `shasum -a 256 *` writes "<hex>  <name>", two spaces, and a `*`
		// before the name in binary mode. Fields handles all of it.
		parts := strings.Fields(scan.Text())
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == name {
			return parts[0], nil
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s has no line for %s", ChecksumsAsset, name)
}

// fetch is one GET of a release asset, as bytes.
func fetch(ctx context.Context, token, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return resp.Body, nil
}
