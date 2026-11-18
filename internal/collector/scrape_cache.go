package collector

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"

	"golang.org/x/sync/singleflight"
)

// maxCachedBody bounds what one scrape keeps. The resources several collectors read are small,
// and a larger body is read once anyway.
const maxCachedBody = 256 << 10

// scrapeCache answers a repeat GET of a URL with the first response, so collectors that read
// the same resource cost the BMC one request. It wraps the client made for one scrape, so
// nothing it keeps outlives the scrape.
type scrapeCache struct {
	next      http.RoundTripper
	inflight  singleflight.Group
	mu        sync.Mutex
	responses map[string]*cachedResponse
}

type cachedResponse struct {
	status int
	header http.Header
	body   []byte
}

func newScrapeCache(next http.RoundTripper) *scrapeCache {
	return &scrapeCache{next: next, responses: map[string]*cachedResponse{}}
}

func (s *scrapeCache) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		return s.next.RoundTrip(r)
	}
	key := r.URL.String()
	s.mu.Lock()
	cached, ok := s.responses[key]
	s.mu.Unlock()
	if ok {
		return cached.response(r), nil
	}
	// Concurrent requests for the same URL share one round trip.
	shared, err, _ := s.inflight.Do(key, func() (any, error) {
		resp, err := s.next.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() // nolint:errcheck // Close() errors on read are not actionable
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		got := &cachedResponse{status: resp.StatusCode, header: resp.Header.Clone(), body: body}
		if resp.StatusCode == http.StatusOK && len(body) <= maxCachedBody {
			s.mu.Lock()
			s.responses[key] = got
			s.mu.Unlock()
		}
		return got, nil
	})
	if err != nil {
		return nil, err
	}
	return shared.(*cachedResponse).response(r), nil
}

func (c *cachedResponse) response(r *http.Request) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", c.status, http.StatusText(c.status)),
		StatusCode:    c.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        c.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(c.body)),
		ContentLength: int64(len(c.body)),
		Request:       r,
	}
}
