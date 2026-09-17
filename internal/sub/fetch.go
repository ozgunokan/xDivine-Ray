// Package sub fetches subscription URLs and turns them into profiles.
package sub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/uri"
)

// DefaultUserAgent is sent with subscription requests. Many providers serve a
// different (or empty) list to unknown clients, so a recognizable client string
// is more reliable than Go's default.
const DefaultUserAgent = "xwrt/1.0 (OpenWrt)"

// Fetcher retrieves subscription bodies over HTTP.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
}

// NewFetcher returns a fetcher with sane timeouts for an embedded device.
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		UserAgent: DefaultUserAgent,
	}
}

// Result is the outcome of refreshing one subscription.
type Result struct {
	Profiles []model.Profile
	Skipped  int
	Warnings []string
}

// Fetch downloads and decodes a subscription.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("subscription url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription returned HTTP %d", resp.StatusCode)
	}
	// Cap the body: a subscription is a text list, and an unbounded read on a
	// router with 64 MB of RAM is a real failure mode.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return Decode(string(body))
}

// Decode turns a subscription body into profiles. The body is either a plain
// list of share links or that same list wrapped in base64.
func Decode(body string) (*Result, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return nil, errors.New("subscription body is empty")
	}
	if !looksLikeLinkList(text) {
		decoded, err := decodeBase64Body(text)
		if err != nil {
			return nil, fmt.Errorf("body is neither a link list nor base64: %w", err)
		}
		text = decoded
	}

	profiles, errs := uri.ParseMany(text)
	res := &Result{Profiles: profiles, Skipped: len(errs)}
	for i, e := range errs {
		if i >= 5 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("... and %d more", len(errs)-5))
			break
		}
		res.Warnings = append(res.Warnings, e.Error())
	}
	if len(profiles) == 0 {
		return res, errors.New("no usable servers in subscription")
	}
	return res, nil
}

func looksLikeLinkList(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, scheme := range []string{"vless://", "vmess://", "trojan://", "ss://"} {
			if strings.HasPrefix(line, scheme) {
				return true
			}
		}
		// Only the first non-empty line is inspected: a base64 blob never
		// starts with a scheme.
		return false
	}
	return false
}

func decodeBase64Body(s string) (string, error) {
	// Subscription bodies are often wrapped at fixed column widths.
	compact := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(s)
	p, err := uri.DecodeBase64(compact)
	if err != nil {
		return "", err
	}
	return p, nil
}
