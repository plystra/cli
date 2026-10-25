package projectstage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/plystra/cli/internal/gocommand"
)

type fileProxy struct {
	server *http.Server
	mu     sync.Mutex
}

// StartFileProxy captures demand-read payloads from one ordinary file GOPROXY
// before serving them to Go when GOTOOLCHAIN=local. Other configurations remain
// unchanged, including Go's special file-proxy toolchain verification rules.
// It does not isolate GOMODCACHE or cover cache hits and direct/VCS access.
// The bool reports whether the effective-environment Go process started.
func (s *Stage) StartFileProxy(ctx context.Context, command string) (bool, error) {
	output, started, err := gocommand.OutputObserved(ctx, gocommand.Options{
		Command: command, Directory: s.Root(), Environment: s.Environment(), OutputLimit: maximumMetadataBytes,
	}, "env", "-json", "GOPROXY", "GOTOOLCHAIN", "GOMODCACHE")
	if err != nil {
		return started, err
	}
	var settings struct{ GOPROXY, GOTOOLCHAIN, GOMODCACHE string }
	if json.Unmarshal(output, &settings) != nil {
		return started, ErrStage
	}
	selected, err := url.Parse(settings.GOPROXY)
	if err != nil || settings.GOTOOLCHAIN != "local" || selected.Scheme != "file" || strings.ContainsAny(settings.GOPROXY, ",|") ||
		selected.Host != "" && selected.Host != "localhost" || selected.User != nil || selected.RawQuery != "" || selected.Fragment != "" {
		return started, nil
	}
	path, err := selectedPath(selected)
	if err != nil {
		return started, nil
	}
	if !filepath.IsAbs(settings.GOMODCACHE) {
		return started, ErrStage
	}
	defer s.lock()()
	if err := s.recheck(ctx); err != nil {
		return started, err
	}
	if s.proxy != nil || s.proxyStopped {
		return started, ErrStage
	}
	// Go's sole-file-proxy exception also covers explicitly requested
	// toolchain modules, including distributions already in the consuming cache.
	toolchains := []string{filepath.Join(path, "golang.org", "toolchain"),
		filepath.Join(settings.GOMODCACHE, "cache", "download", "golang.org", "toolchain")}
	for _, toolchain := range toolchains {
		if _, err := os.Lstat(toolchain); !errors.Is(err, os.ErrNotExist) {
			return started, nil
		}
	}
	for _, toolchain := range toolchains {
		s.paths = append(s.paths, pathSnapshot{path: toolchain})
	}
	s.inputs = s.captureInputs()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return started, ErrStage
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		listener.Close()
		return started, ErrStage
	}
	prefix := "/" + hex.EncodeToString(secret[:]) + "/"
	proxy := &fileProxy{}
	proxy.server = &http.Server{
		ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, prefix) || r.URL.RawQuery != "" {
				http.NotFound(w, r)
				return
			}
			request := strings.TrimPrefix(r.URL.Path, prefix)
			if !fs.ValidPath(request) || strings.ContainsAny(request, "\\:\x00") || !filepath.IsLocal(filepath.FromSlash(request)) {
				http.NotFound(w, r)
				return
			}
			source := filepath.Join(path, filepath.FromSlash(request))
			data, err := s.retainProxyFile(r.Context(), source, request)
			if errors.Is(err, errProxyNotFound) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, "dependency acquisition rejected", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
		}),
	}
	s.proxy = proxy
	s.environment = replaceEnvironment(s.environment, "GOPROXY", "http://"+listener.Addr().String()+prefix)
	go func() { _ = proxy.server.Serve(listener) }()
	return started, nil
}

// StopFileProxy drains acquisition handlers and returns any sticky capture
// failure even if a Go command did not expose that failure itself.
func (s *Stage) StopFileProxy() error {
	err := s.stopFileProxy()
	if s == nil {
		return err
	}
	defer s.lock()()
	return errors.Join(err, s.proxyErr)
}

func (s *Stage) stopFileProxy() error {
	if s == nil {
		return nil
	}
	unlock := s.lock()
	s.proxyStopped = true
	proxy := s.proxy
	unlock()
	if proxy != nil {
		return proxy.close()
	}
	return nil
}

func selectedPath(selected *url.URL) (string, error) {
	path := filepath.FromSlash(selected.Path)
	if filepath.Separator == '\\' && len(path) > 2 && path[0] == '\\' && path[2] == ':' {
		path = path[1:]
	}
	if !filepath.IsAbs(path) {
		return "", ErrStage
	}
	return filepath.Clean(path), nil
}

func (p *fileProxy) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if p.server.Shutdown(ctx) != nil {
		_ = p.server.Close()
		return ErrStage
	}
	return nil
}
