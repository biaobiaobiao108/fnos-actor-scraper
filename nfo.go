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
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			fmt.Printf("跳过无法读取路径 %s：%v\n", path, walkErr)
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".nfo") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxNFOSize {
			fmt.Printf("跳过过大的或无法读取的 NFO：%s\n", path)
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		names, parseErr := parseNFO(io.LimitReader(file, maxNFOSize+1))
		file.Close()
		if parseErr != nil {
			fmt.Printf("跳过无法解析的 NFO %s：%v\n", path, parseErr)
			return nil
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
	})
	return actors, err
}
