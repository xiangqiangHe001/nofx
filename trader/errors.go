package trader

import (
    "fmt"
)

// OrderError 表示下单失败的结构化错误，便于前端友好展示
type OrderError struct {
    Exchange          string  `json:"exchange"`
    Symbol            string  `json:"symbol"`
    Side              string  `json:"side"`
    Quantity          float64 `json:"quantity"`
    Leverage          int     `json:"leverage"`
    RequiredMarginUSD float64 `json:"required_margin_usd"`
    AvailableUSD      float64 `json:"available_usd"`
    Code              string  `json:"code"`
    Message           string  `json:"message"`
    Friendly          string  `json:"friendly"`
    Suggestion        string  `json:"suggestion"`
}

func (e *OrderError) Error() string {
    // 简洁错误字符串，保留核心信息
    return fmt.Sprintf("%s %s %s 失败: code=%s msg=%s (need=%.2f avail=%.2f)",
        e.Exchange, e.Symbol, e.Side, e.Code, e.Message, e.RequiredMarginUSD, e.AvailableUSD)
}

// MapOkxError 根据 OKX sCode/sMsg 映射友好提示
func MapOkxError(sCode, sMsg string) (friendly string, suggestion string) {
    switch sCode {
    case "51008":
        // 资金/保证金不足
        return "保证金或资金不足，当前仓位规模超出账户可用余额。",
            "请降低下单数量或提高杠杆；也可补充资金后再试。"
    case "51000":
        return "参数错误或账户状态不匹配（可能需要设置 posSide）",
            "刷新账户持仓模式并重试；或设置正确的杠杆与持仓模式。"
    case "51010":
        return "账户模式不匹配（净持仓/双向持仓）",
            "清除持仓模式缓存，重新检测账户模式后再尝试。"
    case "50011":
        return "订单状态不允许（可能已成交或已取消）",
            "刷新订单/持仓状态后再操作。"
    default:
        if sMsg != "" {
            return sMsg, "请稍后重试或联系支持。"
        }
        return "下单失败（未知错误）", "请稍后重试或联系支持。"
    }
}