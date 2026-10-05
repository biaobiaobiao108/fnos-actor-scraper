package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxNFOSize = 5 << 20

func parseNFO(reader io.Reader) ([]string, error) {
	decoder := xml.NewDecoder(reader)
	var names []string
	inActor, inName, depth := false, false, 0
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return unique(names), nil
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if strings.EqualFold(value.Name.Local, "actor") {
				inActor = true
			}
			if inActor && strings.EqualFold(value.Name.Local, "name") {
				inName = true
				text.Reset()
			}
		case xml.CharData:
			if inActor && inName {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			if inActor && inName && strings.EqualFold(value.Name.Local, "name") {
				if name := strings.TrimSpace(text.String()); name != "" {
					names = append(names, name)
				}
				inName = false
			}
			if strings.EqualFold(value.Name.Local, "actor") {
				inActor, inName = false, false
			}
			depth--
		}
		if depth < 0 {
			return nil, fmt.Errorf("无效 NFO XML")
		}
	}
}

func scanNFO(root string) (map[string]struct {
	name  string
	count int
}, error) {
	actors := make(map[string]struct {
		name  string
		count int
	})
	visited := make(map[string]struct{})
	var walk func(string) error
	walk = func(path string) error {
		info, err := os.Lstat(path)
		if err != nil {
			fmt.Printf("跳过无法读取路径 %s：%v\n", path, err)
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(path)
			if err != nil {
				fmt.Printf("跳过失效符号链接 %s：%v\n", path, err)
				return nil
			}
		}
		if info.IsDir() {
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				fmt.Printf("跳过无法解析的目录 %s：%v\n", path, err)
				return nil
			}
			realPath, err = filepath.Abs(realPath)
			if err == nil {
				if _, exists := visited[realPath]; exists {
					return nil
				}
				visited[realPath] = struct{}{}
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				fmt.Printf("跳过无法读取目录 %s：%v\n", path, err)
				return nil
			}
			for _, entry := range entries {
				if err := walk(filepath.Join(path, entry.Name())); err != nil {
					return err
				}
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".nfo") {
			return nil
		}
		if info.Size() > maxNFOSize {
			fmt.Printf("跳过过大的 NFO：%s\n", path)
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			fmt.Printf("跳过无法读取的 NFO %s：%v\n", path, err)
			return nil
		}
		names, parseErr := parseNFO(io.LimitReader(file, maxNFOSize+1))
		closeErr := file.Close()
		if parseErr != nil {
			fmt.Printf("跳过无法解析的 NFO %s：%v\n", path, parseErr)
			return nil
		}
		if closeErr != nil {
			fmt.Printf("关闭 NFO 文件失败 %s：%v\n", path, closeErr)
		}
		for _, name := range names {
			key := normalizeName(name)
			prior := actors[key]
			if prior.name == "" {
				prior.name = name
			}
			prior.count++
			actors[key] = prior
		}
		return nil
	}
	if err := walk(root); err != nil {
		return actors, err
	}
	return actors, nil
}
