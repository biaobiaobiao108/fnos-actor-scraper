package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchLegacyMigrationAndReevaluation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE watch_actor_state(guid TEXT PRIMARY KEY, actor_name TEXT, status TEXT, attempts INTEGER, last_attempt_at INTEGER, next_attempt_at INTEGER, last_error TEXT, updated_at INTEGER);
INSERT INTO watch_actor_state VALUES('g','旧名','done',0,0,0,'',0)`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWatchStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Unix(2000000000, 0)
	due, err := store.ShouldProcess(ctx, "g", now, "v2", true)
	if err != nil || !due {
		t.Fatalf("旧版记录没有重评估：%v %v", due, err)
	}
	if err := store.MarkDone(ctx, "g", "旧名", now, "v2"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		elapsed       time.Duration
		revision      string
		missing, want bool
	}{
		{time.Minute, "v2", true, false}, {7 * 24 * time.Hour, "v2", true, true},
		{8 * 24 * time.Hour, "v2", false, false}, {time.Minute, "v3", false, true},
	} {
		due, err := store.ShouldProcess(ctx, "g", now.Add(test.elapsed), test.revision, test.missing)
		if err != nil || due != test.want {
			t.Fatalf("重评估策略不符：%+v due=%v err=%v", test, due, err)
		}
	}
}

func TestWatchRetryBackoffAndRevisionReset(t *testing.T) {
	ctx := context.Background()
	store, err := openWatchStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Unix(2000000000, 0)
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute} {
		delay, err := store.MarkFailure(ctx, "g", "演员", errors.New("暂时错误"), now, "v2")
		if err != nil || delay != want {
			t.Fatalf("重试%d delay=%v err=%v", i, delay, err)
		}
	}
	due, err := store.ShouldProcess(ctx, "g", now, "v2", true)
	if err != nil || due {
		t.Fatalf("未遵守退避：%v %v", due, err)
	}
	delay, err := store.MarkFailure(ctx, "g", "演员", errors.New("新规则失败"), now, "v3")
	if err != nil || delay != time.Minute {
		t.Fatalf("新规则没有重置退避：%v %v", delay, err)
	}
}
