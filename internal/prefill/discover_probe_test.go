package prefill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDiscoverProbesEveryCommonFeedPath(t *testing.T) {
	for index, path := range commonFeedPaths {
		t.Run(path, func(t *testing.T) {
			var visited []string
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				visited = append(visited, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/blog":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<html><head><title>Blog</title></head><body>No feed links</body></html>"))
				case path:
					w.Header().Set("Content-Type", "application/rss+xml")
					_, _ = w.Write([]byte(rss))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			d, err := Discover(context.Background(), srv.URL+"/blog", true)
			if err != nil {
				t.Fatal(err)
			}
			if !d.Probed || len(d.Feeds) != 1 || d.Feeds[0].URL != srv.URL+path || d.Feeds[0].Items != 2 {
				t.Fatalf("feed at %s was not discovered: %+v", path, d)
			}
			want := append([]string{"/blog"}, commonFeedPaths[:index+1]...)
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(visited, want) {
				t.Fatalf("probes = %v, want %v", visited, want)
			}
		})
	}
}

// An unsuccessful discovery exhausts only the fixed candidate list.
func TestDiscoverProbeRequestBound(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("<html>no feeds</html>"))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	d, err := Discover(context.Background(), srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Feeds) != 0 || d.Probed || requests.Load() != int32(1+len(commonFeedPaths)) {
		t.Fatalf("discovery = %+v, requests = %d", d, requests.Load())
	}
}

func TestDiscoverProbeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("<html>no feeds</html>"))
		} else {
			cancel()
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, _ = Discover(ctx, srv.URL, true)
	if requests.Load() != 2 {
		t.Fatalf("requests after cancellation = %d, want 2", requests.Load())
	}
}

func TestDiscoverProbeRejectsPrivateRedirect(t *testing.T) {
	var privateRequests atomic.Int32
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Hostname() == "127.0.0.1" {
			privateRequests.Add(1)
		}
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("<html>no feeds</html>"))
		} else {
			http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
		}
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	old := ProxyFor
	defer func() { ProxyFor = old }()
	ProxyFor = func(string) *url.URL { return proxyURL }
	d, err := Discover(context.Background(), "http://1.1.1.1/", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Feeds) != 0 || privateRequests.Load() != 0 || requests.Load() != int32(1+len(commonFeedPaths)) {
		t.Fatalf("discovery = %+v, private requests = %d, total requests = %d", d, privateRequests.Load(), requests.Load())
	}
}
