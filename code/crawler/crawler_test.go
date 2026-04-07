package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testTransport struct {
	responses map[string]testResponse
}

type testResponse struct {
	status        int
	body          string
	headers       http.Header
	contentLength int64
	setLength     bool
}

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()
	resp, ok := t.responses[key]
	if !ok {
		return nil, fmt.Errorf("no mock for %s", key)
	}

	h := http.Header{}
	for k, vals := range resp.headers {
		for _, v := range vals {
			h.Add(k, v)
		}
	}

	cl := int64(len(resp.body))
	if resp.setLength {
		cl = resp.contentLength
	}

	return &http.Response{
		StatusCode:    resp.status,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(resp.body)),
		ContentLength: cl,
		Request:       req,
	}, nil
}

func TestAnalyzeBasicSuccess(t *testing.T) {
	html := `<html><head><title>Test</title></head><body><h1>Hello</h1></body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {
				status: 200,
				body:   html,
				headers: http.Header{
					"Content-Type": []string{"text/html"},
				},
			},
		},
	}

	client := &http.Client{Transport: mock}

	data, err := Analyze(context.Background(), Options{
		URL:         "https://test.com",
		Depth:       1,
		Timeout:     time.Second,
		Concurrency: 1,
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("bad json: %v", err)
	}

	if report.RootURL != "https://test.com" {
		t.Errorf("wrong root_url: %s", report.RootURL)
	}
	if len(report.Pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(report.Pages))
	}

	page := report.Pages[0]
	if page.HTTPStatus != 200 {
		t.Errorf("expected status 200, got %d", page.HTTPStatus)
	}
	if !page.SEO.HasTitle || page.SEO.Title != "Test" {
		t.Errorf("wrong seo title: %+v", page.SEO)
	}
	if !page.SEO.HasH1 {
		t.Error("expected has_h1 = true")
	}
}

func TestAnalyzeNetworkError(t *testing.T) {
	mock := testTransport{
		responses: map[string]testResponse{}, // пустой - все запросы упадут
	}

	client := &http.Client{Transport: mock}

	data, err := Analyze(context.Background(), Options{
		URL:         "https://broken.test",
		Depth:       1,
		Retries:     0,
		Timeout:     time.Second,
		Concurrency: 1,
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("bad json: %v", err)
	}

	if len(report.Pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(report.Pages))
	}
	if report.Pages[0].Status != "error" {
		t.Errorf("expected status 'error', got '%s'", report.Pages[0].Status)
	}
}

func TestAnalyze404Status(t *testing.T) {
	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {
				status: 404,
				body:   "not found",
			},
		},
	}

	client := &http.Client{Transport: mock}
	data, err := Analyze(context.Background(), Options{
		URL:         "https://test.com",
		Depth:       1,
		Retries:     0,
		Timeout:     time.Second,
		Concurrency: 1,
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report Report
	json.Unmarshal(data, &report)

	page := report.Pages[0]
	if page.HTTPStatus != 404 {
		t.Errorf("expected 404, got %d", page.HTTPStatus)
	}
	if page.Status != "error" {
		t.Errorf("expected 'error' status, got '%s'", page.Status)
	}
}

func TestAnalyze500Status(t *testing.T) {
	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {
				status: 500,
				body:   "server error",
			},
		},
	}

	client := &http.Client{Transport: mock}
	data, err := Analyze(context.Background(), Options{
		URL:         "https://test.com",
		Depth:       1,
		Retries:     0,
		Timeout:     time.Second,
		Concurrency: 1,
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var report Report
	json.Unmarshal(data, &report)
	if report.Pages[0].HTTPStatus != 500 {
		t.Errorf("expected 500, got %d", report.Pages[0].HTTPStatus)
	}
}

func TestAnalyzeTimeout(t *testing.T) {
	// контекст с очень коротким таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// даем контексту истечь
	time.Sleep(5 * time.Millisecond)

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: "<html></html>"},
		},
	}
	client := &http.Client{Transport: mock}

	data, err := Analyze(ctx, Options{
		URL:         "https://test.com",
		Depth:       1,
		Concurrency: 1,
		HTTPClient:  client,
	})

	// при отмененном контексте должен вернуть отчет или ошибку
	if err != nil {
		// ошибка контекста - ок
		return
	}
	if data != nil {
		var report Report
		json.Unmarshal(data, &report)
		// отчет может быть пустой или с ошибкой - оба варианта ок
	}
}

func TestAnalyzeBrokenLinks(t *testing.T) {
	html := `<html><body>
		<a href="https://test.com/ok">good</a>
		<a href="https://test.com/bad">broken</a>
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {
				status:  200,
				body:    html,
				headers: http.Header{"Content-Type": []string{"text/html"}},
			},
			"HEAD https://test.com/ok": {
				status: 200,
			},
			"HEAD https://test.com/bad": {
				status: 404,
			},
		},
	}

	client := &http.Client{Transport: mock}

	data, err := Analyze(context.Background(), Options{
		URL:         "https://test.com",
		Depth:       1,
		Retries:     0,
		Timeout:     time.Second,
		Concurrency: 1,
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var report Report
	json.Unmarshal(data, &report)

	page := report.Pages[0]
	if len(page.BrokenLinks) != 1 {
		t.Fatalf("expected 1 broken link, got %d", len(page.BrokenLinks))
	}
	if page.BrokenLinks[0].StatusCode != 404 {
		t.Errorf("expected 404, got %d", page.BrokenLinks[0].StatusCode)
	}
}
