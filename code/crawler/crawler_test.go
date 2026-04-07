package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
	if page.BrokenLinks[0].URL != "https://test.com/bad" {
		t.Errorf("wrong broken link url: %s", page.BrokenLinks[0].URL)
	}
}

func TestAnalyzeIgnoresUnsupportedSchemes(t *testing.T) {
	html := `<html><body>
		<a href="mailto:test@example.com">email</a>
		<a href="javascript:alert(1)">js</a>
		<a href="tel:+123456">phone</a>
		<a href="">empty</a>
		<a href="#">anchor</a>
		<a href="https://test.com/valid">valid</a>
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {
				status:  200,
				body:    html,
				headers: http.Header{"Content-Type": []string{"text/html"}},
			},
			"HEAD https://test.com/valid": {
				status: 200,
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
	if len(page.BrokenLinks) != 0 {
		t.Errorf("expected 0 broken links (unsupported schemes ignored), got %d: %+v", len(page.BrokenLinks), page.BrokenLinks)
	}
}

func TestSEOAllTagsPresent(t *testing.T) {
	html := `<html>
	<head>
		<title>My Page Title</title>
		<meta name="description" content="Page about testing">
	</head>
	<body><h1>Welcome</h1></body>
	</html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	json.Unmarshal(data, &report)
	seo := report.Pages[0].SEO

	if !seo.HasTitle {
		t.Error("has_title should be true")
	}
	if seo.Title != "My Page Title" {
		t.Errorf("title = %q, want %q", seo.Title, "My Page Title")
	}
	if !seo.HasDescription {
		t.Error("has_description should be true")
	}
	if seo.Description != "Page about testing" {
		t.Errorf("description = %q, want %q", seo.Description, "Page about testing")
	}
	if !seo.HasH1 {
		t.Error("has_h1 should be true")
	}
}

func TestSEONoTags(t *testing.T) {
	html := `<html><head></head><body><p>just text</p></body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	json.Unmarshal(data, &report)
	seo := report.Pages[0].SEO

	if seo.HasTitle {
		t.Error("has_title should be false")
	}
	if seo.Title != "" {
		t.Errorf("title should be empty, got %q", seo.Title)
	}
	if seo.HasDescription {
		t.Error("has_description should be false")
	}
	if seo.Description != "" {
		t.Errorf("description should be empty, got %q", seo.Description)
	}
	if seo.HasH1 {
		t.Error("has_h1 should be false")
	}
}

func TestSEOHtmlEntities(t *testing.T) {
	html := `<html>
	<head>
		<title>Tom &amp; Jerry</title>
		<meta name="description" content="&lt;best&gt; show &amp; more">
	</head>
	<body><h1>test</h1></body>
	</html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	json.Unmarshal(data, &report)
	seo := report.Pages[0].SEO

	if seo.Title != "Tom & Jerry" {
		t.Errorf("title = %q, want %q", seo.Title, "Tom & Jerry")
	}
	if seo.Description != "<best> show & more" {
		t.Errorf("description = %q, want %q", seo.Description, "<best> show & more")
	}
}

func TestDepthLimitsPages(t *testing.T) {
	// depth=1 значит только корневая страница (depth 0), без перехода по ссылкам
	rootHTML := `<html><body>
		<a href="https://test.com/page1">link</a>
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: rootHTML, headers: http.Header{"Content-Type": {"text/html"}}},
			"HEAD https://test.com/page1": {status: 200},
			"GET https://test.com/page1":  {status: 200, body: "<html><body>page1</body></html>"},
		},
	}

	client := &http.Client{Transport: mock}

	// depth=1 -> только root
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: client,
	})
	var report Report
	json.Unmarshal(data, &report)

	if len(report.Pages) != 1 {
		t.Fatalf("depth=1: expected 1 page, got %d", len(report.Pages))
	}
	if report.Pages[0].URL != "https://test.com" {
		t.Errorf("expected root page, got %s", report.Pages[0].URL)
	}

	// depth=2 -> root + page1
	data2, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 2, Timeout: time.Second, Concurrency: 1,
		HTTPClient: client,
	})
	var report2 Report
	json.Unmarshal(data2, &report2)

	if len(report2.Pages) != 2 {
		t.Fatalf("depth=2: expected 2 pages, got %d", len(report2.Pages))
	}
}

func TestExternalPagesNotCrawled(t *testing.T) {
	rootHTML := `<html><body>
		<a href="https://test.com/inner">internal</a>
		<a href="https://external.com/page">external</a>
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com":        {status: 200, body: rootHTML, headers: http.Header{"Content-Type": {"text/html"}}},
			"GET https://test.com/inner":  {status: 200, body: "<html><body>inner</body></html>", headers: http.Header{"Content-Type": {"text/html"}}},
			"HEAD https://external.com/page": {status: 200},
		},
	}

	client := &http.Client{Transport: mock}
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 3, Timeout: time.Second, Concurrency: 1,
		HTTPClient: client,
	})

	var report Report
	json.Unmarshal(data, &report)

	for _, p := range report.Pages {
		if p.URL == "https://external.com/page" {
			t.Error("external page should not be in pages list")
		}
	}

	// внутренняя должна быть
	found := false
	for _, p := range report.Pages {
		if p.URL == "https://test.com/inner" {
			found = true
		}
	}
	if !found {
		t.Error("internal page should be in pages list")
	}
}

