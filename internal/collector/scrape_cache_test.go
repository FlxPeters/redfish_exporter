package collector

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newCountingServer answers /ok with a small body, /slow the same after a pause, /missing
// with 404 and /big with a body larger than maxCachedBody, counting the requests per path.
func newCountingServer(t *testing.T) (*httptest.Server, map[string]*atomic.Int32) {
	t.Helper()
	hits := map[string]*atomic.Int32{}
	for _, p := range []string{"/ok", "/slow", "/missing", "/big"} {
		hits[p] = &atomic.Int32{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path].Add(1)
		switch r.URL.Path {
		case "/slow":
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(w, `{"Id":"slow"}`)
		case "/missing":
			http.NotFound(w, r)
		case "/big":
			_, _ = io.WriteString(w, strings.Repeat("x", maxCachedBody+1))
		default:
			_, _ = io.WriteString(w, `{"Id":"ok"}`)
		}
	}))
	t.Cleanup(server.Close)
	return server, hits
}

func get(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close() // nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestScrapeCache_RepeatGETHitsTheBMCOnce(t *testing.T) {
	server, hits := newCountingServer(t)
	client := &http.Client{Transport: newScrapeCache(http.DefaultTransport)}

	for range 3 {
		status, body := get(t, client, server.URL+"/ok")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, `{"Id":"ok"}`, body)
	}
	require.EqualValues(t, 1, hits["/ok"].Load())
}

func TestScrapeCache_ConcurrentGETsShareOneRequest(t *testing.T) {
	server, hits := newCountingServer(t)
	client := &http.Client{Transport: newScrapeCache(http.DefaultTransport)}

	bodies := make([]string, 8)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() {
			resp, err := client.Get(server.URL + "/slow")
			if err != nil {
				return
			}
			defer resp.Body.Close() // nolint:errcheck
			body, _ := io.ReadAll(resp.Body)
			bodies[i] = string(body)
		})
	}
	wg.Wait()
	for _, body := range bodies {
		require.Equal(t, `{"Id":"slow"}`, body)
	}
	require.EqualValues(t, 1, hits["/slow"].Load())
}

func TestScrapeCache_KeepsOnlySmallSuccesses(t *testing.T) {
	server, hits := newCountingServer(t)
	client := &http.Client{Transport: newScrapeCache(http.DefaultTransport)}

	for range 2 {
		status, _ := get(t, client, server.URL+"/missing")
		require.Equal(t, http.StatusNotFound, status)
		_, body := get(t, client, server.URL+"/big")
		require.Len(t, body, maxCachedBody+1)
	}
	require.EqualValues(t, 2, hits["/missing"].Load(), "an error is asked again")
	require.EqualValues(t, 2, hits["/big"].Load(), "a large body is not kept")

	resp, err := client.Post(server.URL+"/ok", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	get(t, client, server.URL+"/ok")
	require.EqualValues(t, 2, hits["/ok"].Load(), "a POST is never answered from the cache")
}
