package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Version comparison is numeric, and a build that is not a release never wins.
func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.2.0", false},
		{"v0.1.0", "v0.1.0", false},
		// Numeric, not lexicographic. This is the whole reason for parsing.
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.2.0", "dev", true},
		// A dev build never "updates over" a release: it is usually newer.
		{"dev", "v0.1.0", false},
		{"v0.2.0-9-gabc1234", "v0.1.0", false},
		{"garbage", "dev", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Released is the guard that stops an update replacing a newer local build with
// the last release, which Newer cannot express on its own.
func TestReleasedSeparatesAReleaseFromALocalBuild(t *testing.T) {
	for _, v := range []string{"v0.1.0", "v1.2.3", "0.1.0"} {
		if !Released(v) {
			t.Errorf("Released(%q) = false, want true", v)
		}
	}
	// What `make install` stamps, and what an unstamped build says.
	for _, v := range []string{"dev", "v0.2.0-9-gabc1234", "", "v0.1"} {
		if Released(v) {
			t.Errorf("Released(%q) = true — an update would overwrite a local build", v)
		}
	}
}

// Latest skips what is not a release: a draft is unpublished, a prerelease is
// not what `pgctl update` is asking for, and a tag without the prefix is not a
// version at all.
func TestLatestPicksTheNewestRealRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; !strings.HasSuffix(got, "/repos/"+DefaultRepo+"/releases") {
			t.Errorf("asked for %q, want the default repo's releases", got)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "docs/landscape"},
			{"tag_name": "v0.3.0", "draft": true},
			{"tag_name": "v0.3.0-rc1", "prerelease": true},
			{"tag_name": "v0.2.0", "assets": []map[string]string{
				{"name": "pgctl-darwin-arm64", "url": "http://example/asset"},
			}},
			{"tag_name": "v0.1.0"},
		})
	}))
	defer srv.Close()
	defer withAPI(srv.URL)()

	rel, err := Latest(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v0.2.0" {
		t.Errorf("Latest = %q, want v0.2.0", rel.Version)
	}
	if len(rel.Assets) != 1 {
		t.Errorf("assets = %v, want the one the release carries", rel.Assets)
	}
}

// No token is not an error. A public tool's update notice must not depend on
// being logged in to GitHub.
func TestLatestWorksWithoutACredential(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") != ""
		_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v0.1.0"}})
	}))
	defer srv.Close()
	defer withAPI(srv.URL)()

	if _, err := Latest(t.Context(), "", ""); err != nil {
		t.Fatalf("unauthenticated listing: %v", err)
	}
	if sawAuth {
		t.Error("an Authorization header was sent with no token")
	}
}

// A 404 is what GitHub says about a repository you cannot read, so the message
// names the likely cause instead of leaving somebody looking for a network
// fault.
func TestAMissingRepoSaysWhatIsProbablyWrong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer withAPI(srv.URL)()

	_, err := Latest(t.Context(), "", "someone/private-fork")
	if err == nil {
		t.Fatal("a 404 was not an error")
	}
	for _, want := range []string{"someone/private-fork", "is it private", DefaultRepo} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, does not mention %q", err, want)
		}
	}
}

// Apply verifies the download before it replaces anything.
//
// install.sh checks the checksum and says why: a truncated download is the
// failure that produces a binary which runs and then does something
// surprising. This path replaces the binary you are running, so it cannot be
// the laxer of the two.
func TestApplyRefusesABinaryThatDoesNotMatchItsChecksum(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pgctl")
	if err := os.WriteFile(exe, []byte("the binary in place"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv, rel := releaseServing(t, []byte("a truncated download"), sha256.Sum256([]byte("the whole thing")))
	defer srv.Close()

	err := applyTo(t, exe, rel)
	if err == nil {
		t.Fatal("a mismatched download was installed")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want it to name the checksum", err)
	}
	// And the binary in place is untouched, which is the property that matters.
	if got, _ := os.ReadFile(exe); string(got) != "the binary in place" {
		t.Errorf("the executable was replaced anyway: %q", got)
	}
}

// The happy path: verified, and the replacement is atomic — the temp file is
// written beside the target so the rename cannot leave half a pgctl on the
// PATH.
func TestApplyReplacesTheBinaryInPlace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pgctl")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := []byte("the new binary")
	srv, rel := releaseServing(t, body, sha256.Sum256(body))
	defer srv.Close()

	if err := applyTo(t, exe, rel); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the new binary" {
		t.Errorf("executable = %q, want the downloaded one", got)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 — an unexecutable pgctl is a broken install", info.Mode().Perm())
	}
	// Nothing left behind next to it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("%d files in the directory, want only pgctl", len(entries))
	}
}