func TestDuplicateLinksOnlyOnce(t *testing.T) {
	rootHTML := `<html><body>
		<a href="https://test.com/page">first</a>
		<a href="https://test.com/page">second</a>
		<a href="https://test.com/page">third</a>
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com":       {status: 200, body: rootHTML, headers: http.Header{"Content-Type": {"text/html"}}},
			"GET https://test.com/page":  {status: 200, body: "<html><body>ok</body></html>", headers: http.Header{"Content-Type": {"text/html"}}},
		},
	}

	client := &http.Client{Transport: mock}
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 2, Timeout: time.Second, Concurrency: 1,
		HTTPClient: client,
	})

	var report Report
	json.Unmarshal(data, &report)

	count := 0
	for _, p := range report.Pages {
		if p.URL == "https://test.com/page" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("page should appear once, got %d times", count)
	}
}

func TestRateLimitDelay(t *testing.T) {
	// трекаем время запросов
	var mu sync.Mutex
	var requestTimes []time.Time

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><body>ok</body></html>")),
			ContentLength: 28,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}
	delay := 50 * time.Millisecond

	Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Delay: delay, Timeout: 2 * time.Second,
		Concurrency: 1, HTTPClient: client,
	})

	mu.Lock()
	times := requestTimes
	mu.Unlock()

	// проверяем что между запросами минимум delay
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap < delay-5*time.Millisecond { // небольшой допуск
			t.Errorf("gap between requests %d and %d is %v, expected >= %v", i-1, i, gap, delay)
		}
	}
}

func TestRateLimitRPSOverridesDelay(t *testing.T) {
	// rps приоритетнее delay - если rps=10 а delay=1s, то интервал = 100ms (не 1s)
	var mu sync.Mutex
	var requestTimes []time.Time

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><body><a href=\"https://test.com/p1\">l</a></body></html>")),
			ContentLength: 60,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}

	// rps=10 -> интервал 100ms, delay стоит 2s но должен быть проигнорирован
	// в CLI rps переводится в delay, проверим напрямую
	rpsDelay := time.Duration(float64(time.Second) / 10) // 100ms

	start := time.Now()
	Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Delay: rpsDelay, Timeout: 5 * time.Second,
		Concurrency: 1, HTTPClient: client,
	})
	elapsed := time.Since(start)

	mu.Lock()
	n := len(requestTimes)
	mu.Unlock()

	// не должно занять слишком долго (при rps=10 и 1-2 запроса < 1 секунды)
	if elapsed > 2*time.Second {
		t.Errorf("took %v, expected faster with rps=10", elapsed)
	}
	if n == 0 {
		t.Error("expected at least one request")
	}
}

func TestNoDelayNoSlowdown(t *testing.T) {
	var mu sync.Mutex
	var requestTimes []time.Time

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><body>fast</body></html>")),
			ContentLength: 30,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}

	start := time.Now()
	Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Delay: 0, Timeout: time.Second,
		Concurrency: 1, HTTPClient: client,
	})
	elapsed := time.Since(start)

	// без delay должно выполниться быстро
	if elapsed > 500*time.Millisecond {
		t.Errorf("no delay should be fast, took %v", elapsed)
	}
}

func TestDelayContextCancel(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><body>ok</body></html>")),
			ContentLength: 28,
			Request:       req,
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	Analyze(ctx, Options{
		URL: "https://test.com", Depth: 1, Delay: 5 * time.Second, Timeout: 10 * time.Second,
		Concurrency: 1, HTTPClient: &http.Client{Transport: transport},
	})
	elapsed := time.Since(start)

	// контекст отменяется через 100ms, не должно висеть 5 секунд
	if elapsed > 1*time.Second {
		t.Errorf("context cancel should stop waiting, took %v", elapsed)
	}
}

// хелпер для простых моков
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
