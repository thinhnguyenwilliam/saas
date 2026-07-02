# Thiết kế Hệ thống SaaS cho Merchant

Bài toán: Thiết kế hệ thống SaaS (Multi-tenant) cho Merchant (Nhà bán hàng) với nhiều Store, quản lý gói cước (Subscriptions), phân quyền (RBAC), tracking quota và Audit Log.

## 1. Thiết kế Database Schema

Để đáp ứng tính linh hoạt cho một nền tảng SaaS, hệ thống được chia thành các nhóm dữ liệu chính:

### Nhóm 1: Quản lý Merchant, Store và Phân quyền (RBAC)
* **`merchants`** (Tenant gốc)
  * `id`, `name`, `status`, `created_at`
* **`stores`** (Cửa hàng thuộc Merchant)
  * `id`, `merchant_id`, `name`, `address`
* **`users`** (Bao gồm chủ Merchant, nhân viên Store, và cả nhân viên CS của nền tảng)
  * `id`, `merchant_id` *(null nếu là nhân viên CS)*, `email`, `password_hash`
* **`roles`**, **`permissions`**, **`role_permissions`** (Quản lý quyền)
  * Lưu các quyền như: `EXPORT_ADVANCED_REPORT`, `CREATE_ORDER`, `MANAGE_STORE`.
* **`user_roles`**
  * `user_id`, `role_id`, `store_id` *(Cho phép phân quyền một nhân viên chỉ quản lý 1 store cụ thể, hoặc null nếu quản lý toàn bộ hệ thống merchant).*

### Nhóm 2: Quản lý Gói cước (Plans) & Tính năng (Features)
* **`plans`**: Định nghĩa các gói.
  * `id`, `name` (Free, Medium, Pro), `price`
* **`features`**: Danh sách các tính năng của hệ thống.
  * `id`, `code` (vd: `MAX_ORDERS_PER_DAY`, `ADVANCED_REPORT`), `type` (`BOOLEAN` hoặc `LIMIT`).
* **`plan_features`**: Map giữa gói cước và hạn mức tính năng.
  * `plan_id`, `feature_id`, `limit_value` (VD: plan Pro + feature `MAX_ORDERS_PER_DAY` có `limit_value` = 1000).
* **`subscriptions`**: Thông tin đăng ký gói cước hiện tại của Merchant.
  * `id`, `merchant_id`, `plan_id`, `status` (ACTIVE, EXPIRED), `valid_until`.

### Nhóm 3: Tracking Hạn mức & CS Can thiệp
* **`usage_tracking`**: Theo dõi số lượng đơn đã tạo trong ngày.
  * `id`, `merchant_id`, `feature_id`, `period_date` (vd: 2026-07-01), `current_usage`.
* **`quota_adjustments`**: Lưu vết việc nhân viên CS tặng thêm hạn mức.
  * `id`, `merchant_id`, `feature_id`, `additional_limit` (vd: +5), `period_date`, `granted_by`, `reason`.

### Nhóm 4: Audit Logs (Lịch sử chỉnh sửa)
* **`audit_logs`**
  * `id`, `entity_type`, `entity_id`, `action` (CREATE, UPDATE, DELETE), `changed_by`, `created_at`
  * `old_data` (JSONB) - Dữ liệu trước khi sửa.
  * `new_data` (JSONB) - Dữ liệu sau khi sửa.

---

## 2. Thiết kế Giải pháp Hệ thống (System Design)

### a) Tính năng: Merchant có quyền xuất báo cáo chuyên sâu
Khi một User yêu cầu tính năng này, hệ thống sẽ kiểm tra 2 lớp:
1. **Lớp User Role:** Kiểm tra nhân viên này có quyền `EXPORT_ADVANCED_REPORT` không.
2. **Lớp Tenant Plan:** Kiểm tra Merchant đang có `subscriptions` ACTIVE và Plan có tính năng báo cáo chuyên sâu không.
> **Tối ưu:** Các thông tin Permissions của User và Features của Plan nên được **cache trên Redis** (hoặc lưu trong JWT Token) để giảm tải query Database cho mỗi request.

### b) Giới hạn 1000 đơn/ngày. Hết hạn mức không sập (Read-only)
* **Usage Tracking bằng Redis:** Sử dụng **Redis INCR** để đếm số lượng đơn. Key: `usage:{merchant_id}:orders:{YYYY-MM-DD}` (TTL 24-48h).
* **Cơ chế Read-only:** 
  * Đặt **API Gateway / Middleware** ở các API thay đổi dữ liệu (POST/PUT). 
  * Khi có `POST /orders`, Middleware gọi Redis lấy `current_usage`.
  * Nếu `current_usage >= max_quota`, API lập tức trả về HTTP `429 Too Many Requests` hoặc `403 Forbidden`.
  * Các API GET (xem danh sách) vẫn qua bình thường, giúp hệ thống vào trạng thái Read-only đối với việc tạo mới mà không bị sập.

### c) Nhân viên CS có thể tặng thêm 5 đơn/ngày
* Nhân viên CS thao tác trên Admin Portal để insert 1 bản ghi vào bảng `quota_adjustments` (`additional_limit = 5`).
* Limit thực tế: `Max_Quota` = `plan_features.limit_value` (1000) + `SUM(quota_adjustments.additional_limit)` (5) = 1005.
* Hệ thống sẽ tự động update lại biến `Max_Quota` này trên Redis để Middleware kiểm tra nhanh chóng.

### d) Audit Log: Lưu vết thay đổi dữ liệu
* **Cách 1 - Application-Level (Message Queue):** Sử dụng Lifecycle Hook của ORM. Trước khi Update, lấy Data cũ và mới serialize thành JSON. Đẩy message này vào **Kafka / RabbitMQ** để một Worker ngầm ghi vào Database (hoặc Elasticsearch). Cách này giúp API không bị delay.
* **Cách 2 - Database-Level (CDC - Change Data Capture):** Dùng công cụ như **Debezium** đọc Transaction Logs (Binlog/WAL) từ DB và tự động đẩy sự kiện lên Kafka. Tách biệt hoàn toàn logic code và ghi log.
* **Hiển thị (UI):** Vì lưu dưới dạng JSONB, Frontend chỉ cần dùng các thư viện so sánh JSON (diff) để highlight màu xanh/đỏ cho người dùng biết trường nào bị thay đổi.
