package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const gfriendsTreeURL = "https://cdn.jsdelivr.net/gh/gfriends/gfriends@master/Filetree.json"
const defaultJavDBBaseURL = "https://javdb570.com"

type ProviderService struct {
	upstream   *Upstream
	cacheDir   string
	refresh    bool
	treeMu     sync.Mutex
	treeLoaded bool
	tree       map[string]any
}

type providerScrapeError struct{ errors []error }

func (err *providerScrapeError) Error() string {
	parts := make([]string, 0, len(err.errors))
	for _, item := range err.errors {
		parts = append(parts, item.Error())
	}
	return strings.Join(parts, "; ")
}

func (err *providerScrapeError) Unwrap() []error { return err.errors }

func (err *providerScrapeError) Retryable() bool {
	for _, item := range err.errors {
		if isRetryableUpstreamError(item) {
			return true
		}
	}
	return false
}

func NewProviderService(upstream *Upstream, cacheDir string, refresh bool) *ProviderService {
	return &ProviderService{upstream: upstream, cacheDir: cacheDir, refresh: refresh}
}

func (service *ProviderService) Scrape(ctx context.Context, name string) (*ActorProfile, error) {
	profiles := make([]ActorProfile, 0, 5)
	var failures []error
	providers := []struct {
		name string
		run  func() (*ActorProfile, error)
	}{
		// 收集所有头像候选，供图片处理失败时按优先级回退。
		// 头像顺序：Gfriends → JavDB → Wikipedia → Wikidata。
		// 简介优先级：Wikipedia → Wikidata；合并时保留第一个非空值。
		{"Gfriends", func() (*ActorProfile, error) { return service.scrapeGfriends(ctx, name) }},
		{"JavDB", func() (*ActorProfile, error) { return scrapeJavDB(ctx, service.upstream, name) }},
		{"Wikipedia", func() (*ActorProfile, error) { return scrapeWikipedia(ctx, service.upstream, name) }},
		{"Wikidata", func() (*ActorProfile, error) { return scrapeWikidata(ctx, service.upstream, name) }},
	}
	for _, provider := range providers {
		profile, err := provider.run()
		if err != nil {
			fmt.Printf("%s 的来源 %s 查询失败：%v\n", name, provider.name, err)
			failures = append(failures, fmt.Errorf("%s：%w", provider.name, err))
		}
		if profile != nil {
			profiles = append(profiles, *profile)
		}
	}
	if len(profiles) == 0 {
		if len(failures) > 0 {
			return nil, &providerScrapeError{errors: failures}
		}
		return nil, nil
	}
	merged := &ActorProfile{Name: name}
	seenImages := make(map[string]bool)
	for _, profile := range profiles {
		merged.Aliases = append(merged.Aliases, profile.Aliases...)
		merged.SourceURLs = append(merged.SourceURLs, profile.SourceURLs...)
		merged.SourceNames = append(merged.SourceNames, profile.SourceNames...)
		candidates := profile.ImageCandidates
		if len(candidates) == 0 && strings.TrimSpace(profile.ImageURL) != "" {
			source := firstNonempty(strings.Join(profile.SourceNames, ", "), "未知来源")
			candidates = []PortraitCandidate{{URL: profile.ImageURL, Source: source}}
		}
		for _, candidate := range candidates {
			candidate.URL = strings.TrimSpace(candidate.URL)
			if candidate.URL == "" || seenImages[candidate.URL] {
				continue
			}
			seenImages[candidate.URL] = true
			if candidate.Source == "" {
				candidate.Source = firstNonempty(strings.Join(profile.SourceNames, ", "), "未知来源")
			}
			merged.ImageCandidates = append(merged.ImageCandidates, candidate)
		}
		if merged.ImageURL == "" && len(merged.ImageCandidates) > 0 {
			merged.ImageURL = merged.ImageCandidates[0].URL
		}
		if merged.Biography == "" {
			merged.Biography = strings.TrimSpace(profile.Biography)
			if merged.Biography != "" {
				merged.BiographySource = firstNonempty(profile.BiographySource, strings.Join(profile.SourceNames, ", "))
			}
		}
		if merged.Birthday == "" {
			merged.Birthday = profile.Birthday
		}
	}
	merged.Aliases, merged.SourceURLs, merged.SourceNames = unique(merged.Aliases), unique(merged.SourceURLs), unique(merged.SourceNames)
	if len(failures) > 0 {
		return merged, &providerScrapeError{errors: failures}
	}
	return merged, nil
}

