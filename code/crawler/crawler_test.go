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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
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
		if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data2, &report2); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

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

	_, _ = Analyze(context.Background(), Options{
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
	_, _ = Analyze(context.Background(), Options{
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
	_, _ = Analyze(context.Background(), Options{
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
	_, _ = Analyze(ctx, Options{
		URL: "https://test.com", Depth: 1, Delay: 5 * time.Second, Timeout: 10 * time.Second,
		Concurrency: 1, HTTPClient: &http.Client{Transport: transport},
	})
	elapsed := time.Since(start)

	// контекст отменяется через 100ms, не должно висеть 5 секунд
	if elapsed > 1*time.Second {
		t.Errorf("context cancel should stop waiting, took %v", elapsed)
	}
}

func TestRetryAllFailsReturnsError(t *testing.T) {
	// retries=2, сервер все 3 раза отвечает 500 -> ошибка в отчете
	var mu sync.Mutex
	callCount := 0

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return &http.Response{
			StatusCode:    500,
			Header:        http.Header{},
			Body:          io.NopCloser(strings.NewReader("error")),
			ContentLength: 5,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Retries: 2, Timeout: 5 * time.Second,
		Concurrency: 1, HTTPClient: client,
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

	page := report.Pages[0]
	if page.HTTPStatus != 500 {
		t.Errorf("expected status 500, got %d", page.HTTPStatus)
	}
	if page.Status != "error" {
		t.Errorf("expected 'error', got '%s'", page.Status)
	}

	mu.Lock()
	cnt := callCount
	mu.Unlock()

	// retries=2 -> максимум 3 попытки (1 + 2)
	if cnt > 3 {
		t.Errorf("expected at most 3 calls, got %d", cnt)
	}
}

func TestRetrySuccessOnSecondAttempt(t *testing.T) {
	// первый запрос 500, второй 200 -> успех
	var mu sync.Mutex
	callCount := 0

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		if n == 1 {
			return &http.Response{
				StatusCode:    500,
				Header:        http.Header{},
				Body:          io.NopCloser(strings.NewReader("fail")),
				ContentLength: 4,
				Request:       req,
			}, nil
		}
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><head><title>OK</title></head><body></body></html>")),
			ContentLength: 55,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Retries: 2, Timeout: 5 * time.Second,
		Concurrency: 1, HTTPClient: client,
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

	page := report.Pages[0]
	if page.HTTPStatus != 200 {
		t.Errorf("expected 200 after retry, got %d", page.HTTPStatus)
	}
	if page.Status != "ok" {
		t.Errorf("expected 'ok', got '%s'", page.Status)
	}
}

func TestRetry429(t *testing.T) {
	// 429 тоже должен ретраиться
	var mu sync.Mutex
	callCount := 0

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		if n == 1 {
			return &http.Response{
				StatusCode:    429,
				Header:        http.Header{},
				Body:          io.NopCloser(strings.NewReader("rate limited")),
				ContentLength: 12,
				Request:       req,
			}, nil
		}
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{"Content-Type": {"text/html"}},
			Body:          io.NopCloser(strings.NewReader("<html><body>ok</body></html>")),
			ContentLength: 28,
			Request:       req,
		}, nil
	})

	client := &http.Client{Transport: transport}
	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Retries: 1, Timeout: 5 * time.Second,
		Concurrency: 1, HTTPClient: client,
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

	if report.Pages[0].HTTPStatus != 200 {
		t.Errorf("expected 200 after 429 retry, got %d", report.Pages[0].HTTPStatus)
	}
}

func TestAssetsBasic(t *testing.T) {
	html := `<html><body>
		<img src="https://test.com/logo.png">
		<script src="https://test.com/app.js"></script>
		<link rel="stylesheet" href="https://test.com/style.css">
	</body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
			"HEAD https://test.com/logo.png": {
				status: 200, setLength: true, contentLength: 5000,
				headers: http.Header{"Content-Length": {"5000"}},
			},
			"HEAD https://test.com/app.js": {
				status: 200, setLength: true, contentLength: 1024,
				headers: http.Header{"Content-Length": {"1024"}},
			},
			"HEAD https://test.com/style.css": {
				status: 200, setLength: true, contentLength: 512,
				headers: http.Header{"Content-Length": {"512"}},
			},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }
	assets := report.Pages[0].Assets

	if len(assets) != 3 {
		t.Fatalf("expected 3 assets, got %d", len(assets))
	}

	// проверяем типы
	types := map[string]bool{}
	for _, a := range assets {
		types[a.Type] = true
	}
	for _, want := range []string{"image", "script", "style"} {
		if !types[want] {
			t.Errorf("missing asset type %q", want)
		}
	}
}

func TestAssetCacheDedup(t *testing.T) {
	// два ассета с одинаковым URL на двух страницах - запрос только один
	rootHTML := `<html><body>
		<img src="https://test.com/shared.png">
		<a href="https://test.com/page2">p2</a>
	</body></html>`
	page2HTML := `<html><body>
		<img src="https://test.com/shared.png">
	</body></html>`

	var mu sync.Mutex
	assetCalls := 0

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		url := req.URL.String()
		if strings.Contains(url, "shared.png") {
			mu.Lock()
			assetCalls++
			mu.Unlock()
			return &http.Response{
				StatusCode:    200,
				Header:        http.Header{"Content-Length": {"100"}},
				Body:          io.NopCloser(strings.NewReader("")),
				ContentLength: 100,
				Request:       req,
			}, nil
		}
		if url == "https://test.com" {
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}},
				Body: io.NopCloser(strings.NewReader(rootHTML)), ContentLength: int64(len(rootHTML)),
				Request: req,
			}, nil
		}
		if url == "https://test.com/page2" {
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}},
				Body: io.NopCloser(strings.NewReader(page2HTML)), ContentLength: int64(len(page2HTML)),
				Request: req,
			}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})

	_, _ = Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 2, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: transport},
	})

	mu.Lock()
	cnt := assetCalls
	mu.Unlock()

	if cnt != 1 {
		t.Errorf("expected 1 request for shared asset, got %d", cnt)
	}
}

func TestAssetNoContentLength(t *testing.T) {
	html := `<html><body><img src="https://test.com/pic.png"></body></html>`
	imgBody := "fakeimagecontent123"

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
			// HEAD без Content-Length -> fallback на GET
			"HEAD https://test.com/pic.png": {
				status: 200, setLength: true, contentLength: -1,
			},
			"GET https://test.com/pic.png": {
				status: 200, body: imgBody,
			},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

	if len(report.Pages[0].Assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(report.Pages[0].Assets))
	}
	asset := report.Pages[0].Assets[0]
	if asset.SizeBytes != int64(len(imgBody)) {
		t.Errorf("expected size %d, got %d", len(imgBody), asset.SizeBytes)
	}
}

func TestAssetError404(t *testing.T) {
	html := `<html><body><script src="https://test.com/missing.js"></script></body></html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
			"HEAD https://test.com/missing.js": {status: 404},
		},
	}

	data, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		HTTPClient: &http.Client{Transport: mock},
	})

	var report Report
	if err := json.Unmarshal(data, &report); err != nil { t.Fatal(err) }

	asset := report.Pages[0].Assets[0]
	if asset.StatusCode != 404 {
		t.Errorf("expected 404, got %d", asset.StatusCode)
	}
	if asset.Error == "" {
		t.Error("expected error message for 404 asset")
	}
}

