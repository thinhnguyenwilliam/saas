package worker

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UsageTracking mapping với table dưới DB
type UsageTracking struct {
	MerchantID   string `gorm:"primaryKey"`
	Feature      string `gorm:"primaryKey"`
	PeriodDate   string `gorm:"primaryKey"`
	CurrentUsage int
}

// OrderEvent payload nhận từ Kafka
type OrderEvent struct {
	MerchantID string `json:"merchant_id"`
	EventType  string `json:"event_type"`
	Timestamp  int64  `json:"timestamp"`
}

var db *gorm.DB

func initDB() {
	dsn := "host=localhost user=root password=secretpassword dbname=saas_merchant port=5432 sslmode=disable TimeZone=Asia/Ho_Chi_Minh"
	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Cannot connect to Postgres: %v", err)
	}

	log.Println("[Worker] Đã kết nối DB và Tự động tạo bảng usage_trackings (AutoMigrate)...")
	db.AutoMigrate(&UsageTracking{})
}

// StartUsageSyncWorker chạy một con Worker bất đồng bộ
func StartUsageSyncWorker() {
	initDB()

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:   []string{"localhost:9092"},
		Topic:     "usage_updates",
		Partition: 0,
		MaxBytes:  10e6, // 10MB
	})

	log.Println("[Worker] Đang lắng nghe Kafka Topic 'usage_updates'...")

	eventChan := make(chan OrderEvent, 1000)

	// Goroutine 1: Chỉ làm nhiệm vụ đọc liên tục từ Kafka và nhét vào Channel
	go func() {
		for {
			m, err := r.ReadMessage(context.Background())
			if err != nil {
				log.Printf("[Worker] Lỗi đọc message Kafka: %v\n", err)
				time.Sleep(2 * time.Second)
				continue
			}

			var event OrderEvent
			if err := json.Unmarshal(m.Value, &event); err == nil {
				eventChan <- event
			}
		}
	}()

	// Goroutine 2: Chuyên xử lý Gom nhóm (Batching)
	// Cứ mỗi 3 giây sẽ gom toàn bộ request lại và đẩy xuống DB 1 lần
	ticker := time.NewTicker(3 * time.Second)
	
	// Map đếm số lượng tăng thêm. Key: merchantID, Value: số đơn hàng cần cộng thêm
	batchCounts := make(map[string]int)
	var mapMutex sync.Mutex

	for {
		select {
		// Bất kỳ khi nào có event mới bay vào, ta lấy nó ra và tăng biến đếm trong RAM
		case event := <-eventChan:
			mapMutex.Lock()
			batchCounts[event.MerchantID]++
			mapMutex.Unlock()

		// Cứ mỗi 3 giây, chuông đồng hồ reng, ta bốc toàn bộ biến đếm trong RAM ghi xuống DB
		case <-ticker.C:
			mapMutex.Lock()
			if len(batchCounts) == 0 {
				mapMutex.Unlock()
				continue // Bỏ qua nếu 3s qua không ai mua hàng
			}

			// Copy dữ liệu ra biến tạm để nhả Lock ngay lập tức (Giúp Channel tiếp tục nhận đơn)
			countsToUpdate := make(map[string]int)
			for k, v := range batchCounts {
				countsToUpdate[k] = v
			}
			// Reset lại map trống cho chu kỳ 3s tiếp theo
			batchCounts = make(map[string]int)
			mapMutex.Unlock()

			// Gọi hàm ghi xuống Postgres
			flushToDB(countsToUpdate)
		}
	}
}

// flushToDB Ghi nguyên một cục (batch) xuống Database cực nhanh bằng kỹ thuật Upsert
func flushToDB(counts map[string]int) {
	log.Printf("[Worker] Bắt đầu Batch Update xuống Postgres cho %d merchant(s)...\n", len(counts))
	
	today := time.Now().Format("2006-01-02")
	feature := "MAX_ORDERS"

	for merchantID, increment := range counts {
		usage := UsageTracking{
			MerchantID:   merchantID,
			Feature:      feature,
			PeriodDate:   today,
			CurrentUsage: increment,
		}

		// Kỹ thuật Upsert (Insert on Conflict Update)
		// Nếu merchant này hôm nay chưa mua gì -> CREATE dòng mới với số lượng hiện tại
		// Nếu đã mua rồi (bị Conflict PK) -> UPDATE số lượng hiện tại = số đang có + số vừa gom được
		err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "merchant_id"}, {Name: "feature"}, {Name: "period_date"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"current_usage": gorm.Expr("usage_trackings.current_usage + ?", increment),
			}),
		}).Create(&usage).Error

		if err != nil {
			log.Printf("[Lỗi DB] Không thể cập nhật usage cho merchant %s: %v\n", merchantID, err)
		} else {
			log.Printf("[DB] Đã cộng thêm thành công %d đơn cho merchant %s!\n", increment, merchantID)
		}
	}
}
