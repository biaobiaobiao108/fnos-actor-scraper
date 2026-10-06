package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode"
)

type options struct {
	actor, root, database, cache            string
	limit, concurrency                      int
	watchInterval                           time.Duration
	apply, overwrite, refresh, probe, watch bool
}
type task struct {
	name          string
	count         int
	person        *FnPerson
	lookupAliases []string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[ERR] 错误：%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if limit := strings.TrimSpace(os.Getenv("GOMEMLIMIT")); limit != "" {
		if bytes, err := parseMemoryLimit(limit); err == nil {
			debug.SetMemoryLimit(bytes)
		}
	}
	args := flag.NewFlagSet("fnactor", flag.ContinueOnError)
	args.SetOutput(os.Stderr)
	var o options
	args.StringVar(&o.actor, "actor", "", "只处理指定演员")
	args.StringVar(&o.root, "root", "", "从该目录递归读取 NFO 演员名（可选）")
	args.StringVar(&o.database, "db", getenv("FNOS_DB_PATH", "/fnos-db/trimmedia.db"), "飞牛影视 SQLite 数据库路径")
	args.StringVar(&o.cache, "cache", getenv("CACHE_DIR", "/config"), "缓存目录")
	args.IntVar(&o.limit, "limit", 0, "最多处理演员数，0 表示不限")
	args.IntVar(&o.concurrency, "concurrency", 1, "并发演员数，范围 1 到 2")
	args.BoolVar(&o.apply, "apply", false, "将缺失的演员头像/简介写入飞牛")
	args.BoolVar(&o.overwrite, "overwrite", false, "覆盖已存在字段，必须同时指定 --apply")
	args.BoolVar(&o.refresh, "refresh", false, "忽略来源缓存并重新查询")
	args.BoolVar(&o.probe, "probe", false, "仅测试在线来源和头像处理，不登录或修改飞牛")
	args.BoolVar(&o.watch, "watch", false, "持续监控演员数据库；首次扫描全库并补充缺失字段")
	args.DurationVar(&o.watchInterval, "watch-interval", time.Minute, "监控轮询间隔，范围 10 秒到 24 小时")
	args.Usage = func() {
		fmt.Fprint(args.Output(), `fnactor — 补全飞牛影视中的本地演员档案

用法：fnactor [参数]

默认只预览；只有显式指定 --apply 才会写回飞牛。批量模式从只读挂载的飞牛影视数据库枚举演员。

参数：
  --actor NAME       只处理指定演员，不依赖数据库
  --root DIR         可选：只从该目录的 NFO 中选择演员（须挂载媒体目录）
  --db FILE          数据库路径（默认 FNOS_DB_PATH 或 /fnos-db/trimmedia.db）
  --cache DIR        缓存路径（默认 CACHE_DIR 或 /config）
  --limit N          最多处理 N 个演员，0 表示不限制
  --concurrency N    演员并发数，默认 1，最大 2
  --apply            写入飞牛本地演员档案
  --overwrite        覆盖已有头像/简介（仍保护官方、在线和锁定资料）
  --refresh          忽略缓存并重新抓取来源
  --probe            只测试来源和图片，不登录或写入飞牛
  --watch            持续监控演员库（首次启动扫描全库并补充缺失字段）
  --watch-interval   监控轮询间隔，默认 1m，范围 10s 到 24h
  --help             显示帮助

环境变量：FNOS_URL、FNOS_USERNAME、FNOS_PASSWORD 或 FNOS_TOKEN、FNOS_DB_PATH、UPSTREAM_DELAY_MS、JAVDB_BASE_URL。
`)
	}
	if err := args.Parse(os.Args[1:]); err == flag.ErrHelp {
		return nil
	} else if err != nil {
		return err
	}
	if args.NArg() > 0 {
		return fmt.Errorf("不支持的位置参数：%s", strings.Join(args.Args(), " "))
	}
	if len(os.Args) == 1 {
		args.Usage()
		return nil
	}
	if o.limit < 0 {
		return fmt.Errorf("--limit 必须是非负整数")
	}
	if o.concurrency < 1 || o.concurrency > 2 {
		return fmt.Errorf("--concurrency 必须在 1 到 2 之间")
	}
	if o.overwrite && !o.apply {
		return fmt.Errorf("--overwrite 需要同时指定 --apply")
	}
	if o.actor != "" {
		o.actor = strings.TrimSpace(o.actor)
		if o.actor == "" {
			return fmt.Errorf("--actor 不能为空")
		}
		if isNumericActorName(o.actor) {
			return fmt.Errorf("演员名 %q 是纯数字；本程序按演员名称匹配，请提供真实演员姓名", o.actor)
		}
	}
	if o.probe && o.actor == "" {
		return fmt.Errorf("--probe 需要同时指定 --actor 演员名")
	}
	if o.actor != "" && o.root != "" {
		return fmt.Errorf("--actor 与 --root 不能同时指定")
	}
	if o.probe && o.apply {
		return fmt.Errorf("--probe 仅用于只读诊断，不能与 --apply 同时指定")
	}
	if o.watch {
		if !o.apply {
			return fmt.Errorf("--watch 是持续自动处理模式，必须显式指定 --apply")
		}
		if o.actor != "" || o.root != "" || o.probe || o.limit > 0 || o.overwrite {
			return fmt.Errorf("--watch 不能与 --actor、--root、--probe、--limit 或 --overwrite 同时使用")
		}
		if o.concurrency != 1 {
			return fmt.Errorf("--watch 当前按演员顺序处理，请保持 --concurrency=1")
		}
		if o.watchInterval < 10*time.Second || o.watchInterval > 24*time.Hour {
			return fmt.Errorf("--watch-interval 必须在 10s 到 24h 之间")
		}
	}
	if o.watch {
		return runWatch(o)
	}

	tasks, err := collectTasks(o)
	if err != nil {
		return err
	}
	if o.limit > 0 && len(tasks) > o.limit {
		tasks = tasks[:o.limit]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.probe {
		return probe(ctx, tasks[0].name, o)
	}
	fmt.Printf("\n[SCAN] 收集到 %d 个待处理演员，本次处理 %d 个。模式：%s\n", len(tasks), len(tasks), map[bool]string{true: "写入飞牛", false: "预览"}[o.apply])
	if len(tasks) == 0 {
		fmt.Println("\n=^._.^= [IDLE] 这轮没有演员需要处理，先歇一会儿～\n")
		return nil
	}
	base := strings.TrimSpace(os.Getenv("FNOS_URL"))
	if base == "" {
		return fmt.Errorf("请设置 FNOS_URL，例如 http://127.0.0.1:5666")
	}
	if err := validateFnOSURL(base); err != nil {
		return err
	}
	client := NewFnOSClient(base, os.Getenv("FNOS_USERNAME"), os.Getenv("FNOS_PASSWORD"), os.Getenv("FNOS_TOKEN"))
	if err := client.Login(ctx); err != nil {
		return err
	}
	upstream := NewUpstream()
	providers := NewProviderService(upstream, o.cache, o.refresh)
	defer providers.Close()
	if err := providers.loadNameAliases(); err != nil {
		return err
	}
	jobs := make(chan task)
	results := make(chan error)
	workers := min(o.concurrency, len(tasks))
	for range workers {
		go func() {
			for item := range jobs {
				results <- processActor(ctx, client, providers, upstream, item, o)
			}
		}()
	}
	go func() {
		for _, item := range tasks {
			jobs <- item
		}
		close(jobs)
	}()
	var firstErr error
	for range tasks {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func collectTasks(o options) ([]task, error) {
	if o.actor != "" {
		return []task{{name: o.actor, count: 1}}, nil
	}
	if o.root != "" {
		actors, err := scanNFO(o.root)
		if err != nil {
			return nil, err
		}
		result := make([]task, 0, len(actors))
		skippedNumeric := 0
		for _, actor := range actors {
			if isNumericActorName(actor.name) {
				skippedNumeric++
				continue
			}
			result = append(result, task{name: actor.name, count: actor.count})
		}
		if skippedNumeric > 0 && !o.watch {
			fmt.Printf("[SKIP] 跳过 %d 个纯数字演员名称（无法按姓名查询上游资料）\n", skippedNumeric)
		}
		return result, nil
	}
	people, err := loadLocalPeople(o.database)
	if err != nil {
		return nil, fmt.Errorf("读取飞牛演员数据库失败：%w；请确认数据库路径存在且数据库目录未设为 Docker 只读挂载（程序以 mode=ro 查询，WAL 模式仍需要处理临时 SHM 锁文件）", err)
	}
	result := make([]task, 0, len(people))
	skippedNumeric := 0
	for index := range people {
		person := people[index]
		name := firstNonempty(person.Name, person.OriginalName)
		if name == "" {
			continue
		}
		if isNumericActorName(name) {
			skippedNumeric++
			continue
		}
		aliases := []string{}
		if originalName := strings.TrimSpace(person.OriginalName); originalName != "" && normalizeName(originalName) != normalizeName(name) {
			aliases = append(aliases, originalName)
		}
		result = append(result, task{name: name, count: 1, person: &person, lookupAliases: aliases})
	}
	if skippedNumeric > 0 && !o.watch {
		fmt.Printf("[SKIP] 跳过 %d 条纯数字名称记录（看起来是演员编号，无法按名称查询资料）\n", skippedNumeric)
	}
	return result, nil
}

func isNumericActorName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, char := range name {
		if !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

func processActor(ctx context.Context, client *FnOSClient, providers *ProviderService, upstream *Upstream, item task, o options) error {
	name := item.name
	fmt.Printf("\n--- [ACTOR] %s ---\n", name)
	var matches []FnPerson
	if item.person != nil {
		matches = []FnPerson{*item.person}
	} else {
		found, err := client.SearchPeople(ctx, name)
		if err != nil {
			return fmt.Errorf("%s 搜索飞牛演员失败：%w", name, err)
		}
		for _, p := range found {
			if exactNameMatch(p, name) {
				matches = append(matches, p)
			}
		}
	}
	if len(matches) == 0 {
		fmt.Println("  [SKIP] 飞牛中没有同名演员档案（本程序不会新建档案）")
		return nil
	}
	if len(matches) > 1 {
		fmt.Printf("  [SKIP] 找到 %d 个同名档案，无法安全判断目标\n", len(matches))
		return nil
	}
	detail, err := client.GetEditDetail(ctx, matches[0].GUID)
	if err != nil {
		return fmt.Errorf("%s 读取飞牛档案失败：%w", name, err)
	}
	if !isLocalPerson(detail) {
		fmt.Println("  [SKIP] 该档案不是可安全修改的本地演员资料（官方/在线资料受保护）")
		return nil
	}
	hasBio := strings.TrimSpace(detail.Biography) != ""
	hasImage := strings.TrimSpace(detail.ProfilePath) != ""
	needsBio := o.overwrite || !hasBio
	needsImage := o.overwrite || !hasImage
	canTryBio := needsBio && !detail.BiographyLocked
	canTryImage := needsImage && !detail.ProfilePathLocked
	if !canTryBio && !canTryImage {
		printExistingFieldSkip(detail, hasBio, hasImage, o.overwrite)
		return nil
	}
	profile, scrapeErr := cachedScrape(ctx, providers, name, item.lookupAliases, o.cache, o.refresh)
	if profile == nil || len(portraitCandidates(profile)) == 0 && profile.Biography == "" {
		wanted := make([]string, 0, 2)
		if canTryImage {
			wanted = append(wanted, "头像")
		}
		if canTryBio {
			wanted = append(wanted, "简介")
		}
		fmt.Printf("  [INFO] 来源未找到可用%s；已有字段保持不变\n", strings.Join(wanted, "和"))
		if isRetryableUpstreamError(scrapeErr) {
			return scrapeErr
		}
		return nil
	}
	canBio := canTryBio && strings.TrimSpace(profile.Biography) != ""
	canImage := canTryImage && len(portraitCandidates(profile)) > 0
	if !canBio && !canImage {
		printNoWritableFields(detail, profile, hasBio, hasImage, o.overwrite)
		if isRetryableUpstreamError(scrapeErr) {
			return scrapeErr
		}
		return nil
	}
	var image []byte
	imageSource := ""
	retryErr := scrapeErr
	if !isRetryableUpstreamError(retryErr) {
		retryErr = nil
	}
	if canImage {
		for _, candidate := range portraitCandidates(profile) {
			image, err = fetchPortrait(ctx, upstream, candidate.URL)
			if err != nil {
				fmt.Printf("  来源 %s 的头像处理失败，尝试下一来源：%v\n", candidate.Source, err)
				if retryErr == nil && isRetryableUpstreamError(err) {
					retryErr = err
				}
				continue
			}
			imageSource = candidate.Source
			break
		}
		if len(image) == 0 {
			canImage = false
			fmt.Println("  [SKIP] 所有头像来源均处理失败，跳过头像")
			if !canBio {
				fmt.Printf("  [SKIP] 简介无法更新：%s；跳过此演员\n", biographyBlockReason(detail, profile, hasBio, o.overwrite))
				if retryErr != nil {
					return retryErr
				}
				return nil
			}
		}
	}
	usedSources := make([]string, 0, 2)
	if canImage && imageSource != "" {
		usedSources = append(usedSources, imageSource)
	}
	if canBio {
		usedSources = append(usedSources, firstNonempty(profile.BiographySource, "简介来源"))
	}
	fmt.Printf("  [SOURCE] %s\n\n  [PLAN] 将更新：%s\n", strings.Join(unique(usedSources), ", "), strings.Join(nonempty([]string{map[bool]string{true: "头像"}[canImage], map[bool]string{true: "简介"}[canBio]}), "、"))
	if o.apply {
		var profilePath *string
		if len(image) > 0 {
			uploaded, err := client.UploadProfile(ctx, image)
			if err != nil {
				return err
			}
			profilePath = &uploaded
		}
		var biography *string
		if canBio {
			biography = &profile.Biography
		}
		if err := client.SaveProfile(ctx, detail, name, biography, profilePath); err != nil {
			return err
		}
		fmt.Println("  [OK] 已写入飞牛演员档案")
	}
	return retryErr
}

func portraitCandidates(profile *ActorProfile) []PortraitCandidate {
	if profile == nil {
		return nil
	}
	if len(profile.ImageCandidates) > 0 {
		return profile.ImageCandidates
	}
	if strings.TrimSpace(profile.ImageURL) == "" {
		return nil
	}
	return []PortraitCandidate{{URL: profile.ImageURL, Source: firstNonempty(strings.Join(profile.SourceNames, ", "), "缓存来源")}}
}

func printNoWritableFields(detail FnPerson, profile *ActorProfile, hasBio, hasImage, overwrite bool) {
	if hasBio && hasImage && !overwrite {
		fmt.Println("  [SKIP] 头像和简介都已存在；默认只补缺失字段")
		return
	}
	blocked := make([]string, 0, 2)
	if reason := biographyBlockReason(detail, profile, hasBio, overwrite); reason != "" {
		blocked = append(blocked, "简介"+reason)
	}
	if reason := imageBlockReason(detail, profile, hasImage, overwrite); reason != "" {
		blocked = append(blocked, "头像"+reason)
	}
	if len(blocked) == 0 {
		fmt.Println("  [SKIP] 没有可补充的缺失字段")
		return
	}
	fmt.Printf("  [SKIP] 没有可写入的缺失字段（%s）\n", strings.Join(blocked, "；"))
}

func printExistingFieldSkip(detail FnPerson, hasBio, hasImage, overwrite bool) {
	blocked := make([]string, 0, 2)
	if detail.BiographyLocked {
		blocked = append(blocked, "简介字段已锁定")
	} else if hasBio && !overwrite {
		blocked = append(blocked, "简介已有内容")
	}
	if detail.ProfilePathLocked {
		blocked = append(blocked, "头像字段已锁定")
	} else if hasImage && !overwrite {
		blocked = append(blocked, "头像已有内容")
	}
	fmt.Printf("  [SKIP] 没有缺失且未锁定的可写字段（%s）\n", strings.Join(blocked, "；"))
}

func biographyBlockReason(detail FnPerson, profile *ActorProfile, exists, overwrite bool) string {
	if detail.BiographyLocked {
		return "字段已锁定"
	}
	if exists && !overwrite {
		return "已有内容"
	}
	if strings.TrimSpace(profile.Biography) == "" {
		return "来源未提供资料"
	}
	return ""
}

func imageBlockReason(detail FnPerson, profile *ActorProfile, exists, overwrite bool) string {
	if detail.ProfilePathLocked {
		return "字段已锁定"
	}
	if exists && !overwrite {
		return "已有内容"
	}
	if len(portraitCandidates(profile)) == 0 {
		return "来源未提供资料"
	}
	return ""
}

func cachedScrape(ctx context.Context, providers *ProviderService, name string, lookupAliases []string, cache string, refresh bool) (*ActorProfile, error) {
	revision := providers.lookupRevision(name, lookupAliases...)
	sum := sha256.Sum256([]byte(name))
	file := filepath.Join(cache, "actors", hex.EncodeToString(sum[:])+".json")
	var cachedProfile *ActorProfile
	if !refresh {
		if info, err := os.Stat(file); err == nil && info.Size() <= 1<<20 && time.Since(info.ModTime()) < 30*24*time.Hour {
			if data, err := os.ReadFile(file); err == nil {
				var p ActorProfile
				if json.Unmarshal(data, &p) == nil && p.LookupRevision == revision && normalizeName(p.Name) == normalizeName(name) && (len(p.ImageCandidates) > 0 || strings.TrimSpace(p.ImageURL) == "") {
					cachedProfile = &p
					if hasPortraitAndBiography(&p) || time.Since(info.ModTime()) < 7*24*time.Hour {
						return &p, nil
					}
				}
			}
		}
	}
	profile, err := providers.ScrapeWithAliases(ctx, name, lookupAliases...)
	if cachedProfile != nil {
		profile = mergeActorProfiles(name, cachedProfile, profile)
	}
	if profile == nil || isRetryableUpstreamError(err) {
		return profile, err
	}
	profile.LookupRevision = revision
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return profile, fmt.Errorf("创建演员缓存目录失败：%w", err)
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return profile, err
	}
	if err := writeFileAtomic(file, data, 0o640); err != nil {
		return profile, fmt.Errorf("写入演员缓存失败：%w", err)
	}
	return profile, err
}

func probe(ctx context.Context, name string, o options) error {
	started := time.Now()
	upstream := NewUpstream()
	providers := NewProviderService(upstream, o.cache, o.refresh)
	defer providers.Close()
	if err := providers.loadNameAliases(); err != nil {
		return err
	}
	profile, scrapeErr := providers.Scrape(ctx, name)
	if profile == nil {
		if isRetryableUpstreamError(scrapeErr) {
			return scrapeErr
		}
		fmt.Printf("没有找到可用资料（耗时 %s）\n", time.Since(started).Round(time.Millisecond))
		return nil
	}
	fmt.Printf("来源：%s\n简介：%t\n", strings.Join(profile.SourceNames, ", "), strings.TrimSpace(profile.Biography) != "")
	candidates := portraitCandidates(profile)
	if len(candidates) == 0 {
		fmt.Println("头像：未找到")
	} else {
		found := false
		for _, candidate := range candidates {
			portrait, err := fetchPortrait(ctx, upstream, candidate.URL)
			if err != nil {
				fmt.Printf("来源 %s 头像处理失败，尝试下一来源：%v\n", candidate.Source, err)
				continue
			}
			fmt.Printf("头像处理成功，使用来源 %s：JPEG %d 字节\n", candidate.Source, len(portrait))
			found = true
			break
		}
		if !found {
			fmt.Println("所有头像来源均失败；单张头像错误不会终止批量任务")
		}
	}
	var stats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&stats)
	fmt.Printf("诊断完成：耗时 %s，GC 后 Go 堆占用 %.1f MiB，Go 运行时保留内存 %.1f MiB\n", time.Since(started).Round(time.Millisecond), float64(stats.HeapAlloc)/1048576, float64(stats.Sys)/1048576)
	if isRetryableUpstreamError(scrapeErr) {
		return scrapeErr
	}
	return nil
}

func exactNameMatch(person FnPerson, name string) bool {
	return normalizeName(person.Name) == normalizeName(name) || normalizeName(person.OriginalName) == normalizeName(name)
}
func isLocalPerson(person FnPerson) bool {
	return !person.IsOfficial && strings.HasPrefix(strings.ToUpper(person.TrimID), "LOCAL_PERSON_") && person.TMDBID == 0 && person.IMDBID == ""
}
func validateFnOSURL(value string) error {
	if strings.HasPrefix(value, "https://") {
		return nil
	}
	if strings.HasPrefix(value, "http://127.0.0.1:") || strings.HasPrefix(value, "http://localhost:") {
		return nil
	}
	return fmt.Errorf("FNOS_URL 必须使用 HTTPS（HTTP 仅允许本机 localhost/127.0.0.1）")
}
func nonempty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
func parseMemoryLimit(value string) (int64, error) {
	var number int64
	var unit string
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d%s", &number, &unit); err != nil {
		return 0, err
	}
	multiplier := int64(1)
	switch strings.ToLower(unit) {
	case "kib", "k":
		multiplier = 1 << 10
	case "mib", "m":
		multiplier = 1 << 20
	case "gib", "g":
		multiplier = 1 << 30
	case "":
	}
	return number * multiplier, nil
}
