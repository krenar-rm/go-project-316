package crawler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type fetchResult struct {
	statusCode    int
	body          []byte
	contentLength int64
	header        http.Header
}

type fetcher struct {
	client    *http.Client
	retries   int
	delay     time.Duration
	userAgent string

	mu       sync.Mutex
	lastCall time.Time
}

func newFetcher(client *http.Client, retries int, delay time.Duration, ua string) *fetcher {
	if client == nil {
		client = &http.Client{}
	}
	agent := "hexlet-go-crawler/1.0"
	if ua != "" {
		agent = ua
	}
	return &fetcher{
		client:    client,
		retries:   retries,
		delay:     delay,
		userAgent: agent,
	}
}

func (f *fetcher) doFetch(ctx context.Context, method, rawURL string) (*fetchResult, error) {
	if method == "" {
		method = http.MethodGet
	}

	var lastErr error
	var lastResult *fetchResult
	attempts := f.retries + 1

	for i := 0; i < attempts; i++ {
		if i > 0 {
			// пауза между ретраями
			wait := time.Duration(i) * 200 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		if err := f.throttle(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", f.userAgent)

		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := readBody(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		result := &fetchResult{
			statusCode:    resp.StatusCode,
			body:          body,
			contentLength: resp.ContentLength,
			header:        resp.Header.Clone(),
		}

		// ретраим только временные ошибки (429, 5xx)
		if shouldRetry(resp.StatusCode) && i < attempts-1 {
			lastErr = fmt.Errorf("http %d", resp.StatusCode)
			lastResult = result
			continue
		}

		return result, nil
	}

	// если последний ответ был 429/5xx, вернем его (а не ошибку)
	if lastResult != nil {
		return lastResult, nil
	}
	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return nil, lastErr
}

func (f *fetcher) throttle(ctx context.Context) error {
	if f.delay <= 0 {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	wait := f.delay - now.Sub(f.lastCall)
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	f.lastCall = time.Now()
	return nil
}

func shouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func readBody(r io.ReadCloser) (data []byte, err error) {
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
