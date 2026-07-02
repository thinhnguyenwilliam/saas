package main

import (
	"encoding/json"
	"log"
	"net/http"
	"order-service/internal/kafka"
	"time"

	"github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New()
	
	// Khởi tạo Kafka Producer
	kafka.InitProducer()

	app.Post("/api/v1/orders", func(c *fiber.Ctx) error {
		merchantID := c.Query("merchant_id", "merchant_123")
		
		// Gọi API sang Subscription Service để check quyền (Fast Path - Redis Lua Script)
		subURL := "http://localhost:3001/api/v1/features/check-access?merchant_id=" + merchantID
		res, err := http.Post(subURL, "application/json", nil)
		if err != nil {
			log.Printf("[Lỗi] Không thể kết nối tới Subscription Service: %v", err)
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": "Hệ thống gián đoạn, vui lòng thử lại sau!"})
		}
		defer res.Body.Close()

		// Nếu Quota đã hết, hệ thống bên kia trả về 429
		if res.StatusCode == 429 {
			return c.Status(429).JSON(fiber.Map{
				"status": "error",
				"message": "Đã đạt giới hạn số đơn hàng trong ngày (Quota Exceeded). Vui lòng nâng cấp gói Pro!",
			})
		} else if res.StatusCode != 200 {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": "Có lỗi xảy ra khi kiểm tra quyền hạn"})
		}
		
		// Giả sử Check Access đã Pass và lưu Order vào Database của Order Service thành công...
		
		// Sau khi Order thành công, Bắn event bất đồng bộ (Slow Path) vào Kafka
		event := map[string]interface{}{
			"merchant_id": merchantID,
			"event_type":  "ORDER_CREATED",
			"timestamp":   time.Now().Unix(),
			"order_id":    time.Now().UnixNano(),
		}
		
		eventBytes, _ := json.Marshal(event)
		
		err := kafka.PublishEvent(c.Context(), eventBytes)
		if err != nil {
			log.Printf("[Lỗi] Không thể gửi event lên Kafka: %v", err)
			// Lưu ý: Không block luồng chính. Đơn hàng vẫn thành công.
		} else {
			log.Printf("Đã bắn event cập nhật Usage lên Kafka: %s", string(eventBytes))
		}
		
		return c.Status(201).JSON(fiber.Map{
			"status": "success",
			"message": "Tạo đơn hàng thành công!",
			"order": event,
		})
	})

	log.Println("Order Service is running on port 4001...")
	log.Fatal(app.Listen(":4001"))
}
