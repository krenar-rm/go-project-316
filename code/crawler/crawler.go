package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Options struct {
	URL         string
	Depth       int
	Retries     int
	Delay       time.Duration
	Timeout     time.Duration
	UserAgent   string
	Concurrency int
	IndentJSON  bool
	HTTPClient  *http.Client
}

type Report struct {
	RootURL     string       `json:"root_url"`
	Depth       int          `json:"depth"`
	GeneratedAt time.Time    `json:"generated_at"`
	Pages       []PageReport `json:"pages"`
}

type PageReport struct {
	URL          string             `json:"url"`
	Depth        int                `json:"depth"`
	HTTPStatus   int                `json:"http_status"`
	Status       string             `json:"status"`
	Error        string             `json:"error,omitempty"`
	SEO          SEOInfo            `json:"seo"`
	BrokenLinks  []BrokenLinkReport `json:"broken_links"`
	Assets       []AssetReport      `json:"assets"`
	DiscoveredAt time.Time          `json:"discovered_at"`
}

type BrokenLinkReport struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

type AssetReport struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Analyze запускает краулер и возвращает JSON отчет
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	report, err := runCrawl(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.IndentJSON {
		return json.MarshalIndent(report, "", "  ")
	}
	return json.Marshal(report)
}

func runCrawl(ctx context.Context, opts Options) (*Report, error) {
	if opts.URL == "" {
		return nil, errors.New("url is required")
	}

	root, err := url.Parse(opts.URL)
	if err != nil {
		return nil, err
	}
	if root.Scheme == "" {
		root.Scheme = "https"
	}

	// дефолты
	if opts.Depth <= 0 {
		opts.Depth = 10
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}

	client := setupClient(opts)
	f := newFetcher(client, opts.Retries, opts.Delay, opts.UserAgent)

	c := &crawlerState{
		opts:       opts,
		root:       root,
		fetcher:    f,
		visited:    make(map[string]struct{}),
		linkCache:  make(map[string]BrokenLinkReport),
		assetCache: make(map[string]AssetReport),
	}

	return c.execute(ctx)
}

type crawlJob struct {
	url   *url.URL
	depth int
}

type crawlerState struct {
	opts    Options
	root    *url.URL
	fetcher *fetcher

	mu         sync.Mutex
	visited    map[string]struct{}
	linkCache  map[string]BrokenLinkReport
	assetCache map[string]AssetReport

	pagesMu sync.Mutex
	pages   []PageReport
}

func (c *crawlerState) execute(ctx context.Context) (*Report, error) {
	report := &Report{
		RootURL:     c.root.String(),
		Depth:       c.opts.Depth,
		GeneratedAt: time.Now(),
	}

	jobs := make(chan crawlJob)
	var workerWg sync.WaitGroup
	var taskWg sync.WaitGroup

	worker := func() {
		defer workerWg.Done()
		for job := range jobs {
			if ctx.Err() != nil {
				taskWg.Done()
				continue
			}

			page, links := c.processPage(ctx, job)
			c.addPage(page)

			nextDepth := job.depth + 1
			if nextDepth < c.opts.Depth {
				for _, link := range links {
					if !c.tryVisit(link) {
						continue
					}
					taskWg.Add(1)
					select {
					case jobs <- crawlJob{url: link, depth: nextDepth}:
					case <-ctx.Done():
						taskWg.Done()
						continue
					}
				}
			}
			taskWg.Done()

			if ctx.Err() != nil {
				continue
			}
		}
	}

	for i := 0; i < c.opts.Concurrency; i++ {
		workerWg.Add(1)
		go worker()
	}

	// начинаем с корневого URL
	if c.tryVisit(c.root) {
		taskWg.Add(1)
		select {
		case jobs <- crawlJob{url: c.root, depth: 0}:
		case <-ctx.Done():
			taskWg.Done()
		}
	}

	go func() {
		taskWg.Wait()
		close(jobs)
	}()

	workerWg.Wait()

	report.Pages = c.getPages()
	return report, ctx.Err()
}

func setupClient(opts Options) *http.Client {
	if opts.HTTPClient != nil {
		cl := *opts.HTTPClient
		if opts.Timeout > 0 && cl.Timeout == 0 {
			cl.Timeout = opts.Timeout
		}
		return &cl
	}
	cl := &http.Client{}
	if opts.Timeout > 0 {
		cl.Timeout = opts.Timeout
	}
	return cl
}

func (c *crawlerState) processPage(ctx context.Context, job crawlJob) (PageReport, []*url.URL) {
	page := PageReport{
		URL:          job.url.String(),
		Depth:        job.depth,
		Status:       "ok",
		DiscoveredAt: time.Now(),
	}

	res, err := c.fetcher.doFetch(ctx, http.MethodGet, job.url.String())
	if err != nil {
		page.Status = "error"
		page.Error = err.Error()
		return page, nil
	}

	page.HTTPStatus = res.statusCode
	if res.statusCode >= 400 {
		page.Status = "error"
		page.Error = http.StatusText(res.statusCode)
	}

	body := res.body

	// SEO
	page.SEO = extractSEO(body)

	// ссылки и битые ссылки
	discovered, broken := c.processLinks(ctx, job, body)
	page.BrokenLinks = broken

	// ассеты
	page.Assets = c.processAssets(ctx, job.url, extractAssets(body))

	return page, discovered
}

