package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/yanmingcao/opencc-go"
	"github.com/yanmingcao/opencc-go/pkg/embeddata"
)

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
		fmt.Printf("日文汉字转换器不可用，仅使用演员原名和飞牛原名：%v\n", nameConverterErr)
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

func hasAlternateActorLookup(name string, additionalNames ...string) bool {
	return len(actorLookupNames(name, additionalNames...)) > 1
}
