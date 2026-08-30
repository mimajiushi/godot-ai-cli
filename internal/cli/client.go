package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// daemonURL builds the loopback URL for one daemon endpoint.
func daemonURL(httpPort int, path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", httpPort, path)
}

// apiClient talks to the local daemon. There is deliberately NO client-level
// Timeout: http.Client.Timeout caps the whole exchange and would override the
// longer per-request contexts slow operations need (a full test run takes
// minutes). Every call site must set its own context deadline instead.
var apiClient = &http.Client{}

// quickRequestTimeout bounds the short control-plane calls (status, session
// list, activate, custom-tools) that used to rely on the client Timeout.
const quickRequestTimeout = 5 * time.Second

// getDaemonJSON GETs a daemon endpoint and decodes the JSON body.
func getDaemonJSON(httpPort int, path string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), quickRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL(httpPort, path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeBody(resp)
}

// postDaemonJSON POSTs a JSON body to a daemon endpoint with a custom
// per-request timeout and decodes the JSON response.
func postDaemonJSON(httpPort int, path string, body any, timeout time.Duration) (map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daemonURL(httpPort, path), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return decodeBody(resp)
}

// decodeBody reads and JSON-decodes an HTTP response body.
func decodeBody(resp *http.Response) (map[string]any, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		// A garbage/huge body (wrong server on the port, proxy page, ...)
		// must not flood the CLI's own error output — cap the echo.
		preview := string(raw)
		if len(preview) > 512 {
			preview = preview[:512] + "…(truncated)"
		}
		return nil, fmt.Errorf("decode daemon response %q: %w", preview, err)
	}
	return out, nil
}

// daemonReachable reports whether a daemon answers its health endpoint.
func daemonReachable(httpPort int) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(daemonURL(httpPort, "/godot-ai/cli/health"))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
