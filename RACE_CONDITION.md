# Giải quyết bài toán Race Condition trong Tracking Quota

## 1. Race Condition là gì và nó xảy ra như thế nào?

Giả sử Merchant A có gói cước (Quota) là **1000 đơn hàng/ngày**. 
Hiện tại, hệ thống ghi nhận họ đã tạo được **999 đơn**. Tức là Quota còn lại đúng 1 đơn.

Lúc này, đúng vào sự kiện Siêu Sale, có **5 khách hàng** bấm nút thanh toán trên Store của Merchant A vào **cùng một mili-giây**.

Nếu lập trình viên viết code ở API Gateway theo tư duy tuần tự thông thường:
```javascript
// Mã giả (Pseudo code) chạy trên 5 Pods/Server khác nhau cùng lúc
int currentUsage = redis.get("usage:merchant_A"); // Cả 5 request đều đọc được giá trị là 999

if (currentUsage < 1000) {
    redis.set("usage:merchant_A", currentUsage + 1); // Cả 5 request đều ghi đè giá trị lên 1000
    createOrder(); // Bùmmm! Tạo thành công 5 đơn hàng.
}
```

**Hậu quả:** Hệ thống tạo ra tới 1004 đơn hàng (Vượt hạn mức 4 đơn). Đây chính là **Race Condition** - sự tranh chấp giữa các luồng xử lý (Threads) trong hệ thống phân tán khi chúng cùng đọc và ghi vào chung một tài nguyên.

---

## 2. Các phương án giải quyết (Từ thảm họa đến tối ưu)

### ❌ Phương án 1: Dùng Khóa luồng trong Code (Mutex/Synchronized)
*   **Cách làm:** Dùng thẻ `synchronized` trong Java hoặc `Mutex` trong Go để khóa đoạn code lại.
*   **Vì sao thất bại?** Lệnh này chỉ có tác dụng trên **1 con Server**. Trong thực tế, hệ thống SaaS chạy hàng chục con Server (Instances) khác nhau qua Load Balancer. Server A khóa nhưng Server B vẫn chạy song song. -> **Vô dụng trong Distributed System.**

### ⚠️ Phương án 2: Khóa dòng Database (Pessimistic Locking)
*   **Cách làm:** Dùng câu lệnh SQL `SELECT ... FOR UPDATE`.
    ```sql
    BEGIN TRANSACTION;
    -- Lệnh này sẽ KHÓA CỨNG dòng (Row Lock). Các request khác phải đứng đợi.
    SELECT current_usage FROM usage_tracking WHERE merchant_id = 'A' FOR UPDATE;
    
    -- Nếu < 1000 thì UPDATE, rồi COMMIT để nhả khóa.
    UPDATE usage_tracking SET current_usage = current_usage + 1;
    COMMIT;
    ```
*   **Ưu điểm:** Đảm bảo chính xác 100%, không bao giờ lọt.
*   **Nhược điểm:** **Hiệu năng thảm họa**. Khi có 1000 request ập tới, 999 request phải xếp hàng chờ Database nhả khóa. Điều này tạo ra nút thắt cổ chai cực lớn (Bottleneck), dễ gây Deadlock và sập luôn cả Database (Cascading Failure).

### 🐢 Phương án 3: Khóa phân tán (Distributed Lock với Redis - Redlock)
*   **Cách làm:** Trước khi xử lý, yêu cầu Redis cấp cho 1 cái khóa (Lock). Ai cầm khóa mới được đi tiếp, làm xong thì trả khóa.
*   **Nhược điểm:** Tốn quá nhiều Network Call (1 lần gọi xin khóa, 1 lần gọi đọc data, 1 lần gọi ghi data, 1 lần gọi trả khóa). Rất rườm rà và làm chậm tốc độ đáp ứng API chỉ vì một thao tác "Cộng 1 biến đếm".

### ✅ Phương án 4: Tuyệt chiêu tối ưu nhất - Redis Lua Script (Giao dịch Nguyên tử)

Bản thân Redis là một database chạy **Single-thread** (Đơn luồng). Tại 1 thời điểm, nó chỉ chạy 1 câu lệnh duy nhất.
Tuy nhiên, nếu bạn gọi lệnh `GET` từ code, rồi gọi tiếp lệnh `SET`, thì khoảng thời gian giữa 2 lệnh đó, Redis vẫn có thể bị chèn lệnh của Request khác vào.

Để gộp quá trình *"Đọc -> Kiểm tra -> Ghi"* thành 1 khối duy nhất **không thể bị tách rời (Atomic)**, ta sẽ gửi cho Redis một đoạn **Lua Script**.

**Đoạn code Lua Script thần thánh (Lưu sẵn vào backend):**
```lua
-- KEYS[1] là tên key trong Redis (vd: usage:merchant_A)
-- ARGV[1] là hạn mức tối đa (vd: 1000)

local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local limit = tonumber(ARGV[1])

if current >= limit then
    return -1 -- Hết Quota, Script tự báo lỗi từ chối ngay bên trong Redis!
else
    -- Nếu còn Quota thì tăng biến đếm lên 1 và trả về số lượng vừa cập nhật
    redis.call("INCR", KEYS[1])
    return current + 1
end
```

**Luồng thực thi hoàn hảo:**
1. Khi có 5 request tới cùng lúc, 5 Backend Server gọi lệnh `EVAL` (Thực thi Lua Script) bắn sang Redis.
2. Redis xếp hàng 5 cái Script này. Vì chạy Single-thread, nó bốc Script số 1 lên chạy.
3. Trong lúc chạy Script số 1 (Đọc 999 -> Thấy bé hơn 1000 -> Tăng lên 1000), **không một request hay lệnh nào khác trên toàn thế giới được phép chen ngang**.
4. Chạy xong Script số 1, Redis lấy Script số 2 lên chạy. Lúc này Đọc = 1000, dòng `IF` thấy `>= limit` -> Bắn thẳng lỗi `-1`.
5. Các Request số 3, 4, 5 đều sẽ bốc phải kịch bản y hệt và nhận lỗi `-1`.

**Kết quả:**
*   **Đảm bảo Atomic 100%:** Ngăn chặn triệt để Race Condition. Chặn đứng 100% các đơn hàng vượt quá 1000.
*   **Hiệu năng vô địch (O(1)):** Gateway chỉ tốn đúng **1 Network call** tới Redis thay vì phải gọi nhiều bước. Tốc độ check quyền < 2ms, không hề tạo ra Bottleneck.
