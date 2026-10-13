package fetchweb

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// DoWithRetry performs an HTTP request with at most two retries for 429/5xx.
// Every attempt remains subject to ctx and the client timeout. Requests with
// bodies must provide GetBody so they can be replayed. The caller must close
// the returned response body.
func DoWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return nil, fmt.Errorf("retryable request body requires GetBody")
	}
	for attempt := 0; ; attempt++ {
		copy := req.Clone(ctx)
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			copy.Body = body
		}
		resp, err := client.Do(copy)
		if err != nil {
			return nil, err
		}
		if attempt == 2 || (resp.StatusCode != 429 && resp.StatusCode < 500) {
			return resp, nil
		}
		delay := time.Duration(1<<attempt) * time.Second
		if seconds, err := time.ParseDuration(resp.Header.Get("Retry-After") + "s"); err == nil && seconds > delay {
			delay = min(seconds, 10*time.Second)
		}
		resp.Body.Close()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
