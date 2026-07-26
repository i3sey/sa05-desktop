package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultFeedURL is the release feed. It is the public GitHub API, which needs no token —
// a client cannot carry a credential, so an update channel that requires one is no
// channel at all.
const DefaultFeedURL = "https://api.github.com/repos/i3sey/sa05-desktop/releases/latest"

const (
	feedTimeout     = 15 * time.Second
	downloadTimeout = 10 * time.Minute
	maxArchiveBytes = 200 << 20
	userAgent       = "SA05-Updater/1.0"
)

// errKeyInvalid means the build carries no usable verification key.
var errKeyInvalid = errors.New("ключ проверки обновлений повреждён")

// Asset is one downloadable file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is what the feed offers.
type Release struct {
	Tag       string  `json:"tag_name"`
	Name      string  `json:"name"`
	Notes     string  `json:"body"`
	Draft     bool    `json:"draft"`
	Prerelase bool    `json:"prerelease"`
	Assets    []Asset `json:"assets"`
}

// Available describes an update the client can install.
type Available struct {
	Version   Version
	Notes     string
	Archive   Asset
	Signature Asset
}

// Checker asks the feed what the latest release is.
type Checker struct {
	// FeedURL overrides DefaultFeedURL, so a fork or a private mirror can serve updates.
	FeedURL string
	HTTP    *http.Client
	// Current is the running version; an unparsable one disables updates rather than
	// installing something blindly.
	Current string
}

// Check returns the update to offer, or ok=false when the client is current.
func (c *Checker) Check(ctx context.Context) (Available, bool, error) {
	current, err := ParseVersion(c.Current)
	if err != nil {
		return Available{}, false, fmt.Errorf("текущая версия неизвестна: %w", err)
	}

	release, err := c.latest(ctx)
	if err != nil {
		return Available{}, false, err
	}
	if release.Draft {
		// A draft is not published; treating it as available would ship an unfinished
		// build to everyone who happens to check.
		return Available{}, false, nil
	}
	candidate, err := ParseVersion(release.Tag)
	if err != nil {
		return Available{}, false, fmt.Errorf("некорректный тег релиза %q", release.Tag)
	}
	if !candidate.NewerThan(current) {
		return Available{}, false, nil
	}

	archive, signature, err := selectAssets(release.Assets)
	if err != nil {
		return Available{}, false, err
	}
	return Available{
		Version:   candidate,
		Notes:     release.Notes,
		Archive:   archive,
		Signature: signature,
	}, true, nil
}

func (c *Checker) latest(ctx context.Context) (Release, error) {
	feed := c.FeedURL
	if feed == "" {
		feed = DefaultFeedURL
	}
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feed, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", userAgent)

	response, err := c.client().Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("список релизов недоступен: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return Release{}, errors.New("релизы не опубликованы")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Release{}, errors.New("канал обновлений недоступен без авторизации")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Release{}, fmt.Errorf("сервер обновлений ответил HTTP %d", response.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("ответ сервера обновлений не разобран: %w", err)
	}
	return release, nil
}

func (c *Checker) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: feedTimeout}
}

// assetSuffix is the archive this platform installs from.
func assetSuffix() string {
	if runtime.GOOS == "windows" {
		return "windows-" + runtime.GOARCH + ".zip"
	}
	return runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
}

// selectAssets picks the archive for this platform and its signature. A release without a
// signature is refused: an unsigned update is an unauthenticated code path.
func selectAssets(assets []Asset) (Asset, Asset, error) {
	suffix := assetSuffix()
	archive := Asset{}
	for _, asset := range assets {
		if strings.HasSuffix(asset.Name, suffix) {
			archive = asset
			break
		}
	}
	if archive.Name == "" {
		return Asset{}, Asset{}, fmt.Errorf("в релизе нет сборки для %s/%s",
			runtime.GOOS, runtime.GOARCH)
	}
	for _, asset := range assets {
		if asset.Name == archive.Name+".sig" {
			return archive, asset, nil
		}
	}
	return Asset{}, Asset{}, fmt.Errorf("релиз без подписи для %s — установка отменена",
		archive.Name)
}

// Installer downloads and installs a verified update.
type Installer struct {
	// PublicKey verifies the release signature. It is compiled in, so a compromised feed
	// cannot substitute its own key along with its own archive.
	PublicKey ed25519.PublicKey
	HTTP      *http.Client
	// TargetDir is where the binaries live; empty means the directory of the running
	// executable.
	TargetDir string
}

// Install downloads the update, verifies it and replaces the binaries. The client must be
// restarted afterwards: the running process keeps executing the old image.
func (i *Installer) Install(ctx context.Context, update Available) error {
	target, err := i.targetDir()
	if err != nil {
		return err
	}
	// A package-managed install is owned by root: writing into it behind the package
	// manager's back would break the next upgrade.
	if err := writable(target); err != nil {
		return fmt.Errorf(
			"каталог %s недоступен для записи — обновите клиент через пакетный менеджер", target)
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	work, err := os.MkdirTemp(target, ".sa05-update-*")
	if err != nil {
		return fmt.Errorf("временный каталог не создан: %w", err)
	}
	defer os.RemoveAll(work)

	archivePath := filepath.Join(work, update.Archive.Name)
	digest, err := i.download(ctx, update.Archive, archivePath)
	if err != nil {
		return err
	}
	signature, err := i.fetchSignature(ctx, update.Signature)
	if err != nil {
		return err
	}
	// The signature covers the digest, so a tampered archive fails here — before anything
	// is unpacked, let alone executed.
	if !ed25519.Verify(i.PublicKey, digest, signature) {
		return errors.New("подпись обновления не совпала — установка отменена")
	}

	unpacked := filepath.Join(work, "unpacked")
	if err := extract(archivePath, unpacked); err != nil {
		return err
	}
	return replaceBinaries(unpacked, target)
}

func (i *Installer) targetDir() (string, error) {
	if i.TargetDir != "" {
		return i.TargetDir, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("путь к программе не определён: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Dir(executable), nil
}

// download streams the asset to path and returns its SHA-256 digest.
func (i *Installer) download(ctx context.Context, asset Asset, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := i.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("обновление не скачано: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("сервер обновлений ответил HTTP %d", response.StatusCode)
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("файл обновления не создан: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxArchiveBytes))
	if err != nil {
		return nil, fmt.Errorf("обновление не скачано: %w", err)
	}
	if asset.Size > 0 && written != asset.Size {
		return nil, fmt.Errorf("размер обновления не совпал: %d вместо %d", written, asset.Size)
	}
	return hash.Sum(nil), nil
}

// fetchSignature reads the detached signature, accepting the base64 or hex forms so the
// release job can produce whichever is convenient.
func (i *Installer) fetchSignature(ctx context.Context, asset Asset) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)

	response, err := i.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("подпись не скачана: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("подпись недоступна: HTTP %d", response.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("подпись не прочитана: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil &&
		len(decoded) == ed25519.SignatureSize {
		return decoded, nil
	}
	decoded, err := hex.DecodeString(text)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return nil, errors.New("подпись обновления повреждена")
	}
	return decoded, nil
}

func (i *Installer) client() *http.Client {
	if i.HTTP != nil {
		return i.HTTP
	}
	return &http.Client{Timeout: downloadTimeout}
}

// writable reports whether the process may replace files in the directory.
func writable(directory string) error {
	probe, err := os.CreateTemp(directory, ".sa05-write-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}
