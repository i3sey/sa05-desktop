package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionOrdering(t *testing.T) {
	cases := []struct {
		left, right string
		newer       bool
	}{
		{"v1.2.4", "v1.2.3", true},
		{"v1.3.0", "v1.2.9", true},
		{"v2.0.0", "v1.9.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.4", false},
		// A release supersedes its own candidates, and a candidate never supersedes it.
		{"v1.0.0", "v1.0.0-rc1", true},
		{"v1.0.0-rc1", "v1.0.0", false},
		{"v1.0.0-rc2", "v1.0.0-rc1", true},
	}
	for _, item := range cases {
		left, err := ParseVersion(item.left)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", item.left, err)
		}
		right, err := ParseVersion(item.right)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", item.right, err)
		}
		if left.NewerThan(right) != item.newer {
			t.Fatalf("%s новее %s? получили %v", item.left, item.right, !item.newer)
		}
	}
}

func TestParseVersionRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "latest", "v", "1.2.x", "v1.2.3.4", "-1.0.0"} {
		if _, err := ParseVersion(raw); err == nil {
			t.Fatalf("принята версия %q", raw)
		}
	}
}

// releaseEntries is what a release archive holds: the binaries plus files the updater
// must ignore.
func releaseEntries(content string) map[string]string {
	return map[string]string{
		"sa05-v9.9.9-linux-amd64/sa05":        content,
		"sa05-v9.9.9-linux-amd64/sa05ctl":     content + "-ctl",
		"sa05-v9.9.9-linux-amd64/README.md":   "документация",
		"sa05-v9.9.9-linux-amd64/sa05.exe":    content,
		"sa05-v9.9.9-linux-amd64/sa05ctl.exe": content + "-ctl",
	}
}

// archiveWithBinaries builds a release archive in the format this platform publishes:
// zip on Windows, tar.gz elsewhere. Using the wrong one would test a code path the
// client never takes.
func archiveWithBinaries(t *testing.T, content string) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return zipArchive(t, content)
	}
	return tarArchive(t, content)
}

func zipArchive(t *testing.T, content string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for name, body := range releaseEntries(content) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buffer.Bytes()
}

func tarArchive(t *testing.T, content string) []byte {
	t.Helper()
	buffer := &strings.Builder{}
	gzipWriter := gzip.NewWriter(newStringWriter(buffer))
	writer := tar.NewWriter(gzipWriter)

	for name, body := range releaseEntries(content) {
		if err := writer.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return []byte(buffer.String())
}

type stringWriter struct{ builder *strings.Builder }

func newStringWriter(builder *strings.Builder) *stringWriter { return &stringWriter{builder} }

func (w *stringWriter) Write(data []byte) (int, error) { return w.builder.Write(data) }

// releaseServer serves a feed plus its assets, signing the archive with the given key.
func releaseServer(t *testing.T, tag string, archive []byte, key ed25519.PrivateKey, corrupt bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	digest := sha256.Sum256(archive)
	signature := ed25519.Sign(key, digest[:])
	if corrupt {
		signature[0] ^= 0xff
	}

	suffix := assetSuffix()
	archiveName := fmt.Sprintf("sa05-%s-%s", tag, suffix)

	mux.HandleFunc("/feed", func(writer http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(writer).Encode(Release{
			Tag:   tag,
			Notes: "что нового",
			Assets: []Asset{
				{Name: archiveName, URL: server.URL + "/archive", Size: int64(len(archive))},
				{Name: archiveName + ".sig", URL: server.URL + "/signature"},
			},
		})
	})
	mux.HandleFunc("/archive", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Write(archive)
	})
	mux.HandleFunc("/signature", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, base64.StdEncoding.EncodeToString(signature))
	})
	return server
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return public, private
}

func TestCheckOffersNewerRelease(t *testing.T) {
	_, private := newKey(t)
	server := releaseServer(t, "v9.9.9", archiveWithBinaries(t, "новый клиент"), private, false)

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	update, ok, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok {
		t.Fatal("новая версия не предложена")
	}
	if update.Version.String() != "v9.9.9" || update.Signature.Name == "" {
		t.Fatalf("обновление: %+v", update)
	}
}

func TestCheckIgnoresSameOrOlderRelease(t *testing.T) {
	_, private := newKey(t)
	server := releaseServer(t, "v1.0.0", archiveWithBinaries(t, "клиент"), private, false)

	for _, current := range []string{"v1.0.0", "v1.1.0"} {
		checker := &Checker{FeedURL: server.URL + "/feed", Current: current}
		_, ok, err := checker.Check(context.Background())
		if err != nil {
			t.Fatalf("Check(%s): %v", current, err)
		}
		if ok {
			t.Fatalf("при версии %s предложено обновление на v1.0.0", current)
		}
	}
}

