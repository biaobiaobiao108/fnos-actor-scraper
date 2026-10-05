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

const watchStateVersion = 1

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
	if !exists {
		current, err := collectTasks(o)
		if err != nil {
			return err
		}
		state = watchState{Version: watchStateVersion, Seen: make(map[string]bool, len(current))}
		for _, item := range current {
			if key := taskIdentity(item); key != "" {
				state.Seen[key] = true
			}
		}
		if err := writeWatchState(statePath, state); err != nil {
			return err
		}
		fmt.Printf("监控基线已建立：现有 %d 个演员不会被处理；之后新增的演员会自动处理。状态文件：%s\n", len(state.Seen), statePath)
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
	fmt.Printf("演员监控已启动，轮询间隔 %s；按 Ctrl+C 停止。\n", o.watchInterval)
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
	fmt.Printf("发现 %d 个新演员，开始自动处理。\n", len(newTasks))
	for _, item := range newTasks {
		if err := processActor(ctx, client, providers, upstream, item, o); err != nil {
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
		return watchState{}, true, fmt.Errorf("监控状态文件 %s 无效：%w；请检查文件或移走后重新建立基线", path, err)
	}
	if state.Version != watchStateVersion || state.Seen == nil {
		return watchState{}, true, fmt.Errorf("监控状态文件 %s 版本不兼容；请检查文件或移走后重新建立基线", path)
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
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("写入监控状态失败：%w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("更新监控状态失败：%w", err)
	}
	return nil
}
