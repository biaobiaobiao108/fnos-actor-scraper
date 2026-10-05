package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func runWatch(o options) error {
	statePath := filepath.Join(o.cache, "fnactor-state.db")
	store, err := openWatchStore(statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	fmt.Printf("监控状态数据库：%s。数据库中尚无记录的演员会从全库扫描；失败任务按退避间隔重试。\n", statePath)

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
		if err := processNewActors(ctx, client, providers, upstream, o, store); err != nil {
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

func processNewActors(ctx context.Context, client *FnOSClient, providers *ProviderService, upstream *Upstream, o options, store *watchStore) error {
	current, err := collectTasks(o)
	if err != nil {
		return err
	}
	dueTasks := make([]task, 0)
	now := time.Now()
	for _, item := range current {
		key := taskIdentity(item)
		if key == "" {
			continue
		}
		due, err := store.ShouldProcess(ctx, key, now)
		if err != nil {
			return err
		}
		if due {
			dueTasks = append(dueTasks, item)
		}
	}
	if len(dueTasks) == 0 {
		return nil
	}
	fmt.Printf("发现 %d 个待处理或到达重试时间的演员，开始自动处理。\n", len(dueTasks))
	for _, item := range dueTasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := processActor(ctx, client, providers, upstream, item, o); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			retryAfter, stateErr := store.MarkFailure(ctx, taskIdentity(item), item.name, err, time.Now())
			if stateErr != nil {
				return stateErr
			}
			fmt.Fprintf(os.Stderr, "%s 处理失败，将在 %s 后重试：%v\n", item.name, retryAfter.Round(time.Second), err)
			continue
		}
		if err := store.MarkDone(ctx, taskIdentity(item), item.name, time.Now()); err != nil {
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