func TestCheckRefusesReleaseWithoutSignature(t *testing.T) {
	// An unsigned release is an unauthenticated code path; refusing beats installing.
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/feed", func(writer http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(writer).Encode(Release{
			Tag: "v9.9.9",
			Assets: []Asset{
				{Name: "sa05-v9.9.9-" + assetSuffix(), URL: server.URL + "/archive"},
			},
		})
	})

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	if _, _, err := checker.Check(context.Background()); err == nil {
		t.Fatal("релиз без подписи принят")
	}
}

func TestCheckSkipsDraft(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/feed", func(writer http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(writer).Encode(Release{Tag: "v9.9.9", Draft: true})
	})

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	_, ok, err := checker.Check(context.Background())
	if err != nil || ok {
		t.Fatalf("черновик предложен как обновление: ok=%v err=%v", ok, err)
	}
}

func TestInstallReplacesBinaries(t *testing.T) {
	public, private := newKey(t)
	server := releaseServer(t, "v9.9.9", archiveWithBinaries(t, "новый клиент"), private, false)

	target := t.TempDir()
	binary := filepath.Join(target, executableName("sa05"))
	if err := os.WriteFile(binary, []byte("старый клиент"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	update, ok, err := checker.Check(context.Background())
	if err != nil || !ok {
		t.Fatalf("Check: ok=%v err=%v", ok, err)
	}

	installer := &Installer{PublicKey: public, TargetDir: target}
	if err := installer.Install(context.Background(), update); err != nil {
		t.Fatalf("Install: %v", err)
	}

	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "новый клиент" {
		t.Fatalf("бинарь не заменён: %q", content)
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("бинарь не исполняемый: %v", info.Mode())
	}
	// The temporary work directory must not survive the install.
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sa05-update-") {
			t.Fatalf("остался временный каталог %s", entry.Name())
		}
	}
}

func TestInstallRejectsTamperedArchive(t *testing.T) {
	public, private := newKey(t)
	// The signature is broken on purpose: this is the case the whole mechanism exists for.
	server := releaseServer(t, "v9.9.9", archiveWithBinaries(t, "подменённый клиент"), private, true)

	target := t.TempDir()
	binary := filepath.Join(target, executableName("sa05"))
	if err := os.WriteFile(binary, []byte("старый клиент"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	update, ok, err := checker.Check(context.Background())
	if err != nil || !ok {
		t.Fatalf("Check: ok=%v err=%v", ok, err)
	}
	installer := &Installer{PublicKey: public, TargetDir: target}
	if err := installer.Install(context.Background(), update); err == nil {
		t.Fatal("обновление с неверной подписью установлено")
	}
	content, _ := os.ReadFile(binary)
	if string(content) != "старый клиент" {
		t.Fatalf("бинарь заменён несмотря на неверную подпись: %q", content)
	}
}

func TestInstallRejectsSignatureFromAnotherKey(t *testing.T) {
	public, _ := newKey(t)
	_, attacker := newKey(t)
	// Correct signature, wrong signer: whoever controls the feed cannot bring their own key.
	server := releaseServer(t, "v9.9.9", archiveWithBinaries(t, "чужой клиент"), attacker, false)

	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, executableName("sa05")),
		[]byte("старый клиент"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	checker := &Checker{FeedURL: server.URL + "/feed", Current: "v0.1.0"}
	update, _, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	installer := &Installer{PublicKey: public, TargetDir: target}
	if err := installer.Install(context.Background(), update); err == nil {
		t.Fatal("принята подпись чужим ключом")
	}
}

func TestExtractIgnoresEverythingButBinaries(t *testing.T) {
	// Path traversal and stray files are handled by the same rule: only known base names
	// are written, so nothing can land outside the destination.
	buffer := &strings.Builder{}
	gzipWriter := gzip.NewWriter(newStringWriter(buffer))
	writer := tar.NewWriter(gzipWriter)
	for _, name := range []string{
		"../../etc/passwd",
		"release/sa05",
		"release/evil.sh",
		"/absolute/sa05ctl",
	} {
		body := "данные"
		writer.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		writer.Write([]byte(body))
	}
	writer.Close()
	gzipWriter.Close()

	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(archive, []byte(buffer.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	destination := t.TempDir()
	if err := extract(archive, destination); err != nil {
		t.Fatalf("extract: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	for _, name := range names {
		if name != executableName("sa05") && name != executableName("sa05ctl") {
			t.Fatalf("распакован лишний файл %q (всего: %v)", name, names)
		}
	}
}

func TestPublicKeyIsUsable(t *testing.T) {
	key, err := PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if len(key) != ed25519.PublicKeySize {
		t.Fatalf("длина ключа %d", len(key))
	}
}
