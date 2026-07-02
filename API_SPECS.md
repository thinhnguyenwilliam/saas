# Đặc tả API Check Access (Frontend Integration)

Để Frontend có thể linh hoạt tùy chỉnh UI (Ví dụ: Ẩn/hiện nút bấm, Hiển thị popup "Nâng cấp gói", Hiển thị cảnh báo "Sắp hết hạn mức"), API `/api/v1/features/check-access` cần được thiết kế trả về nhiều Context (ngữ cảnh) hơn là chỉ một giá trị `true/false`.

## 1. Request Payload

Thông thường, thông tin về `user_id` và `merchant_id` đã được trích xuất từ **JWT Token** nằm trong Header `Authorization`, do đó Frontend không cần gửi lên để tránh việc user có thể cố tình giả mạo (spoofing) ID của người khác. 
Frontend chỉ cần gửi danh sách các `feature_code` cần kiểm tra (Nên hỗ trợ kiểm tra dạng Mảng (Array) để Frontend có thể check 1 lần khi load trang cho nhiều tính năng thay vì gọi API nhiều lần).

**POST /api/v1/features/check-access**
```json
{
  "features": [
    "CREATE_ORDER",
    "EXPORT_ADVANCED_REPORT"
  ]
}
```

## 2. Response Payload

Response nên trả về một object (Key-Value) để Frontend dễ dàng truy xuất data theo từng tên feature (độ phức tạp O(1) dưới Frontend).

```json
{
  "status": "success",
  "data": {
    "CREATE_ORDER": {
      "is_allowed": false,
      "type": "LIMIT",
      "reason_code": "QUOTA_EXCEEDED",
      "quota_info": {
        "current_usage": 1005,
        "max_quota": 1005,
        "remaining": 0,
        "reset_at": "2026-07-02T00:00:00Z"
      }
    },
    "EXPORT_ADVANCED_REPORT": {
      "is_allowed": false,
      "type": "BOOLEAN",
      "reason_code": "FEATURE_NOT_IN_PLAN",
      "quota_info": null
    }
  }
}
```

## 3. Ý nghĩa các trường & Cách Frontend tùy chỉnh giao diện (UI/UX)

Khi nhận được Response trên, Frontend sẽ dựa vào `is_allowed` và `reason_code` để "chốt" cách render giao diện:

### a) Trường hợp `is_allowed`: `true`
*   **Xử lý UI:** Cho phép User thực hiện hành động. (Hiển thị nút bấm màu xanh có thể click bình thường).
*   **Trải nghiệm nâng cao (Bonus UX):** Nếu `type` là `LIMIT`, Frontend có thể check biến `quota_info.remaining`. 
    *   Ví dụ: Nếu `remaining <= 10`, Frontend có thể hiển thị một dòng text nhỏ màu cam nhấp nháy bên cạnh nút tạo đơn: *"Bạn chỉ còn 10 lượt tạo đơn hôm nay"*. Điều này giúp Merchant biết để chuẩn bị mua thêm lượt.

### b) Trường hợp `is_allowed`: `false`
Tùy vào mã lỗi `reason_code` mà Frontend sẽ có các kịch bản UX (User Experience) rất khác biệt để kiếm tiền cho công ty SaaS:

1.  **`reason_code`: `"QUOTA_EXCEEDED"` (Hết hạn mức)**
    *   **UI Frontend:** Nút "Tạo đơn hàng" bị disable (chữ xám).
    *   **Hành động:** Khi user bấm vào hoặc hover chuột, Frontend không báo lỗi chung chung mà hiển thị một Modal hoặc Tooltip: *"Hạn mức tạo đơn của bạn hôm nay đã hết (1005/1005). Vui lòng nâng cấp gói cước hoặc liên hệ CSKH để mua thêm."* (Nên có một nút Call-to-action sáng màu để người dùng bấm vào gọi API mua thêm luôn).

2.  **`reason_code`: `"FEATURE_NOT_IN_PLAN"` (Tính năng không có trong gói cước)**
    *   **UI Frontend:** **Không nên ẩn nút bấm đi** mà vẫn hiển thị nút "Xuất báo cáo", nhưng kèm theo một biểu tượng 🔒 (Ổ khóa) hoặc nhãn tag nhỏ có chữ `[PRO]`.
    *   **Hành động:** Khi user (có quyền admin) click vào, hiển thị một Popup quảng cáo thật to và đẹp giới thiệu: "Báo cáo chuyên sâu giúp tăng 30% doanh số" và một nút "Nâng cấp lên gói Pro ngay". Đây là thủ thuật Upsell (bán chéo) cực kỳ quan trọng giúp nền tảng SaaS gia tăng doanh thu (MRR).

3.  **`reason_code`: `"MISSING_PERMISSION"` (Nhân viên không được cấp quyền)**
    *   **Trường hợp:** Merchant dùng gói Pro, quota vẫn còn vô biên, nhưng user đăng nhập hiện tại chỉ là nhân viên bốc vác hoặc thu ngân, không phải chủ cửa hàng (không có role xem báo cáo).
    *   **UI Frontend:** **Ẩn hoàn toàn** nút bấm hoặc menu đó khỏi giao diện màn hình. Tránh việc nhân viên bấm linh tinh sinh ra lỗi, đồng thời giữ giao diện gọn gàng cho từng loại Role.

4.  **`reason_code`: `"SUBSCRIPTION_EXPIRED"` (Gói cước bị quá hạn)**
    *   **UI Frontend:** Khóa mọi nút bấm (như QUOTA_EXCEEDED). Thêm vào đó, Frontend có thể vẽ thêm một chiếc Banner cảnh báo màu đỏ to đùng ghim trên đỉnh màn hình (Header) nhắc nhở: *"Gói dịch vụ của bạn đã hết hạn, các tính năng bị tạm khóa. Vui lòng nạp tiền để tiếp tục."*
