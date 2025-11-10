package main

import (
    "bufio"
    "context"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "os"
    "strings"
    "sync/atomic"
    "time"
)

// Simple in-process service to simulate a long-lived connection (SSE) and a restart command.
// It listens on 127.0.0.1:7888 and exposes:
// - GET /health: basic health check
// - GET /sse: server-sent events stream (3 events, 1s interval)
// - POST /restart: simulate a restart by resetting a ticker (in-memory)
//
// The test then performs requests via two clients:
// - Direct client (no proxy)
// - Proxied client (HTTP proxy at http://127.0.0.1:7879)
//
// Expected:
// - If system proxy is enabled but 127.0.0.1:7879 is not actually listening or doesn't support long connections, the proxied client will fail while direct succeeds.

var restartedCount atomic.Int64

func main() {
    addr := "127.0.0.1:7888"
    srv := &http.Server{Addr: addr}

    mux := http.NewServeMux()

    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        io.WriteString(w, `{"status":"ok"}`)
    })

    mux.HandleFunc("/restart", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        restartedCount.Add(1)
        w.Header().Set("Content-Type", "application/json")
        io.WriteString(w, fmt.Sprintf(`{"restarted":%d}`, restartedCount.Load()))
    })

    mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("Connection", "keep-alive")
        flusher, ok := w.(http.Flusher)
        if !ok {
            http.Error(w, "streaming unsupported", http.StatusInternalServerError)
            return
        }
        for i := 1; i <= 3; i++ {
            fmt.Fprintf(w, "data: tick %d at %s\n\n", i, time.Now().Format(time.RFC3339))
            flusher.Flush()
            time.Sleep(1 * time.Second)
        }
    })

    srv.Handler = mux

    // Start server in background
    go func() {
        log.Printf("[server] listening on http://%s\n", addr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("[server] ListenAndServe error: %v", err)
        }
    }()

    // Wait a moment for server to start
    time.Sleep(300 * time.Millisecond)

    // Build clients
    directClient := &http.Client{Timeout: 5 * time.Second}
    proxiedClient := &http.Client{Timeout: 5 * time.Second, Transport: proxyTransport("http://127.0.0.1:7879")}

    // Perform tests
    log.Println("[test] BEGIN proxy simulation against local service")
    testAll("DIRECT", directClient)
    testAll("PROXY 127.0.0.1:7879", proxiedClient)
    log.Println("[test] END proxy simulation")

    // Shutdown server gracefully
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
    defer cancel()
    _ = srv.Shutdown(ctx)
}

func proxyTransport(proxyURL string) *http.Transport {
    return &http.Transport{
        Proxy: func(req *http.Request) (*url.URL, error) {
            // Do not bypass localhost here to force proxy usage for the test
            if proxyURL == "" {
                return nil, nil
            }
            return url.Parse(proxyURL)
        },
    }
}

func testAll(label string, client *http.Client) {
    // /health
    if err := simpleGET(client, "http://127.0.0.1:7888/health", label, "/health"); err != nil {
        log.Printf("[result][%s] /health FAILED: %v\n", label, err)
    } else {
        log.Printf("[result][%s] /health OK\n", label)
    }

    // /restart (POST)
    if err := simplePOST(client, "http://127.0.0.1:7888/restart", label, "/restart"); err != nil {
        log.Printf("[result][%s] /restart FAILED: %v\n", label, err)
    } else {
        log.Printf("[result][%s] /restart OK\n", label)
    }

    // /sse (stream)
    if err := simpleSSE(client, "http://127.0.0.1:7888/sse", label, "/sse"); err != nil {
        log.Printf("[result][%s] /sse FAILED: %v\n", label, err)
    } else {
        log.Printf("[result][%s] /sse OK (received 3 events)\n", label)
    }
}

func simpleGET(client *http.Client, url string, label string, name string) error {
    req, err := http.NewRequest(http.MethodGet, url, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return classifyNetErr(err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("%s status=%d body=%s", name, resp.StatusCode, strings.TrimSpace(string(body)))
    }
    return nil
}

func simplePOST(client *http.Client, url string, label string, name string) error {
    req, err := http.NewRequest(http.MethodPost, url, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return classifyNetErr(err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("%s status=%d body=%s", name, resp.StatusCode, strings.TrimSpace(string(body)))
    }
    return nil
}

func simpleSSE(client *http.Client, urlStr string, label string, name string) error {
    req, err := http.NewRequest(http.MethodGet, urlStr, nil)
    if err != nil {
        return err
    }
    resp, err := client.Do(req)
    if err != nil {
        return classifyNetErr(err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("%s status=%d body=%s", name, resp.StatusCode, strings.TrimSpace(string(body)))
    }
    // Read 3 events quickly
    reader := bufio.NewReader(resp.Body)
    events := 0
    deadline := time.Now().Add(5 * time.Second)
    for events < 3 && time.Now().Before(deadline) {
        line, err := reader.ReadString('\n')
        if err != nil {
            if err == io.EOF {
                break
            }
            return classifyNetErr(err)
        }
        if strings.HasPrefix(line, "data:") {
            events++
        }
    }
    if events < 3 {
        return fmt.Errorf("%s expected 3 events, got %d", name, events)
    }
    return nil
}

func classifyNetErr(err error) error {
    // Hint common proxy / TLS / connect errors
    msg := err.Error()
    switch {
    case strings.Contains(msg, "connectex") || strings.Contains(msg, "connect: "):
        return fmt.Errorf("connect error: %v", err)
    case strings.Contains(strings.ToLower(msg), "proxy"):
        return fmt.Errorf("proxy error: %v", err)
    case strings.Contains(strings.ToLower(msg), "tls") || strings.Contains(strings.ToLower(msg), "certificate"):
        return fmt.Errorf("tls/cert error: %v", err)
    default:
        return err
    }
}

// Optional: allow setting env proxies for external verification
func init() {
    if os.Getenv("HTTP_PROXY") != "" || os.Getenv("HTTPS_PROXY") != "" {
        log.Printf("[env] HTTP_PROXY=%s HTTPS_PROXY=%s NO_PROXY=%s\n", os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"), os.Getenv("NO_PROXY"))
    }
}