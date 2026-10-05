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

type ProviderService struct {
	upstream *Upstream
	cacheDir string
	refresh  bool
	treeOnce sync.Once
	tree     map[string]any
	treeErr  error
}

func NewProviderService(upstream *Upstream, cacheDir string, refresh bool) *ProviderService {
	return &ProviderService{upstream: upstream, cacheDir: cacheDir, refresh: refresh}
}

func (service *ProviderService) Scrape(ctx context.Context, name string) *ActorProfile {
	profiles := make([]ActorProfile, 0, 3)
	providers := []struct {
		name string
		run  func() (*ActorProfile, error)
	}{
		{"Gfriends", func() (*ActorProfile, error) { return service.scrapeGfriends(ctx, name) }},
		{"Minnano-AV", func() (*ActorProfile, error) { return scrapeMinnano(ctx, service.upstream, name) }},
		{"Wikipedia", func() (*ActorProfile, error) { return scrapeWikipedia(ctx, service.upstream, name) }},
	}
	for _, provider := range providers {
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

func scrapeMinnano(ctx context.Context, upstream *Upstream, name string) (*ActorProfile, error) {
	const base = "https://www.minnano-av.com"
	query := url.Values{"search_scope": {"actress"}, "search_word": {name}, "search": {" Go "}}
	searchURL := base + "/search_result.php?" + query.Encode()
	html, err := fetchText(ctx, upstream, searchURL)
	if err != nil {
		return nil, err
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		label, context, target string
		score                  float64
	}
	candidates := make(map[string]candidate)
	document.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
		href, exists := selection.Attr("href")
		if !exists || !strings.Contains(href, "actress") || !strings.Contains(href, ".html") {
			return
		}
		label := cleanText(selection.Text())
		contextText := cleanText(selection.Closest("li, tr, .actress, section, article").Text())
		if contextText == "" {
			contextText = label
		}
		target := absoluteURL(base, href)
		if target == "" {
			return
		}
		score := max(nameSimilarity(name, label), nameSimilarity(name, contextText))
		threshold := 1.0
		if runeCount(normalizeName(name)) >= 4 {
			threshold = 0.7
		}
		if score >= threshold {
			candidates[target] = candidate{label: label, context: contextText, target: target, score: score}
		}
	})
	items := make([]candidate, 0, len(candidates))
	for _, item := range candidates {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].target < items[j].target
		}
		return items[i].score > items[j].score
	})
	if len(items) == 0 {
		return nil, nil
	}
	profileHTML, err := fetchText(ctx, upstream, items[0].target)
	if err != nil {
		return nil, err
	}
	page, err := goquery.NewDocumentFromReader(strings.NewReader(profileHTML))
	if err != nil {
		return nil, err
	}
	profileRoot := page.Find(".actress-header .act-profile, .actress-header").First()
	imageURL, _ := profileRoot.Find("img").First().Attr("data-src")
	if imageURL == "" {
		imageURL, _ = profileRoot.Find("img").First().Attr("src")
	}
	if imageURL == "" {
		imageURL, _ = page.Find(`meta[property="og:image"]`).Attr("content")
	}
	imageURL = absoluteURL(base, imageURL)
	aliases := []string{items[0].label}
	profileRoot.Find("a").Each(func(_ int, selection *goquery.Selection) { aliases = append(aliases, cleanText(selection.Text())) })
	facts := make([]string, 0)
	page.Find(".actress-header tr, .act-profile tr, .actress-profile tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.Find("th,td")
		if cells.Length() < 2 {
			return
		}
		label, value := cleanText(cells.Eq(0).Text()), cleanText(cells.Eq(1).Text())
		if label != "" && value != "" {
			facts = append(facts, label+"："+value)
		}
	})
	biography := strings.Join(facts, "；")
	if biography == "" {
		biography = cleanText(profileRoot.Find("p").Text())
	}
	if biography == "" {
		biography, _ = page.Find(`meta[name="description"]`).Attr("content")
	}
	return &ActorProfile{Name: name, Aliases: unique(aliases), ImageURL: imageURL, Biography: cleanText(biography), SourceURLs: []string{items[0].target}, SourceNames: []string{"Minnano-AV"}}, nil
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

func absoluteURL(base, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ""
	}
	valueURL, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return baseURL.ResolveReference(valueURL).String()
}

func cleanText(value string) string { return strings.Join(strings.Fields(value), " ") }
