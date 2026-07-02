package kafka

import (
	"context"
	"log"
	"time"

	kgo "github.com/segmentio/kafka-go"
)

var Writer *kgo.Writer

func InitProducer() {
	// Khởi tạo Kafka Producer kết nối tới Broker
	Writer = &kgo.Writer{
		Addr:     kgo.TCP("localhost:9092"),
		Topic:    "usage_updates",
		Balancer: &kgo.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
	}
	log.Println("Kafka Producer initialized")
}

func PublishEvent(ctx context.Context, msg []byte) error {
	err := Writer.WriteMessages(ctx,
		kgo.Message{
			Key:   []byte("order_event"), // Gom nhóm theo Partition
			Value: msg,
		},
	)
	return err
}
