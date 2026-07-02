package main

import (
	"context"
	"log"
	"time"

	"subscription-service/internal/quota"
	"subscription-service/internal/worker"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func main() {
	app := fiber.New()
	
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", 
		DB:       0,
	})
	
	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalf("Cannot connect to Redis: %v", err)
	}
	log.Println("Connected to Redis successfully!")

	// Chạy Kafka Consumer ngầm trên 1 Goroutine
	go worker.StartUsageSyncWorker()

	app.Post("/api/v1/features/check-access", func(c *fiber.Ctx) error {
		// Mock parse JWT Token
		merchantID := c.Query("merchant_id", "merchant_123")
		feature := "MAX_ORDERS"
		
		// Giả sử API tự động đọc từ L1 Cache (RAM) được Max Quota (gốc + thưởng) là 1000
		maxQuota := 1000
		today := time.Now().Format("2006-01-02")
		
		// Luồng Fast Path: Check & Tăng quota ngay lập tức trên Redis bằng Lua Script
		currentUsage, err := quota.CheckAndIncrQuota(c.Context(), rdb, merchantID, feature, maxQuota, today)
		if err != nil && err.Error() == "QUOTA_EXCEEDED" {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"status": "error",
				"reason": "QUOTA_EXCEEDED",
				"message": "Bạn đã hết hạn mức tạo đơn hàng hôm nay.",
			})
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		
		return c.Status(200).JSON(fiber.Map{
			"status": "success",
			"is_allowed": true,
			"current_usage": currentUsage,
			"max_quota": maxQuota,
		})
	})

	log.Println("Subscription Service is running on port 3001...")
	log.Fatal(app.Listen(":3001"))
}
