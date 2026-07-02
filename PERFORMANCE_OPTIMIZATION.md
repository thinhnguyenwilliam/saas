# Tối ưu Hiệu năng (Performance) cho API Check Access

Khi **mọi** API nghiệp vụ (Tạo đơn, Xuất báo cáo, Quản lý sản phẩm...) đều phải đi qua bước Check Access (Kiểm tra Quyền & Quota), nếu không thiết kế khéo, bước này sẽ trở thành **Bottleneck (Thắt cổ chai)** cực lớn làm sập toàn bộ hệ thống.

Dưới đây là các chiến lược (System Design Strategies) để tối ưu Performance cho Check Access xuống mức chỉ vài mili-giây (< 5ms):

## 1. Stateless Authorization với JWT (Nhúng Quyền vào Token)
Thay vì mỗi request API Gateway phải gọi mạng (network call) sang Auth Service để hỏi "User này có quyền X không?", hãy **nhúng thẳng quyền vào JWT**.

*   **Cách làm:** Trong payload của JWT Token, khi user login, thêm mảng `permissions: ["CREATE_ORDER", "EXPORT_REPORT"]` và `plan_features: ["DARK_MODE"]`.
*   **Lợi ích:** Khi user gọi API, API Gateway chỉ cần parse JWT (CPU xử lý nội bộ tốn chưa tới 0.1ms). Nếu có quyền thì đi tiếp, không thì chặn ngay. **Tiết kiệm 100% thời gian gọi Database/Redis cho việc check Quyền boolean.**
*   **Lưu ý:** Chỉ dùng cho các quyền/tính năng ít thay đổi. Không nhúng "Lưu lượng Usage đang dùng" vào JWT vì nó thay đổi liên tục.

## 2. Multi-level Caching (Cache Nhiều Lớp) cho Quota Limit
Với các Quota đếm số (như "1000 đơn/ngày"), dữ liệu nhảy số liên tục nên phải gọi ra ngoài. Ta dùng kiến trúc Cache 2 lớp:

### Lớp 1: L1 Cache (Local In-Memory Cache)
*   Bộ nhớ tạm nằm trực tiếp trên RAM của API Gateway (Dùng Guava Cache, LRU Cache).
*   **Lưu trữ:** Biến `max_quota` (Tổng gói gốc + thưởng). Vì biến này rất ít khi đổi (chỉ đổi khi CS tặng thêm hoặc mua gói mới).
*   **TTL (Time-to-live):** Set khoảng 3 - 5 phút. 
    *   *Trade-off (Sự đánh đổi):* Nếu CS tặng thêm lượt, Merchant có thể phải đợi tối đa 3 phút để hệ thống nhận lượt mới. Điều này hoàn toàn chấp nhận được trong SaaS để đổi lấy tốc độ bàn thờ.

### Lớp 2: L2 Cache (Distributed Cache - Redis)
*   **Lưu trữ:** Biến `current_usage` (Số lượng đơn đã xài).
*   Vì biến này nhảy liên tục từng mili-giây, ta đọc/ghi thẳng vào Redis. Lệnh `GET` của Redis chỉ tốn khoảng 1-2ms.
*   **Luồng tối ưu:** Gateway lấy `max_quota` từ L1 (0ms) -> Gateway lấy `current_usage` từ Redis (1ms) -> So sánh và cho qua. **Hoàn toàn KHÔNG query Database.**

## 3. Giao tiếp nội bộ bằng gRPC (Thay vì REST/HTTP)
Nếu hệ thống bắt buộc phải có một "Access Control Service" riêng biệt và API Gateway phải gọi sang nó để check những logic quá phức tạp:
*   Hãy dùng **gRPC** (chạy trên HTTP/2, dữ liệu nhị phân Protobuf) thay vì REST API (JSON).
*   **Lợi ích:** Kích thước gói tin nhỏ hơn 5-10 lần, tốc độ parse nhanh hơn, và hỗ trợ **Multiplexing** (tái sử dụng 1 connection liên tục) giúp loại bỏ hoàn toàn độ trễ bắt tay TCP (TCP Handshake) của mỗi request check quyền.

## 4. Dùng Lua Script trong Redis để chốt chặn Race Condition
Khi Merchant áp sát ngưỡng 1000/1000 đơn. Nếu có 50 request Tạo Đơn ập tới cùng lúc ở mili-giây đó. Nếu ta thiết kế kiểu: 
1. Get usage -> 2. So sánh < 1000 -> 3. Tăng usage lên 1. 
Thì cả 50 request đều thấy usage đang là 999 và cùng pass, dẫn tới việc Merchant tạo được 1049 đơn (Vượt hạn mức - Race Condition).

*   **Đỉnh cao tối ưu:** Sử dụng **Redis Lua Script**. Đưa cả 3 bước (Lấy, So sánh, Tăng biến) vào một đoạn script Lua. Redis sẽ thực thi cục script này như một giao dịch nguyên tử (Atomic transaction). 
*   **Kết quả:** Vừa chặn đứng tuyệt đối lỗi vượt hạn mức, vừa chỉ tốn đúng 1 lượt gọi network tới Redis thay vì 2 lượt, tăng gấp đôi Performance.

## 5. Bloom Filter (Lưới lọc xác suất)
Giả sử có tính năng nâng cao "Xuất báo cáo PDF" mà chỉ 5% khách hàng mua.
*   Đưa toàn bộ danh sách `merchant_id` CÓ tính năng này vào một cấu trúc dữ liệu siêu nhỏ tên là **Bloom Filter** (đặt ở RAM của API Gateway).
*   Khi có request check, Bloom Filter trả lời cực nhanh (0.01ms):
    *   *Chắc chắn KHÔNG:* API Gateway từ chối ngay lập tức (Xử lý 95% traffic rác ngay tại cửa).
    *   *Có thể CÓ:* API Gateway mới tốn công gọi sang Redis/DB để check lại chắc chắn cho 5% khách hàng kia. 
*   Đây là tuyệt chiêu của các hệ thống cực lớn như Medium (check user đã đọc bài chưa), hay Tinder (check user đã swipe chưa).