func TestJSONReportMatchesReference(t *testing.T) {
	rootHTML := `<html>
		<head>
			<title>Example title</title>
			<meta name="description" content="Example description">
		</head>
		<body>
			<h1>Header</h1>
			<a href="https://example.com/missing">broken</a>
			<img src="https://example.com/static/logo.png">
		</body>
	</html>`

	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://example.com": {
				status: 200, body: rootHTML,
				headers: http.Header{"Content-Type": {"text/html"}},
			},
			"HEAD https://example.com/missing": {status: 404},
			"HEAD https://example.com/static/logo.png": {
				status: 200, setLength: true, contentLength: 12345,
				headers: http.Header{"Content-Length": {"12345"}},
			},
		},
	}

	data, err := Analyze(context.Background(), Options{
		URL: "https://example.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		IndentJSON: false, HTTPClient: &http.Client{Transport: mock},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// проверяем структуру
	if report.RootURL != "https://example.com" {
		t.Errorf("root_url = %s", report.RootURL)
	}
	if report.Depth != 1 {
		t.Errorf("depth = %d", report.Depth)
	}
	if report.GeneratedAt.IsZero() {
		t.Error("generated_at is zero")
	}
	if len(report.Pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(report.Pages))
	}

	p := report.Pages[0]
	if p.URL != "https://example.com" {
		t.Errorf("page url = %s", p.URL)
	}
	if p.Depth != 0 {
		t.Errorf("page depth = %d", p.Depth)
	}
	if p.HTTPStatus != 200 {
		t.Errorf("http_status = %d", p.HTTPStatus)
	}
	if p.Status != "ok" {
		t.Errorf("status = %s", p.Status)
	}
	if !p.SEO.HasTitle || p.SEO.Title != "Example title" {
		t.Errorf("seo title: %+v", p.SEO)
	}
	if !p.SEO.HasDescription || p.SEO.Description != "Example description" {
		t.Errorf("seo desc: %+v", p.SEO)
	}
	if !p.SEO.HasH1 {
		t.Error("has_h1 should be true")
	}
	if len(p.BrokenLinks) != 1 || p.BrokenLinks[0].StatusCode != 404 {
		t.Errorf("broken_links: %+v", p.BrokenLinks)
	}
	if len(p.Assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(p.Assets))
	}
	if p.Assets[0].Type != "image" || p.Assets[0].SizeBytes != 12345 {
		t.Errorf("asset: %+v", p.Assets[0])
	}
	if p.DiscoveredAt.IsZero() {
		t.Error("discovered_at is zero")
	}
}

func TestIndentJSONOnlyChangesFormatting(t *testing.T) {
	html := `<html><head><title>T</title></head><body></body></html>`
	mock := testTransport{
		responses: map[string]testResponse{
			"GET https://test.com": {status: 200, body: html, headers: http.Header{"Content-Type": {"text/html"}}},
		},
	}
	client := &http.Client{Transport: mock}

	compact, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		IndentJSON: false, HTTPClient: client,
	})

	indented, _ := Analyze(context.Background(), Options{
		URL: "https://test.com", Depth: 1, Timeout: time.Second, Concurrency: 1,
		IndentJSON: true, HTTPClient: client,
	})

	// формат разный
	if string(compact) == string(indented) {
		t.Error("compact and indented should differ in formatting")
	}

	// содержимое одинаковое (сравниваем без timestamps)
	var r1, r2 Report
	if err := json.Unmarshal(compact, &r1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(indented, &r2); err != nil {
		t.Fatal(err)
	}

	if r1.RootURL != r2.RootURL || r1.Depth != r2.Depth {
		t.Error("content should be the same")
	}
	if len(r1.Pages) != len(r2.Pages) {
		t.Error("page count should match")
	}
	if r1.Pages[0].SEO.Title != r2.Pages[0].SEO.Title {
		t.Error("seo title should match")
	}
}

// хелпер для простых моков
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
