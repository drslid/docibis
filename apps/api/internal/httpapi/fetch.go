package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Images given by their address in the data, as Docmosis allows, are read
// only from the domains of the setting image_url_allowlist (and their
// sub-domains), at most 10 MB each, and kept for image_cache_minutes.

const (
	maxFetchedImage = 10 << 20
	imageCacheBytes = 64 << 20
)

type imageCache struct {
	mu    sync.Mutex
	items map[string]cachedImage
	size  int
}

type cachedImage struct {
	data []byte
	at   time.Time
}

func (c *imageCache) get(key string, ttl time.Duration) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok || time.Since(item.at) > ttl {
		return nil, false
	}
	return item.data, true
}

func (c *imageCache) put(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]cachedImage{}
	}
	for c.size+len(data) > imageCacheBytes && len(c.items) > 0 {
		oldest := ""
		for k, item := range c.items {
			if oldest == "" || item.at.Before(c.items[oldest].at) || time.Since(item.at) > ttl {
				oldest = k
			}
		}
		c.size -= len(c.items[oldest].data)
		delete(c.items, oldest)
	}
	if old, ok := c.items[key]; ok {
		c.size -= len(old.data)
	}
	c.items[key] = cachedImage{data: data, at: time.Now()}
	c.size += len(data)
}

// refuseInternal keeps the image reader off the addresses of the instance's
// own network: link-local (the metadata of the ECS task and of the host),
// loopback, private and shared ranges, checked at connection time so that a
// name cannot point there later (DNS rebinding).
func refuseInternal(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsLoopback() && !loopbackAllowed || ip.IsPrivate() || sharedRange.Contains(ip) {
		return fmt.Errorf("address %s refused", host)
	}
	return nil
}

// loopbackAllowed lets the tests serve images from this machine.
var loopbackAllowed = false

// sharedRange is the carrier-grade NAT range (RFC 6598), private in effect.
var sharedRange = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

var imageTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, Control: refuseInternal}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 10 * time.Second,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       time.Minute,
}

// allowedHosts reads the setting: one domain per line, written as
// cdn.example.com, *.example.com or https://cdn.example.com/.
func allowedHosts(setting string) []string {
	var hosts []string
	for _, line := range strings.Fields(setting) {
		line = strings.ToLower(line)
		if u, err := url.Parse(line); err == nil && u.Host != "" {
			line = u.Hostname()
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "*"), ".")
		if line = strings.TrimSuffix(line, "/"); line != "" {
			hosts = append(hosts, line)
		}
	}
	return hosts
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// imageFetcher returns what reads the images given by address for one
// document; nil when no domain is allowed (such images are left out).
func (s *Server) imageFetcher(ctx context.Context) func(string) ([]byte, error) {
	cfg := s.cfg.Load()
	allowed := allowedHosts(cfg.String("image_url_allowlist"))
	if len(allowed) == 0 {
		return nil
	}
	ttl := time.Duration(cfg.Int("image_cache_minutes")) * time.Minute
	client := &http.Client{
		Transport: imageTransport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 3 || !hostAllowed(r.URL.Hostname(), allowed) || (r.URL.Scheme != "https" && r.URL.Scheme != "http") {
				return errors.New("redirect refused")
			}
			return nil
		},
	}
	return func(address string) ([]byte, error) {
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errors.New("not an image address")
		}
		if !hostAllowed(u.Hostname(), allowed) {
			s.logger.Warn("image address refused: domain not allowed", "request_id", requestID(ctx), "host", u.Hostname())
			return nil, fmt.Errorf("domain %s not allowed", u.Hostname())
		}
		if ttl > 0 {
			if data, ok := s.images.get(address, ttl); ok {
				return data, nil
			}
		}
		data, err := fetchImage(ctx, client, address)
		if err != nil {
			s.logger.Warn("image not read", "request_id", requestID(ctx), "host", u.Hostname(), "error", err)
			return nil, err
		}
		if ttl > 0 {
			s.images.put(address, data, ttl)
		}
		return data, nil
	}
}

func fetchImage(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", "DocIbis")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchedImage+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFetchedImage {
		return nil, errors.New("image over 10 MB")
	}
	kind := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(kind, "image/") {
		kind = http.DetectContentType(data)
	}
	if !strings.HasPrefix(kind, "image/") {
		return nil, fmt.Errorf("not an image (%s)", kind)
	}
	return data, nil
}
