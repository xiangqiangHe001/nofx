package main

import (
    "fmt"
    "os"
    "nofx/decision"
    "nofx/market"
)

// 本地测试：构造一个AI响应，其中开仓的RR<2.6，验证校验错误信息是否生效
func main() {
    // 1) 获取当前价格
    md, err := market.Get("BTCUSDT")
    if err != nil {
        fmt.Printf("获取市场数据失败: %v\n", err)
        return
    }
    entry := md.CurrentPrice

    // 2) 构造止损/止盈使 RR < 2.6
    // 风险约 ~1% 下穿，收益约 ~2% 上涨 → RR≈2.0
    stopLoss := entry * 0.99
    takeProfit := entry * 1.02

    // 3) 构造AI响应（包含思维链 + 决策JSON）
    aiResponse := fmt.Sprintf(`
思维链：市场震荡，尝试小额试单以观察动能。
[
  {
    "symbol": "BTCUSDT",
    "action": "open_long",
    "leverage": 5,
    "position_size_usd": 100,
    "stop_loss": %.4f,
    "take_profit": %.4f,
    "confidence": 60,
    "risk_usd": 2.0,
    "reasoning": "短期多头尝试，RR刻意设置为<2.6用于测试校验"
  }
]
`, stopLoss, takeProfit)

    // 4) 解析并校验（使用账户净值与杠杆上限）
    // 账户净值与杠杆上限只影响其他校验，此处设为常用默认
    // 使用环境变量或默认值传入最小风险回报比
    minRR := 2.6
    if v := os.Getenv("NOFX_MIN_RISK_REWARD_RATIO"); v != "" {
        fmt.Sscanf(v, "%f", &minRR)
    }
    fd, err := decision.ParseDecisionsForTest(aiResponse, 1000.0, 5, 5, minRR)
    if err != nil {
        fmt.Println("=== 校验返回（预期触发RR<2.6） ===")
        fmt.Println(err.Error())
        return
    }

    // 若未报错，展示解析结果（意外通过时的调试信息）
    fmt.Println("解析成功，未触发RR校验")
    fmt.Printf("Decisions parsed: %d\n", len(fd.Decisions))
}