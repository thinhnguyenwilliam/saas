package quota

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
)

// CheckAndIncrScript là đoạn Lua Script chạy Atomic trên Redis.
// Nó gộp quá trình Đọc (GET), So sánh (< limit) và Tăng (INCR) thành một thao tác duy nhất.
var CheckAndIncrScript = redis.NewScript(`
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local limit = tonumber(ARGV[1])

if current >= limit then
    return -1
else
    redis.call("INCR", KEYS[1])
    return current + 1
end
`)

func CheckAndIncrQuota(ctx context.Context, client *redis.Client, merchantID string, feature string, limit int, date string) (int, error) {
	key := fmt.Sprintf("usage:%s:%s:%s", merchantID, feature, date)
	
	result, err := CheckAndIncrScript.Run(ctx, client, []string{key}, limit).Result()
	if err != nil {
		return 0, err
	}
	
	val, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected script return type")
	}
	
	if val == -1 {
		return -1, fmt.Errorf("QUOTA_EXCEEDED")
	}
	
	return int(val), nil
}
