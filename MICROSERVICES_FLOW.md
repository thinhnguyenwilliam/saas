# Kiến trúc Microservices & Sequence Diagrams

Dưới đây là thiết kế chi tiết kiến trúc Microservices cho hệ thống SaaS và Flow/Sequence Diagram cho 2 luồng cực kỳ quan trọng: **Check Access (Kiểm tra phân quyền & Quota)** và **Update Usage (Cập nhật lưu lượng sử dụng)**.

## 1. Thiết kế các Microservices (Microservice Architecture)

Với bài toán lớn, hệ thống được chia thành các service độc lập để scale dễ dàng:

1.  **API Gateway**: Điểm vào duy nhất (Entry point) cho mọi request. Xử lý Rate Limiting, Authentication ban đầu và Routing.
2.  **Identity / Auth Service**: Quản lý User, Role, Permission (RBAC). Cung cấp API để xác thực và cấp JWT Token.
3.  **Tenant / Subscription Service**: Quản lý thông tin Merchant, Store, Gói cước (Plans), Hạn mức (Quotas) và các Hạn mức được thưởng/tặng (Adjustments). Service này sở hữu Redis Cache để check Quota cực nhanh.
4.  **Core Domain Services (vd: Order Service, Product Service)**: Chứa logic nghiệp vụ chính của hệ thống.
5.  **Audit Log Service**: Consume messages từ Kafka để lưu vết lịch sử mọi hành động (đã trình bày ở file trước).

---

## 2. Sequence Diagram: Flow Kiểm tra Quyền và Quota (API Check Access)

**Yêu cầu đặt ra:** Khi user gọi API, cần check xem User có quyền không, và Merchant có còn Quota không. Quota phải tính tổng của **Gói gốc (Plan Limit)** và **Gói thưởng (Bonus/Adjustment Limit)**.

```mermaid
sequenceDiagram
    participant Client
    participant APIGW as API Gateway
    participant Auth as Auth Service
    participant SubSvc as Subscription Service (Redis)
    participant CoreSvc as Order Service

    Client->>APIGW: 1. POST /api/v1/orders (Kèm JWT Token)
    
    %% Lớp 1: Check Role/Permission của User
    APIGW->>Auth: 2. Validate Token & Check Permission (CREATE_ORDER)
    alt Token hết hạn hoặc Không có quyền
        Auth-->>APIGW: 403 Forbidden hoặc 401 Unauthorized
        APIGW-->>Client: Trả về lỗi phân quyền
    end
    Auth-->>APIGW: 3. Token Hợp lệ (Kèm merchant_id, role)

    %% Lớp 2: Check Quota của Tenant (Gói gốc + Gói thưởng)
    APIGW->>SubSvc: 4. Check Quota(merchant_id, feature='MAX_ORDERS')
    
    Note over SubSvc: SubSvc sẽ lấy data từ Redis (Cache) để tối ưu
    SubSvc->>SubSvc: 5a. Lấy current_usage từ Redis
    SubSvc->>SubSvc: 5b. Tính max_quota = Plan Limit (gói gốc) + Bonus Limit (gói thưởng)
    
    alt current_usage >= max_quota
        SubSvc-->>APIGW: 6a. Quota Exceeded (Hết hạn mức)
        APIGW-->>Client: Trả về lỗi 429 Too Many Requests (Hệ thống Read-only)
    end
    SubSvc-->>APIGW: 6b. OK (Còn hạn mức)

    %% Lớp 3: Xử lý nghiệp vụ lõi
    APIGW->>CoreSvc: 7. Forward request tạo Order
    CoreSvc->>CoreSvc: 8. Insert Order vào Database
    CoreSvc-->>APIGW: 9. Order created success
    APIGW-->>Client: 10. 201 Created (Success)
```

**Giải thích logic tối ưu "Gói gốc và Gói thưởng" (Bước 5):**
Để không phải query DB tính toán `max_quota` = "gói gốc" + "gói thưởng" cho mỗi request gây chậm chạp, ta dùng kiến trúc **Event-Driven**:
Mỗi khi Merchant mua gói mới (gói gốc) hoặc Customer Service tặng thêm lượt (gói thưởng), hệ thống tự động tính toán tổng số này và lưu vào Redis.
Dữ liệu Redis lúc này chỉ đơn giản gồm 2 key:
- `quota:{merchant_id}:max_orders` = 1005 *(1000 từ Plan + 5 từ CS)* -> **Đã được tính toán sẵn**
- `usage:{merchant_id}:orders:2026-07-01` = 800 *(Lượng đang dùng hôm nay)*
Thuật toán check lúc này ở API Gateway/SubSvc chỉ tốn `O(1)`: Lấy 2 số từ Redis so sánh với nhau, cực kỳ trơn tru.

---

## 3. Sequence Diagram: Flow Cập nhật Lưu lượng (API Update Usage)

Sau khi Tạo đơn hàng thành công, hệ thống phải **tăng biến đếm Usage lên 1**. Nếu cộng thẳng vào DB (UPDATE current_usage = current_usage + 1) mỗi khi có đơn hàng thì sẽ dẫn tới khóa dòng (Row Lock) trên Database gây Deadlock khi có hàng ngàn đơn mỗi giây. 

Giải pháp là dùng **Redis INCR** và **Kafka (Đồng bộ ngầm)**.

```mermaid
sequenceDiagram
    participant CoreSvc as Order Service
    participant SubSvc as Subscription Service (Redis)
    participant Kafka as Kafka (Message Queue)
    participant Worker as Usage Sync Worker
    participant DB as Database (usage_tracking)

    Note over CoreSvc: Order được tạo thành công dưới DB
    
    %% Tăng bộ đếm trên Cache
    CoreSvc->>SubSvc: 1. gRPC / HTTP: Tăng usage (merchant_id, feature='MAX_ORDERS')
    SubSvc->>SubSvc: 2. Redis INCR usage:{merchant_id}:orders:{date}
    SubSvc-->>CoreSvc: 3. Trả kết quả ngay (Không đợi ghi DB)
    
    %% Sync bất đồng bộ xuống Database
    SubSvc-)Kafka: 4. Bắn event: UsageIncremented(merchant_id, feature, count=1)
    
    Note over Worker,DB: Chạy ngầm (Background/Async)
    Kafka--)Worker: 5. Consume message (Sử dụng Batching)
    Worker->>DB: 6. UPDATE usage_tracking SET current_usage = current_usage + N
    Note over DB: Worker gom (batch) các event của cùng merchant lại<br/>để update cộng dồn 1 lần thay vì update liên tục.
```

**Giải thích Flow Update Usage:**
1.  **Fast Path (Đường đáp ứng nhanh cho User):** Order Service báo cho Subscription Service tăng counter. Lệnh `INCR` của Redis chạy trên memory cực nhanh (chưa tới 1ms), request kết thúc ngay lập tức. Tính realtime luôn được đảm bảo vì API Check Access (ở trên) đọc trực tiếp số đếm đang nhảy liên tục từ Redis.
2.  **Slow Path (Đường đồng bộ bền vững - Durability):** Để tránh mất data trên Redis nếu server sập, ta bắn một Event xuống Kafka. Một con Worker chạy ngầm sẽ gom (batching) các sự kiện lại. 
    *VD: Trong 5 giây có 50 đơn hàng mới của cùng Merchant A, Worker gom lại và gọi duy nhất 1 lệnh SQL: `UPDATE usage_tracking SET current_usage = current_usage + 50 WHERE merchant_id = A`.* Điều này triệt tiêu hoàn toàn Database Bottleneck.