// A release with no checksums.txt cannot be verified, and that is a refusal
// rather than a silent unverified install.
func TestApplyRefusesAReleaseWithNoChecksums(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pgctl")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := &Release{Version: "v0.2.0", Assets: []Asset{
		{Name: AssetName(), URL: "http://example/asset"},
	}}
	err := applyTo(t, exe, rel)
	if err == nil || !strings.Contains(err.Error(), ChecksumsAsset) {
		t.Errorf("err = %v, want it to name the missing %s", err, ChecksumsAsset)
	}
}

// The asset name is a contract with release.yml. Changing one without the other
// is an update that reports an unsupported platform on the platform it was
// built for.
func TestTheAssetNameMatchesWhatTheWorkflowPublishes(t *testing.T) {
	name := AssetName()
	if !strings.HasPrefix(name, "pgctl-") || strings.Count(name, "-") != 2 {
		t.Errorf("AssetName = %q, want pgctl-<os>-<arch>", name)
	}
	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skipf("no workflow to check against: %v", err)
	}
	// The workflow builds `dist/pgctl-$os-$arch`; the shape is what matters,
	// since the loop's values are substituted at run time.
	if !strings.Contains(string(workflow), "dist/pgctl-$os-$arch") {
		t.Errorf("release.yml no longer publishes dist/pgctl-$os-$arch, so %q is wrong", name)
	}
}

// Homebrew keeps every version under <prefix>/Cellar, on all three prefixes.
func TestHomebrewIsRecognisedByItsCellar(t *testing.T) {
	for _, path := range []string{
		"/opt/homebrew/Cellar/pgctl/0.2.0/bin/pgctl",
		"/usr/local/Cellar/pgctl/0.2.0/bin/pgctl",
		"/home/linuxbrew/.linuxbrew/Cellar/pgctl/0.2.0/bin/pgctl",
	} {
		if !fromCellar(path) {
			t.Errorf("fromCellar(%q) = false", path)
		}
	}
	for _, path := range []string{"/Users/x/.local/bin/pgctl", "/usr/local/bin/pgctl"} {
		if fromCellar(path) {
			t.Errorf("fromCellar(%q) = true", path)
		}
	}
}

// withAPI points the package at a test server and returns the undo.
func withAPI(url string) func() {
	old := apiBase
	apiBase = url
	return func() { apiBase = old }
}

// releaseServing is a server handing out one binary and a checksums.txt naming
// the sum it is told to name — which is how a mismatch is arranged.
func releaseServing(t *testing.T, body []byte, sum [32]byte) (*httptest.Server, *Release) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			_, _ = w.Write(body)
		case "/checksums":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), AssetName())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &Release{Version: "v0.2.0", Assets: []Asset{
		{Name: AssetName(), URL: srv.URL + "/binary"},
		{Name: ChecksumsAsset, URL: srv.URL + "/checksums"},
	}}
}

// applyTo runs Apply against a specific path, which is what os.Executable
// would return in a real install.
func applyTo(t *testing.T, exe string, rel *Release) error {
	t.Helper()
	old := executable
	executable = func() (string, error) { return exe, nil }
	defer func() { executable = old }()
	return Apply(t.Context(), "", rel)
}
