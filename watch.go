package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const watchStateVersion = 2

type watchState struct {
	Version int             `json:"version"`
	Seen    map[string]bool `json:"seen"`
}

func runWatch(o options) error {
	statePath := filepath.Join(o.cache, "watch-state.json")
	state, exists, err := readWatchState(statePath)
	if err != nil {
		return err
	}
	if !exists || state.Version == 1 {
		previousCount := len(state.Seen)
		state = watchState{Version: watchStateVersion, Seen: make(map[string]bool)}
		if err := writeWatchState(statePath, state); err != nil {
			return err
		}
		if exists {
			fmt.Printf("旧版监控状态已重置（旧基线包含 %d 个演员）；本次将从全库重新处理缺失资料。状态文件：%s\n", previousCount, statePath)
		} else {
			fmt.Printf("监控状态已初始化；本次将从全库开始，已有头像/简介会逐字段跳过。状态文件：%s\n", statePath)
		}
	}

	base := strings.TrimSpace(os.Getenv("FNOS_URL"))
	if base == "" {
		return fmt.Errorf("请设置 FNOS_URL，例如 http://127.0.0.1:5666")
	}
	if err := validateFnOSURL(base); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := NewFnOSClient(base, os.Getenv("FNOS_USERNAME"), os.Getenv("FNOS_PASSWORD"), os.Getenv("FNOS_TOKEN"))
	if err := client.Login(ctx); err != nil {
		return err
	}
	upstream := NewUpstream()
	providers := NewProviderService(upstream, o.cache, o.refresh)
	ticker := time.NewTicker(o.watchInterval)
	defer ticker.Stop()
	fmt.Printf("演员监控已启动，首轮检查全库，之后每 %s 检查新增演员；日志输出到 Docker 标准日志。\n", o.watchInterval)
	for {
		if err := processNewActors(ctx, client, providers, upstream, o, &state, statePath); err != nil {
			fmt.Fprintf(os.Stderr, "监控本轮失败：%v\n", err)
		}
		select {
		case <-ctx.Done():
			fmt.Println("演员监控已停止")
			return nil
		case <-ticker.C:
		}
	}
}

func processNewActors(ctx context.Context, client *FnOSClient, providers *ProviderService, upstream *Upstream, o options, state *watchState, statePath string) error {
	current, err := collectTasks(o)
	if err != nil {
		return err
	}
	newTasks := make([]task, 0)
	for _, item := range current {
		key := taskIdentity(item)
		if key != "" && !state.Seen[key] {
			newTasks = append(newTasks, item)
		}
	}
	if len(newTasks) == 0 {
		return nil
	}
	fmt.Printf("发现 %d 个待处理演员，开始自动处理。\n", len(newTasks))
	for _, item := range newTasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := processActor(ctx, client, providers, upstream, item, o); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Keep failed actors unseen so a later poll can retry transient failures.
			fmt.Fprintf(os.Stderr, "%s 处理失败，将在后续轮询重试：%v\n", item.name, err)
			continue
		}
		key := taskIdentity(item)
		if key == "" {
			continue
		}
		state.Seen[key] = true
		if err := writeWatchState(statePath, *state); err != nil {
			delete(state.Seen, key)
			return err
		}
	}
	return nil
}

func taskIdentity(item task) string {
	if item.person == nil {
		return ""
	}
	return firstNonempty(item.person.GUID, item.person.TrimID)
}

func readWatchState(path string) (watchState, bool, error) {
	info, statErr := os.Stat(path)
	if statErr == nil && info.Size() > 16<<20 {
		return watchState{}, true, fmt.Errorf("监控状态文件 %s 超过 16 MiB 安全上限", path)
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return watchState{}, true, fmt.Errorf("检查监控状态文件失败：%w", statErr)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return watchState{}, false, nil
	}
	if err != nil {
		return watchState{}, false, fmt.Errorf("读取监控状态失败：%w", err)
	}
	var state watchState
	if err := json.Unmarshal(data, &state); err != nil {
		return watchState{}, true, fmt.Errorf("监控状态文件 %s 无效：%w；请检查文件或移走后重新开始全库扫描", path, err)
	}
	if state.Version != 1 && state.Version != watchStateVersion || state.Seen == nil {
		return watchState{}, true, fmt.Errorf("监控状态文件 %s 版本不兼容；请检查文件或移走后重新开始全库扫描", path)
	}
	return state, true, nil
}

func writeWatchState(path string, state watchState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("创建监控状态目录失败：%w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("更新监控状态失败：%w", err)
	}
	return nil
}