func (c *crawlerState) processLinks(ctx context.Context, current crawlJob, body []byte) ([]*url.URL, []BrokenLinkReport) {
	rawLinks := extractLinks(body)
	var discovered []*url.URL
	var broken []BrokenLinkReport

	nextDepth := current.depth + 1
	canFollow := nextDepth < c.opts.Depth

	for _, href := range rawLinks {
		if isAnchor(href) {
			continue
		}

		linkURL, err := resolveURL(current.url, href)
		if err != nil || !isHTTPScheme(linkURL) {
			continue
		}

		// пропускаем ссылку на саму себя
		if isSamePage(linkURL, current.url) {
			continue
		}

		internal := linkURL.Host == c.root.Host
		if internal {
			discovered = append(discovered, linkURL)
			if canFollow {
				continue // будет загружена как отдельная страница
			}
		}

		// проверяем в кеше
		if cached, ok := c.getLinkCache(linkURL); ok {
			if cached.Error != "" || cached.StatusCode >= 400 {
				broken = append(broken, cached)
			}
			continue
		}

		// проверяем ссылку
		report := c.checkOneLink(ctx, linkURL)
		c.setLinkCache(linkURL, report)
		if report.Error != "" || report.StatusCode >= 400 {
			broken = append(broken, report)
		}
	}

	return discovered, broken
}

func (c *crawlerState) checkOneLink(ctx context.Context, link *url.URL) BrokenLinkReport {
	report := BrokenLinkReport{URL: link.String()}

	res, err := c.fetcher.doFetch(ctx, http.MethodHead, link.String())
	if err != nil {
		report.Error = err.Error()
		return report
	}

	report.StatusCode = res.statusCode

	// если HEAD не поддерживается, пробуем GET
	if res.statusCode == http.StatusMethodNotAllowed {
		res, err = c.fetcher.doFetch(ctx, http.MethodGet, link.String())
		if err != nil {
			report.Error = err.Error()
			return report
		}
		report.StatusCode = res.statusCode
	}

	if res.statusCode >= 400 {
		report.Error = http.StatusText(res.statusCode)
	}
	return report
}

func (c *crawlerState) processAssets(ctx context.Context, base *url.URL, candidates []assetCandidate) []AssetReport {
	var result []AssetReport
	for _, asset := range candidates {
		assetURL, err := resolveURL(base, asset.url)
		if err != nil || !isHTTPScheme(assetURL) {
			continue
		}

		if cached, ok := c.getAssetCache(assetURL); ok {
			result = append(result, cached)
			continue
		}

		ar := AssetReport{
			URL:  assetURL.String(),
			Type: asset.assetType,
		}

		res, err := c.fetcher.doFetch(ctx, http.MethodHead, assetURL.String())
		if err != nil {
			ar.Error = err.Error()
			c.setAssetCache(assetURL, ar)
			result = append(result, ar)
			continue
		}

		ar.StatusCode = res.statusCode

		// fallback на GET если HEAD не поддерживается или нет Content-Length
		if res.statusCode == http.StatusMethodNotAllowed || (res.statusCode < 400 && res.contentLength < 0) {
			res, err = c.fetcher.doFetch(ctx, http.MethodGet, assetURL.String())
			if err != nil {
				ar.Error = err.Error()
				c.setAssetCache(assetURL, ar)
				result = append(result, ar)
				continue
			}
			ar.StatusCode = res.statusCode
		}

		if res.statusCode >= 400 {
			ar.Error = http.StatusText(res.statusCode)
		} else {
			size := res.contentLength
			if size < 0 {
				size = int64(len(res.body))
				if size == 0 {
					ar.Error = "unknown content length"
				}
			}
			ar.SizeBytes = size
		}

		c.setAssetCache(assetURL, ar)
		result = append(result, ar)
	}
	return result
}

// хелперы для URL

func resolveURL(base *url.URL, raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty url")
	}
	if strings.HasPrefix(raw, "javascript:") || strings.HasPrefix(raw, "mailto:") || strings.HasPrefix(raw, "tel:") {
		return nil, errors.New("unsupported scheme")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		parsed = base.ResolveReference(parsed)
	}
	parsed.Fragment = ""
	return parsed, nil
}

func canonURL(u *url.URL) string {
	cp := *u
	cp.Fragment = ""
	if cp.Path == "" {
		cp.Path = "/"
	}
	return cp.String()
}

func isSamePage(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return canonURL(a) == canonURL(b)
}

func isAnchor(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), "#")
}

func isHTTPScheme(u *url.URL) bool {
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}

func (c *crawlerState) tryVisit(u *url.URL) bool {
	if !isHTTPScheme(u) || u.Host != c.root.Host {
		return false
	}
	key := canonURL(u)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.visited[key]; ok {
		return false
	}
	c.visited[key] = struct{}{}
	return true
}

// кеш ссылок
func (c *crawlerState) getLinkCache(u *url.URL) (BrokenLinkReport, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.linkCache[u.String()]
	return r, ok
}

func (c *crawlerState) setLinkCache(u *url.URL, r BrokenLinkReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.linkCache[u.String()] = r
}

// кеш ассетов
func (c *crawlerState) getAssetCache(u *url.URL) (AssetReport, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.assetCache[u.String()]
	return r, ok
}

func (c *crawlerState) setAssetCache(u *url.URL, r AssetReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assetCache[u.String()] = r
}

func (c *crawlerState) addPage(p PageReport) {
	c.pagesMu.Lock()
	c.pages = append(c.pages, p)
	c.pagesMu.Unlock()
}

func (c *crawlerState) getPages() []PageReport {
	c.pagesMu.Lock()
	defer c.pagesMu.Unlock()
	out := make([]PageReport, len(c.pages))
	copy(out, c.pages)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth == out[j].Depth {
			return out[i].URL < out[j].URL
		}
		return out[i].Depth < out[j].Depth
	})
	return out
}
