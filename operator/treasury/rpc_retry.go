// 本文件在 Arc 公共 RPC 返回 429 或 5xx 时按退避重试。
package treasury

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

type retryTransport struct {
	base http.RoundTripper
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		clone := req.Clone(req.Context())
		if body != nil {
			clone.Body = io.NopCloser(bytes.NewReader(body))
			clone.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
			clone.ContentLength = int64(len(body))
		}
		res, err := base.RoundTrip(clone)
		if err != nil {
			last = err
		} else if res.StatusCode != http.StatusTooManyRequests && res.StatusCode < 500 {
			return res, nil
		} else {
			last = fmt.Errorf("rpc http %d", res.StatusCode)
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		delay := time.Duration(200*(1<<attempt)) * time.Millisecond
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("rpc retries exhausted: %w", last)
}
