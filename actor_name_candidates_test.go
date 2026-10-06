package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfirmedAliasesAndGlyphCandidates(t *testing.T) {
	for name, expected := range map[string]string{"七泽美亚": "七沢みあ", "桥本有菜": "橋本ありな", "佐仓宁宁": "佐倉ねね", "伊贺まこ": "伊賀まこ"} {
		if !containsExact(actorLookupNames(name), expected) {
			t.Fatalf("%s 未包含 %s", name, expected)
		}
	}
}

func TestConfiguredAliasesRevision(t *testing.T) {
	dir := t.TempDir()
	service := NewProviderService(nil, dir, false)
	if err := service.loadNameAliases(); err != nil {
		t.Fatal(err)
	}
	before := service.lookupRevision("测试演员")
	path := filepath.Join(dir, "actor-aliases.json")
	if err := os.WriteFile(path, []byte(`{"测试演员":["日文テスト"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.loadNameAliases(); err != nil {
		t.Fatal(err)
	}
	if !containsExact(service.lookupNames("测试演员"), "日文テスト") {
		t.Fatal("用户别名没有进入查询")
	}
	if before == service.lookupRevision("测试演员") {
		t.Fatal("配置变化没有失效旧检索版本")
	}
	if err := os.WriteFile(path, []byte(`{"测试演员":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.loadNameAliases(); err == nil {
		t.Fatal("无效配置没有报错")
	}
	if !containsExact(service.lookupNames("测试演员"), "日文テスト") {
		t.Fatal("无效配置破坏旧映射")
	}
}
