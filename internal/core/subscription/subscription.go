// Package subscription fetches and caches the provider subscription.
//
// The subscription is one HTTPS URL returning a JSON array of complete Xray configs.
// A failed refresh never replaces the last valid cached profile list, and the client
// never rewrites a profile's JSON.
//
// Ported from the Android client's SubscriptionRepository.kt / SubscriptionAuth.kt /
// SubscriptionDeepLink.kt: profile IDs, header handling and validation rules match, so
// both clients agree on what a valid subscription is.
package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fife/sa05-desktop/internal/core/xrayconf"
)

const (
	maxResponseBytes = 2 * 1024 * 1024
	requestTimeout   = 15 * time.Second
	userAgent        = "SA05-Xray/1.0"
)

// Profile is one selectable server configuration.
type Profile struct {
	ID      string `json:"id"`
	Remarks string `json:"remarks"`
	JSON    string `json:"json"`
}

// State is the cached subscription, persisted verbatim.
type State struct {
	URL                 string    `json:"url"`
	Title               string    `json:"title"`
	Profiles            []Profile `json:"profiles"`
	ActiveProfileID     string    `json:"activeProfileId"`
	UpdatedAt           int64     `json:"updatedAt"`
	ETag                string    `json:"etag"`
	UserInfo            string    `json:"userInfo"`
	UpdateIntervalHours int       `json:"updateIntervalHours"`
}

// ActiveProfile returns the selected profile, falling back to the first one so a
// stale selection never blocks a connection.
func (s State) ActiveProfile() *Profile {
	for index := range s.Profiles {
		if s.Profiles[index].ID == s.ActiveProfileID {
			return &s.Profiles[index]
		}
	}
	if len(s.Profiles) > 0 {
		return &s.Profiles[0]
	}
	return nil
}

// Authorized reports whether the client may connect: exactly one successful import of
// a subscription with at least one valid profile is required.
func (s State) Authorized() bool {
	return strings.TrimSpace(s.URL) != "" && len(s.Profiles) > 0
}

// RefreshIntervalHours is the provider-requested refresh cadence, or 0 when periodic
// refresh must stay off.
func (s State) RefreshIntervalHours() int {
	if s.UpdateIntervalHours <= 0 {
		return 0
	}
	if !strings.HasPrefix(s.URL, "https://") || len(s.Profiles) == 0 {
		return 0
	}
	return s.UpdateIntervalHours
}

// Result reports whether the server returned a new document or confirmed the cache.
type Result struct {
	State       State
	NotModified bool
}

// Client fetches subscriptions over HTTPS.
type Client struct {
	HTTP *http.Client
	// Now is injectable so tests can pin updatedAt.
	Now func() time.Time
}

// NewClient builds a client with the shared timeout and no redirect surprises.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{Timeout: requestTimeout},
		Now:  time.Now,
	}
}

// Update fetches inputURL and merges the response with previous, preserving the active
// profile where possible. previous is returned untouched on any failure.
func (c *Client) Update(ctx context.Context, inputURL string, previous State) (Result, error) {
	normalized, err := NormalizeURL(inputURL)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return Result{}, fmt.Errorf("Некорректная ссылка подписки: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if previous.URL == normalized && strings.TrimSpace(previous.ETag) != "" {
		request.Header.Set("If-None-Match", previous.ETag)
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("Подписка недоступна: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotModified &&
		previous.URL == normalized && len(previous.Profiles) > 0 {
		refreshed := previous
		refreshed.UpdatedAt = c.now().UnixMilli()
		return Result{State: refreshed, NotModified: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Result{}, fmt.Errorf("Сервер подписки вернул HTTP %d", response.StatusCode)
	}

	body, err := readLimited(response.Body, maxResponseBytes)
	if err != nil {
		return Result{}, err
	}
	profiles, err := ParseProfiles(string(body))
	if err != nil {
		return Result{}, err
	}

	next := State{
		URL:                 normalized,
		Title:               decodeHeaderValue(response.Header.Get("profile-title")),
		Profiles:            profiles,
		ActiveProfileID:     carryActiveProfile(previous, profiles, normalized),
		UpdatedAt:           c.now().UnixMilli(),
		ETag:                response.Header.Get("ETag"),
		UserInfo:            response.Header.Get("subscription-userinfo"),
		UpdateIntervalHours: parseInterval(response.Header.Get("profile-update-interval")),
	}
	return Result{State: next}, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: requestTimeout}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ParseProfiles validates every entry of the subscription document. One invalid
// profile rejects the whole response, matching the Android contract.
func ParseProfiles(body string) ([]Profile, error) {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var array []json.RawMessage
	if err := decoder.Decode(&array); err != nil {
		return nil, fmt.Errorf("Ответ не является JSON-массивом: %w", err)
	}
	if len(array) == 0 {
		return nil, errors.New("Подписка не содержит профилей")
	}
	profiles := make([]Profile, 0, len(array))
	for index, entry := range array {
		var object map[string]any
		objectDecoder := json.NewDecoder(strings.NewReader(string(entry)))
		objectDecoder.UseNumber()
		if err := objectDecoder.Decode(&object); err != nil {
			return nil, fmt.Errorf("Профиль %d не является JSON-объектом", index+1)
		}
		raw, err := reindent(object)
		if err != nil {
			return nil, fmt.Errorf("Профиль %d: %w", index+1, err)
		}
		if _, err := xrayconf.Validate(raw); err != nil {
			return nil, fmt.Errorf("Профиль %d: %w", index+1, err)
		}
		remarks, _ := object["remarks"].(string)
		if strings.TrimSpace(remarks) == "" {
			remarks = fmt.Sprintf("Профиль %d", index+1)
		}
		profiles = append(profiles, Profile{
			ID:      StableID(raw),
			Remarks: remarks,
			JSON:    raw,
		})
	}
	return profiles, nil
}

// StableID is the profile identity: the first 12 bytes of the SHA-256 of its JSON.
func StableID(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:12])
}

// NormalizeURL enforces the HTTPS-only contract for subscription links.
func NormalizeURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil {
		return "", errors.New("Некорректная ссылка подписки")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", errors.New("Подписка должна использовать HTTPS")
	}
	if parsed.Host == "" {
		return "", errors.New("Некорректная ссылка подписки")
	}
	return value, nil
}

