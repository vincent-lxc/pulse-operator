package payment

import (
	"fmt"
	"net/http"
	"time"
)

func localHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("local RPC redirects are forbidden") }}
}
