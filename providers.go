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
)

const gfriendsTreeURL = "https://cdn.jsdelivr.net/gh/gfriends/gfriends@master/Filetree.json"

type ProviderService struct {
	upstream   *Upstream
	cacheDir   string
	refresh    bool
	treeOnce   sync.Once
	tree       map[string]any
	treeErr    error
	disabledMu sync.Mutex
	disabled   map[string]bool
}

func NewProviderService(upstream *Upstream, cacheDir string, refresh bool) *ProviderService {
	return &ProviderService{upstream: upstream, cacheDir: cacheDir, refresh: refresh, disabled: make(map[string]bool)}
}

func (service *ProviderService) Scrape(ctx context.Context, name string) *ActorProfile {
	profiles := make([]ActorProfile, 0, 5)
	providers := []struct {
		name string
		run  func() (*ActorProfile, error)
	}{
		// 头像优先级：Gfriends（针对 AV 演员的头像库）→ Wikipedia → Wikidata。
		// 简介优先级：Wikipedia → Wikidata；合并时保留各字段第一个非空值。
		{"Gfriends", func() (*ActorProfile, error) { return service.scrapeGfriends(ctx, name) }},
		{"Wikipedia", func() (*ActorProfile, error) { return scrapeWikipedia(ctx, service.upstream, name) }},
		{"Wikidata", func() (*ActorProfile, error) { return scrapeWikidata(ctx, service.upstream, name) }},
	}
	for _, provider := range providers {
		if service.isDisabled(provider.name) {
			continue
		}
		profile, err := provider.run()
		if err != nil {
			fmt.Printf("%s 的来源 %s 查询失败：%v\n", name, provider.name, err)
			continue
		}
		if profile != nil {
			profiles = append(profiles, *profile)
		}
	}
	if len(profiles) == 0 {
		return nil
	}
	merged := &ActorProfile{Name: name}
	for _, profile := range profiles {
		merged.Aliases = append(merged.Aliases, profile.Aliases...)
		merged.SourceURLs = append(merged.SourceURLs, profile.SourceURLs...)
		merged.SourceNames = append(merged.SourceNames, profile.SourceNames...)
		if merged.ImageURL == "" {
			merged.ImageURL = profile.ImageURL
		}
		if merged.Biography == "" {
			merged.Biography = strings.TrimSpace(profile.Biography)
		}
		if merged.Birthday == "" {
			merged.Birthday = profile.Birthday
		}
	}
	merged.Aliases, merged.SourceURLs, merged.SourceNames = unique(merged.Aliases), unique(merged.SourceURLs), unique(merged.SourceNames)
	return merged
}

func (service *ProviderService) isDisabled(name string) bool {
	service.disabledMu.Lock()
	defer service.disabledMu.Unlock()
	return service.disabled[name]
}

func (service *ProviderService) disable(name string) bool {
	service.disabledMu.Lock()
	defer service.disabledMu.Unlock()
	if service.disabled[name] {
		return false
	}
	service.disabled[name] = true
	return true
}

func scrapeWikidata(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
	type searchItem struct {
		ID      string   `json:"id"`
		Label   string   `json:"label"`
		Aliases []string `json:"aliases"`
	}
	var matched []searchItem
	for _, language := range []string{"zh", "ja", "en"} {
		query := url.Values{"action": {"wbsearchentities"}, "search": {name}, "language": {language}, "format": {"json"}, "limit": {"5"}}
		address := "https://www.wikidata.org/w/api.php?" + query.Encode()
		body, err := upstream.Get(ctx, address, map[string]string{
			"Accept": "application/json", "User-Agent": "fnactor/0.5 (https://github.com/biaobiaobiao108/fnos-actor-scraper)",
		}, defaultBodyLimit)
		if err != nil {
			if language == "en" {
				return nil, err
			}
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
	if filename != "" {
		imageURL = "https://commons.wikimedia.org/wiki/Special:FilePath/" + url.PathEscape(filename)
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
		return nil, nil
	}
	return &ActorProfile{Name: name, Aliases: unique(aliases), ImageURL: imageURL, Biography: biography,
		SourceURLs: sourceURLs, SourceNames: []string{"Wikidata"}}, nil
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
	service.treeOnce.Do(func() {
		cachePath := filepath.Join(service.cacheDir, "gfriends-filetree.json")
		if !service.refresh {
			if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < 24*time.Hour && info.Size() <= defaultBodyLimit {
				if data, err := os.ReadFile(cachePath); err == nil {
					service.treeErr = json.Unmarshal(data, &service.tree)
					if service.treeErr == nil {
						return
					}
				}
			}
		}
		data, err := service.upstream.Get(ctx, gfriendsTreeURL, map[string]string{"Accept": "application/json"}, defaultBodyLimit)
		if err != nil {
			service.treeErr = err
			return
		}
		if err := json.Unmarshal(data, &service.tree); err != nil {
			service.treeErr = err
			return
		}
		if err := os.MkdirAll(service.cacheDir, 0o750); err != nil {
			service.treeErr = err
			return
		}
		if err := os.WriteFile(cachePath, data, 0o640); err != nil {
			service.treeErr = err
		}
	})
	return service.tree, service.treeErr
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

func scrapeWikipedia(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
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
			continue
		}
		bestScore := 0.0
		bestIndex := -1
		for index, page := range response.Query.Pages {
			score := nameSimilarity(name, page.Title)
			threshold := 1.0
			if runeCount(name) >= 4 {
				threshold = 0.7
			}
			if score >= threshold && score > bestScore {
				bestScore, bestIndex = score, index
			}
		}
		if bestIndex < 0 {
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
		return &ActorProfile{Name: name, Aliases: aliases, ImageURL: page.Thumbnail.Source, Biography: strings.TrimSpace(page.Extract), SourceURLs: []string{"https://" + language + ".wikipedia.org/wiki/" + url.PathEscape(page.Title)}, SourceNames: []string{"Wikipedia (" + language + ")"}}, nil
	}
	return nil, nil
}
