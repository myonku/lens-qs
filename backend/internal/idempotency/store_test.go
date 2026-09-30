package idempotency

import (
	"context"
	"testing"
	"time"

	"github.com/myonku/lens-qs-api/internal/store"
)

func TestBeginCompleteAndReplay(t *testing.T) {
	db := store.NewTestDB(t)
	st := NewStore(db, "sqlite", time.Hour)
	ctx := context.Background()

	body := []byte(`{"record":{"process_code":"一次加硬"},"client_request_id":"abc-123"}`)
	method, path := "POST", "/api/forms/M-1/records"

	// 首次：未命中
	replay, err := st.Begin(ctx, "user-1", "abc-123", method, path, body)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if replay != nil {
		t.Fatal("首次请求不应命中幂等记录")
	}

	// 记录首次响应
	respBody := []byte(`{"code":"ok","data":{"action":"create_record"}}`)
	if err := st.Complete(ctx, "user-1", "abc-123", method, path, body, 200, "", respBody); err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}

	// 相同键 + 相同请求体：命中并回放
	replay, err = st.Begin(ctx, "user-1", "abc-123", method, path, body)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if replay == nil {
		t.Fatal("相同幂等键应命中")
	}
	if !replay.SameRequest {
		t.Error("请求体相同，SameRequest 应为 true")
	}
	if replay.StatusCode != 200 || string(replay.Body) != string(respBody) {
		t.Fatalf("回放内容不正确: status=%d body=%s", replay.StatusCode, replay.Body)
	}

	// 相同键但请求体不同：命中但 SameRequest=false（调用方应返回 409）
	other := []byte(`{"record":{"process_code":"二次加硬"},"client_request_id":"abc-123"}`)
	replay, err = st.Begin(ctx, "user-1", "abc-123", method, path, other)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if replay == nil {
		t.Fatal("相同幂等键应命中")
	}
	if replay.SameRequest {
		t.Error("请求体不同，SameRequest 应为 false")
	}
}

func TestIdempotencyScopedByUser(t *testing.T) {
	db := store.NewTestDB(t)
	st := NewStore(db, "sqlite", time.Hour)
	ctx := context.Background()
	body := []byte(`{"x":1}`)

	if err := st.Complete(ctx, "user-A", "k1", "POST", "/api/forms", body, 200, "", []byte(`{"a":1}`)); err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	// 另一个用户用同样的键：不应互相影响
	replay, err := st.Begin(ctx, "user-B", "k1", "POST", "/api/forms", body)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if replay != nil {
		t.Fatal("幂等键必须按用户隔离，不同用户不应命中同一条记录")
	}
}

func TestBeginWithoutKeyOrUserIsNoop(t *testing.T) {
	db := store.NewTestDB(t)
	st := NewStore(db, "sqlite", time.Hour)
	ctx := context.Background()

	for _, tc := range []struct{ user, key string }{
		{"", "k"}, {"u", ""}, {"", ""},
	} {
		replay, err := st.Begin(ctx, tc.user, tc.key, "POST", "/api/x", nil)
		if err != nil || replay != nil {
			t.Fatalf("缺用户或键时应为 no-op，得到 replay=%v err=%v", replay, err)
		}
		if err := st.Complete(ctx, tc.user, tc.key, "POST", "/api/x", nil, 200, "", nil); err != nil {
			t.Fatalf("缺用户或键时 Complete 应为 no-op: %v", err)
		}
	}
}

func TestExpiredRecordIsPurged(t *testing.T) {
	db := store.NewTestDB(t)
	st := NewStore(db, "sqlite", time.Hour)
	ctx := context.Background()
	body := []byte(`{"x":1}`)

	if err := st.Complete(ctx, "u1", "k-old", "POST", "/api/x", body, 200, "", []byte(`{}`)); err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	// 手工把过期时间改到过去，模拟 TTL 到期
	past := store.FormatTime(time.Now().Add(-2 * time.Hour))
	if _, err := db.ExecContext(ctx, db.Rebind(
		`UPDATE idempotency_requests SET expires_at = ? WHERE idempotency_key = ?`), past, "k-old"); err != nil {
		t.Fatalf("更新过期时间失败: %v", err)
	}

	// Begin 会顺手清理过期记录，因此不应命中
	replay, err := st.Begin(ctx, "u1", "k-old", "POST", "/api/x", body)
	if err != nil {
		t.Fatalf("Begin 失败: %v", err)
	}
	if replay != nil {
		t.Fatal("已过期的幂等记录不应命中")
	}

	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM idempotency_requests`); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("过期记录应被清理，仍有 %d 条", n)
	}
}

func TestCompleteIsIdempotentUnderRace(t *testing.T) {
	db := store.NewTestDB(t)
	st := NewStore(db, "sqlite", time.Hour)
	ctx := context.Background()
	body := []byte(`{"x":1}`)

	// 同一个键写两次（模拟并发重试）：第二次不应报错，先写入者胜出
	if err := st.Complete(ctx, "u1", "k", "POST", "/api/x", body, 200, "", []byte(`{"first":true}`)); err != nil {
		t.Fatalf("首次 Complete 失败: %v", err)
	}
	if err := st.Complete(ctx, "u1", "k", "POST", "/api/x", body, 200, "", []byte(`{"second":true}`)); err != nil {
		t.Fatalf("重复 Complete 不应报错: %v", err)
	}

	replay, err := st.Begin(ctx, "u1", "k", "POST", "/api/x", body)
	if err != nil || replay == nil {
		t.Fatalf("应命中首次记录: replay=%v err=%v", replay, err)
	}
	if string(replay.Body) != `{"first":true}` {
		t.Fatalf("应保留首次响应，得到 %s", replay.Body)
	}
}

func TestHashBodyDistinguishesPayload(t *testing.T) {
	a := HashBody("POST", "/api/x", []byte(`{"a":1}`))
	b := HashBody("POST", "/api/x", []byte(`{"a":2}`))
	c := HashBody("PATCH", "/api/x", []byte(`{"a":1}`))
	d := HashBody("POST", "/api/y", []byte(`{"a":1}`))
	if a == b || a == c || a == d {
		t.Fatal("不同方法/路径/请求体应得到不同指纹")
	}
	if a != HashBody("post", "/api/x", []byte(`{"a":1}`)) {
		t.Fatal("方法名应大小写不敏感")
	}
}
