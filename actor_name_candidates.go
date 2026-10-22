package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yanmingcao/opencc-go"
	"github.com/yanmingcao/opencc-go/pkg/embeddata"
)

const actorMatchingVersion = "3"

// 用户映射是明确确认的姓名对应；不从相似度推断人物。
func (service *ProviderService) loadNameAliases() error {
	file, err := os.Open(filepath.Join(service.cacheDir, "actor-aliases.json"))
	if os.IsNotExist(err) {
		service.nameAliases = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取演员别名配置失败：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("演员别名配置超过 1 MiB")
	}
	var configured map[string][]string
	if err := json.Unmarshal(data, &configured); err != nil {
		return fmt.Errorf("演员别名配置格式错误：%w", err)
	}
	if configured == nil {
		return fmt.Errorf("演员别名配置必须是姓名到名称数组的 JSON 对象")
	}
	aliases := make(map[string][]string, len(configured))
	for name, values := range configured {
		key := normalizeName(name)
		if key == "" || len(values) == 0 || len(values) > 8 {
			return fmt.Errorf("演员别名配置每个姓名必须有 1 到 8 个别名")
		}
		if _, exists := aliases[key]; exists {
			return fmt.Errorf("演员别名配置包含重复的规范化姓名：%s", name)
		}
		for _, value := range values {
			if normalizeName(value) == "" {
				return fmt.Errorf("演员别名配置包含空别名：%s", name)
			}
		}
		aliases[key] = values
	}
	service.nameAliases = aliases
	return nil
}

func (service *ProviderService) lookupNames(name string, additional ...string) []string {
	additional = append([]string(nil), additional...)
	seeds := append([]string{name}, additional...)
	for _, seed := range seeds {
		additional = append(additional, service.nameAliases[normalizeName(seed)]...)
	}
	return actorLookupNames(name, additional...)
}

func (service *ProviderService) lookupRevision(name string, additional ...string) string {
	data, _ := json.Marshal(service.lookupNames(name, additional...))
	sum := sha256.Sum256(append([]byte(actorMatchingVersion+":"+gfriendsIndexVersion+":"), data...))
	return hex.EncodeToString(sum[:])
}

var (
	nameConvertersOnce sync.Once
	nameS2T            *opencc.SimpleConverter
	nameT2JP           *opencc.SimpleConverter
	nameConverterErr   error
)

func actorLookupNames(name string, additionalNames ...string) []string {
	seeds := append([]string{name}, additionalNames...)
	candidates := make([]string, 0, len(seeds)*3)
	seen := make(map[string]bool, len(seeds)*3)
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		key := normalizeName(candidate)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, candidate)
	}
	for _, seed := range seeds {
		add(seed)
	}

	ensureNameConverters()
	if nameConverterErr != nil {
		fmt.Printf("日文汉字转换器不可用，仅使用原名和已确认别名：%v\n", nameConverterErr)
		return candidates
	}
	for _, seed := range seeds {
		seed = strings.TrimSpace(seed)
		if seed == "" {
			continue
		}
		traditional := nameS2T.Convert(seed)
		add(traditional)
		add(nameT2JP.Convert(traditional))
	}
	return candidates
}

func ensureNameConverters() {
	nameConvertersOnce.Do(func() {
		load := func(profile string) (*opencc.SimpleConverter, error) {
			data, err := embeddata.GetConfig(profile)
			if err != nil {
				return nil, err
			}
			return opencc.NewSimpleConverterFromData(data)
		}
		var err error
		nameS2T, err = load("s2t")
		if err != nil {
			nameConverterErr = fmt.Errorf("加载简体转繁体配置：%w", err)
			return
		}
		nameT2JP, err = load("t2jp")
		if err != nil {
			nameConverterErr = fmt.Errorf("加载繁体转日文汉字配置：%w", err)
		}
	})
}
