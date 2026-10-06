package main

import "strings"

// confirmedActorAliases contains Japanese spellings that were confirmed to
// return usable portraits from the configured providers. Keep this list
// explicit: automatic character conversion can silently point at another
// person, while these entries are tied to the exact FnOS display name.
var confirmedActorAliases = map[string][]string{
	"碓冰莲":   {"碓氷れん"},
	"七泽美亚":  {"七沢みあ"},
	"三咲美忧":  {"三咲美憂"},
	"三宫椿":   {"三宮つばき"},
	"东云みれい": {"東雲みれい"},
	"二叶惠麻":  {"二葉エマ"},
	"仓本堇":   {"倉本すみれ"},
	"优木あおい": {"優木あおい"},
	"佐仓宁宁":  {"佐倉ねね"},
	"佐山爱":   {"佐山愛"},
	"佳苗琉华":  {"佳苗るか"},
	"凉森玲梦":  {"涼森れむ"},
	"儿玉玲奈":  {"児玉れな"},
	"君岛美绪":  {"君島みお"},
	"大槻响":   {"大槻ひびき"},
	"枫可怜":   {"楓カレン"},
	"相泽南":   {"相沢みなみ"},
	"浜崎真绪":  {"浜崎真緒"},
	"桥本有菜":  {"橋本ありな"},
	"梦乃爱华":  {"夢乃あいか"},
	"水卜樱":   {"水卜さくら"},
	"高桥圣子":  {"高橋しょう子"},
	"杏树纱奈":  {"杏樹紗奈"},
	"永井玛丽亚": {"永井マリア"},
	"园田美樱":  {"園田みおん"},
}

func actorLookupNames(name string, additional ...string) []string {
	names := []string{name}
	names = append(names, additional...)
	for key, aliases := range confirmedActorAliases {
		if normalizeName(key) == normalizeName(name) {
			names = append(names, aliases...)
			break
		}
	}

	uniqueNames := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, candidate := range names {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		key := normalizeName(candidate)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		uniqueNames = append(uniqueNames, candidate)
	}
	return uniqueNames
}

func hasAlternateActorLookup(name string, additional ...string) bool {
	return len(actorLookupNames(name, additional...)) > 1
}