// ParseDeepLink extracts the subscription URL from sa05://add/<percent-encoded-https-url>.
// Anything else returns an empty string, so an invalid link never replaces a valid cache.
func ParseDeepLink(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	outer, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if !strings.EqualFold(outer.Scheme, "sa05") || !strings.EqualFold(outer.Host, "add") {
		return ""
	}
	encoded := strings.TrimPrefix(outer.EscapedPath(), "/")
	if encoded == "" {
		return ""
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil {
		return ""
	}
	normalized, err := NormalizeURL(decoded)
	if err != nil {
		return ""
	}
	return normalized
}

var whitespacePattern = regexp.MustCompile(`\s+`)

// ServerRemark splits a provider remark into a display name and its flag emoji.
type ServerRemark struct {
	Name string
	Flag string
}

// ParseServerRemark pulls the leading regional-indicator pair out of the remark so the
// UI can show the flag separately from the server name.
func ParseServerRemark(raw string) ServerRemark {
	runes := []rune(raw)
	flagIndex := -1
	for index := 0; index+1 < len(runes); index++ {
		if isRegionalIndicator(runes[index]) && isRegionalIndicator(runes[index+1]) {
			flagIndex = index
			break
		}
	}
	if flagIndex < 0 {
		return ServerRemark{Name: strings.TrimSpace(raw)}
	}
	flag := string(runes[flagIndex : flagIndex+2])
	name := string(runes[:flagIndex]) + string(runes[flagIndex+2:])
	name = whitespacePattern.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "|·•-—–")
	return ServerRemark{Name: strings.TrimSpace(name), Flag: flag}
}

func isRegionalIndicator(value rune) bool {
	return value >= 0x1F1E6 && value <= 0x1F1FF
}

func carryActiveProfile(previous State, profiles []Profile, normalized string) string {
	if previous.URL != normalized {
		return profiles[0].ID
	}
	for _, profile := range profiles {
		if profile.ID == previous.ActiveProfileID {
			return profile.ID
		}
	}
	if active := previous.ActiveProfile(); active != nil {
		for _, profile := range profiles {
			if profile.Remarks == active.Remarks {
				return profile.ID
			}
		}
	}
	return profiles[0].ID
}

func parseInterval(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

// decodeHeaderValue accepts both plain and "base64:"-prefixed header values, the two
// forms subscription providers use for profile-title.
func decodeHeaderValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	_, encoded, found := strings.Cut(value, "base64:")
	if !found || strings.TrimSpace(encoded) == "" {
		return value
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return ""
		}
	}
	return string(decoded)
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("Подписка не прочитана: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, errors.New("Подписка больше 2 МБ")
	}
	return body, nil
}

func reindent(object map[string]any) (string, error) {
	encoded, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return "", fmt.Errorf("JSON не сериализован: %w", err)
	}
	return string(encoded), nil
}
