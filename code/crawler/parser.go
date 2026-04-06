package crawler

import (
	"bytes"
	"html"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type SEOInfo struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type assetCandidate struct {
	url       string
	assetType string
}

// extractLinks достает все href из <a> тегов
func extractLinks(htmlBytes []byte) []string {
	doc, err := parseHTML(htmlBytes)
	if err != nil {
		return nil
	}

	var links []string
	seen := make(map[string]struct{})

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}
		href = strings.TrimSpace(href)
		if href == "" {
			return
		}
		if _, ok := seen[href]; ok {
			return
		}
		seen[href] = struct{}{}
		links = append(links, href)
	})

	return links
}

// extractSEO собирает title, description, h1
func extractSEO(htmlBytes []byte) SEOInfo {
	doc, err := parseHTML(htmlBytes)
	if err != nil {
		return SEOInfo{}
	}

	var info SEOInfo

	// title
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if title != "" {
		info.HasTitle = true
		info.Title = html.UnescapeString(title)
	}

	// meta description
	doc.Find("meta[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		name, ok := s.Attr("name")
		if !ok {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(name), "description") {
			content, ok := s.Attr("content")
			if ok {
				content = strings.TrimSpace(html.UnescapeString(content))
				if content != "" {
					info.HasDescription = true
					info.Description = content
				}
			}
			return false
		}
		return true
	})

	// h1
	if doc.Find("h1").Length() > 0 {
		info.HasH1 = true
	}

	return info
}

// extractAssets находит картинки, скрипты и стили на странице
func extractAssets(htmlBytes []byte) []assetCandidate {
	doc, err := parseHTML(htmlBytes)
	if err != nil {
		return nil
	}

	var assets []assetCandidate
	seen := make(map[string]struct{})

	add := func(rawURL, typ string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		key := typ + "::" + rawURL
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		assets = append(assets, assetCandidate{url: rawURL, assetType: typ})
	}

	doc.Find("img[src]").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			add(src, "image")
		}
	})

	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			add(src, "script")
		}
	})

	doc.Find("link[href]").Each(func(_ int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		if !isStylesheet(rel) {
			return
		}
		if href, ok := s.Attr("href"); ok {
			add(href, "style")
		}
	})

	return assets
}

func parseHTML(data []byte) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(bytes.NewReader(data))
}

func isStylesheet(rel string) bool {
	rel = strings.ToLower(rel)
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';'
	}) {
		if part == "stylesheet" {
			return true
		}
	}
	return false
}