func scrapeWikidata(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
	type searchItem struct {
		ID      string   `json:"id"`
		Label   string   `json:"label"`
		Aliases []string `json:"aliases"`
	}
	var matched []searchItem
	var searchFailures []error
	for _, language := range []string{"zh", "ja", "en"} {
		query := url.Values{"action": {"wbsearchentities"}, "search": {name}, "language": {language}, "format": {"json"}, "limit": {"5"}}
		address := "https://www.wikidata.org/w/api.php?" + query.Encode()
		body, err := upstream.Get(ctx, address, map[string]string{
			"Accept": "application/json", "User-Agent": "fnactor/0.5 (https://github.com/biaobiaobiao108/fnos-actor-scraper)",
		}, defaultBodyLimit)
		if err != nil {
			searchFailures = append(searchFailures, fmt.Errorf("Wikidata %s 搜索：%w", language, err))
			continue
		}
		var response struct {
			Search []searchItem `json:"search"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}
		matched = matched[:0]
		for _, candidate := range response.Search {
			if normalizeName(candidate.Label) == normalizeName(name) || containsExact(candidate.Aliases, name) {
				matched = append(matched, candidate)
			}
		}
		if len(matched) > 0 {
			break
		}
	}
	entities := make(map[string]searchItem, len(matched))
	for _, candidate := range matched {
		if candidate.ID != "" {
			entities[candidate.ID] = candidate
		}
	}
	if len(entities) != 1 {
		if len(searchFailures) > 0 {
			return nil, &providerScrapeError{errors: searchFailures}
		}
		return nil, nil
	}
	var candidate searchItem
	for _, candidate = range entities {
	}
	entityURL := "https://www.wikidata.org/wiki/Special:EntityData/" + url.PathEscape(candidate.ID) + ".json"
	body, err := upstream.Get(ctx, entityURL, map[string]string{
		"Accept": "application/json", "User-Agent": "fnactor/0.5 (https://github.com/biaobiaobiao108/fnos-actor-scraper)",
	}, defaultBodyLimit)
	if err != nil {
		return nil, err
	}
	var response struct {
		Entities map[string]struct {
			Descriptions map[string]struct {
				Value string `json:"value"`
			} `json:"descriptions"`
			Aliases map[string][]struct {
				Value string `json:"value"`
			} `json:"aliases"`
			Claims map[string][]struct {
				MainSnak struct {
					DataValue struct {
						Value json.RawMessage `json:"value"`
					} `json:"datavalue"`
				} `json:"mainsnak"`
			} `json:"claims"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	entity, exists := response.Entities[candidate.ID]
	if !exists {
		return nil, nil
	}
	biography := ""
	for _, language := range []string{"zh", "ja", "en"} {
		if value := strings.TrimSpace(entity.Descriptions[language].Value); value != "" {
			biography = value
			break
		}
	}
	filename := ""
	if statements := entity.Claims["P18"]; len(statements) > 0 {
		_ = json.Unmarshal(statements[0].MainSnak.DataValue.Value, &filename)
	}
	imageURL := ""
	var imageErr error
	if filename != "" {
		imageURL, imageErr = wikimediaThumbnailURL(ctx, upstream, filename)
	}
	aliases := make([]string, 0, len(entity.Aliases["zh"])+len(entity.Aliases["ja"])+1)
	if candidate.Label != name {
		aliases = append(aliases, candidate.Label)
	}
	for _, language := range []string{"zh", "ja", "en"} {
		for _, alias := range entity.Aliases[language] {
			aliases = append(aliases, alias.Value)
		}
	}
	sourceURLs := []string{"https://www.wikidata.org/wiki/" + candidate.ID}
	if filename != "" {
		sourceURLs = append(sourceURLs, "https://commons.wikimedia.org/wiki/File:"+url.PathEscape(filename))
	}
	if biography == "" && imageURL == "" {
		return nil, imageErr
	}
	biographySource := ""
	if biography != "" {
		biographySource = "Wikidata"
	}
	profile := &ActorProfile{Name: name, Aliases: unique(aliases), ImageURL: imageURL, Biography: biography,
		BiographySource: biographySource,
		SourceURLs:      sourceURLs, SourceNames: []string{"Wikidata"}}
	if len(searchFailures) > 0 {
		if imageErr != nil {
			searchFailures = append(searchFailures, imageErr)
		}
		return profile, &providerScrapeError{errors: searchFailures}
	}
	return profile, imageErr
}

func wikimediaThumbnailURL(ctx context.Context, upstream *Upstream, filename string) (string, error) {
	query := url.Values{"action": {"query"}, "titles": {"File:" + filename}, "prop": {"imageinfo"}, "iiprop": {"url"}, "iiurlwidth": {"640"}, "format": {"json"}, "formatversion": {"2"}}
	var response struct {
		Query struct {
			Pages []struct {
				ImageInfo []struct {
					ThumbURL string `json:"thumburl"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := fetchJSON(ctx, upstream, "https://commons.wikimedia.org/w/api.php?"+query.Encode(), &response); err != nil {
		return "", err
	}
	for _, page := range response.Query.Pages {
		if len(page.ImageInfo) > 0 && page.ImageInfo[0].ThumbURL != "" {
			return page.ImageInfo[0].ThumbURL, nil
		}
	}
	return "", fmt.Errorf("Wikimedia Commons 未返回缩放头像")
}

func containsExact(values []string, expected string) bool {
	for _, value := range values {
		if normalizeName(value) == normalizeName(expected) {
			return true
		}
	}
	return false
}

func (service *ProviderService) loadGfriendsTree(ctx context.Context) (map[string]any, error) {
	service.treeMu.Lock()
	defer service.treeMu.Unlock()
	if service.treeLoaded {
		return service.tree, nil
	}
	cachePath := filepath.Join(service.cacheDir, "gfriends-filetree.json")
	if !service.refresh {
		if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < 24*time.Hour && info.Size() <= defaultBodyLimit {
			if data, err := os.ReadFile(cachePath); err == nil {
				var tree map[string]any
				if json.Unmarshal(data, &tree) == nil && tree != nil {
					service.tree, service.treeLoaded = tree, true
					return service.tree, nil
				}
			}
		}
	}
	data, err := service.upstream.Get(ctx, gfriendsTreeURL, map[string]string{"Accept": "application/json"}, defaultBodyLimit)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	if tree == nil {
		return nil, fmt.Errorf("Gfriends 文件树为空")
	}
	if err := os.MkdirAll(service.cacheDir, 0o750); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(cachePath, data, 0o640); err != nil {
		return nil, err
	}
	service.tree, service.treeLoaded = tree, true
	return service.tree, nil
}

func (service *ProviderService) scrapeGfriends(ctx context.Context, name string) (*ActorProfile, error) {
	tree, err := service.loadGfriendsTree(ctx)
	if err != nil {
		return nil, err
	}
	wanted := normalizeName(strings.TrimSuffix(name, filepath.Ext(name)))
	paths := make([]string, 0, 1)
	var walk func(any, []string)
	walk = func(node any, parents []string) {
		object, ok := node.(map[string]any)
		if !ok {
			return
		}
		for key, value := range object {
			if key == "Information" {
				continue
			}
			if child, ok := value.(string); ok {
				alias := normalizeName(strings.TrimSuffix(strings.TrimSuffix(key, ".jpg"), ".png"))
				if alias == wanted {
					paths = append(paths, strings.Join(append(append([]string{}, parents...), child), "/"))
				}
				continue
			}
			walk(value, append(append([]string{}, parents...), key))
		}
	}
	walk(tree["Content"], []string{"Content"})
	if len(paths) == 0 {
		return nil, nil
	}
	sort.Strings(paths)
	imageURL := gfriendsRawURL(paths[0])
	return &ActorProfile{Name: name, Aliases: []string{}, ImageURL: imageURL, SourceURLs: []string{gfriendsTreeURL, imageURL}, SourceNames: []string{"Gfriends"}}, nil
}

func gfriendsRawURL(path string) string {
	parts := strings.SplitN(path, "?", 2)
	segments := strings.Split(parts[0], "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	result := "https://raw.githubusercontent.com/gfriends/gfriends/master/" + strings.Join(segments, "/")
	if len(parts) == 2 {
		result += "?" + parts[1]
	}
	return result
}

func scrapeJavDB(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
	baseURL, err := javDBBase()
	if err != nil {
		return nil, err
	}
	query := url.Values{"f": {"actor"}, "q": {name}}
	searchURL := strings.TrimRight(baseURL, "/") + "/search?" + query.Encode()
	html, err := fetchText(ctx, upstream, searchURL)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		pageURL, imageURL string
		aliases           []string
	}
	candidates := make(map[string]candidate)
	document.Find("a[href^='/actors/']").Each(func(_ int, selection *goquery.Selection) {
		classes, _ := selection.Attr("class")
		if containsExact(strings.Fields(classes), "navbar-item") {
			return
		}
		aliases := strings.Split(selection.AttrOr("title", ""), ",")
		if !containsExact(aliases, name) {
			return
		}
		imageURL := selection.Find("img.avatar").First().AttrOr("src", "")
		if imageURL == "" || strings.Contains(imageURL, "/images/actor_unknow.") {
			return
		}
		pageURL, err := resolveJavDBURL(selection.AttrOr("href", ""))
		if err != nil {
			return
		}
		page, err := url.Parse(pageURL)
		base, _ := url.Parse(baseURL)
		if err != nil || page.Hostname() != base.Hostname() {
			return
		}
		imageURL, err = resolveJavDBURL(imageURL)
		if err != nil {
			return
		}
		image, err := url.Parse(imageURL)
		if err != nil || !isPublicHost(image.Hostname()) ||
			(image.Hostname() != base.Hostname() && image.Hostname() != "jdbstatic.com" && !strings.HasSuffix(image.Hostname(), ".jdbstatic.com")) {
			return
		}
		for index := range aliases {
			aliases[index] = strings.TrimSpace(aliases[index])
		}
		candidates[pageURL] = candidate{pageURL: pageURL, imageURL: imageURL, aliases: unique(aliases)}
	})
	if len(candidates) != 1 {
		return nil, nil
	}
	var match candidate
	for _, match = range candidates {
	}
	return &ActorProfile{Name: name, Aliases: match.aliases, ImageURL: match.imageURL,
		SourceURLs: []string{match.pageURL, match.imageURL}, SourceNames: []string{"JavDB"}}, nil
}

func resolveJavDBURL(value string) (string, error) {
	target, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	baseURL, err := javDBBase()
	if err != nil {
		return "", err
	}
	base, _ := url.Parse(baseURL)
	resolved := base.ResolveReference(target)
	if resolved.Scheme != "https" || resolved.Hostname() == "" || !isPublicHost(resolved.Hostname()) {
		return "", fmt.Errorf("JavDB 返回了不安全的链接")
	}
	return resolved.String(), nil
}

func javDBBase() (string, error) {
	value := strings.TrimRight(strings.TrimSpace(getenv("JAVDB_BASE_URL", defaultJavDBBaseURL)), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || !isPublicHost(parsed.Hostname()) || parsed.User != nil {
		return "", fmt.Errorf("JAVDB_BASE_URL 必须是公网 HTTPS 基础地址")
	}
	return value, nil
}

func scrapeWikipedia(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
	var failures []error
	for _, language := range []string{"zh", "ja"} {
		query := url.Values{"action": {"query"}, "generator": {"search"}, "gsrsearch": {name}, "gsrnamespace": {"0"}, "gsrlimit": {"5"}, "prop": {"extracts|pageimages"}, "exintro": {"1"}, "explaintext": {"1"}, "piprop": {"thumbnail"}, "pithumbsize": {"640"}, "format": {"json"}, "formatversion": {"2"}, "utf8": {"1"}}
		var response struct {
			Query struct {
				Pages []struct {
					Title     string `json:"title"`
					Extract   string `json:"extract"`
					Thumbnail struct {
						Source string `json:"source"`
					} `json:"thumbnail"`
				} `json:"pages"`
			} `json:"query"`
		}
		target := "https://" + language + ".wikipedia.org/w/api.php?" + query.Encode()
		if err := fetchJSON(ctx, upstream, target, &response); err != nil {
			failures = append(failures, fmt.Errorf("Wikipedia (%s)：%w", language, err))
			continue
		}
		bestIndex := -1
		ambiguous := false
		for index, page := range response.Query.Pages {
			if normalizeName(name) == normalizeName(page.Title) {
				if bestIndex >= 0 {
					ambiguous = true
					break
				}
				bestIndex = index
			}
		}
		if bestIndex < 0 || ambiguous {
			continue
		}
		page := response.Query.Pages[bestIndex]
		if strings.TrimSpace(page.Extract) == "" && page.Thumbnail.Source == "" {
			continue
		}
		aliases := []string{}
		if page.Title != name {
			aliases = append(aliases, page.Title)
		}
		biography := strings.TrimSpace(page.Extract)
		biographySource := ""
		if biography != "" {
			biographySource = "Wikipedia (" + language + ")"
		}
		profile := &ActorProfile{Name: name, Aliases: aliases, ImageURL: page.Thumbnail.Source, Biography: biography, BiographySource: biographySource, SourceURLs: []string{"https://" + language + ".wikipedia.org/wiki/" + url.PathEscape(page.Title)}, SourceNames: []string{"Wikipedia (" + language + ")"}}
		if len(failures) > 0 {
			return profile, &providerScrapeError{errors: failures}
		}
		return profile, nil
	}
	if len(failures) > 0 {
		return nil, &providerScrapeError{errors: failures}
	}
	return nil, nil
}
