package trader

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"
)

// OKXTrader OKX交易平台实现
type OKXTrader struct {
    apiKey     string
    secretKey  string
    passphrase string
    client     *http.Client
    baseURL    string
}

// NewOKXTrader 创建OKX交易器
func NewOKXTrader(apiKey, secretKey, passphrase string) (*OKXTrader, error) {
    if apiKey == "" || secretKey == "" || passphrase == "" {
        return nil, fmt.Errorf("缺少OKX凭据")
    }
    return &OKXTrader{
        apiKey:     apiKey,
        secretKey:  secretKey,
        passphrase: passphrase,
        client:     &http.Client{Timeout: 15 * time.Second},
        baseURL:    "https://www.okx.com",
    }, nil
}

// sign 生成 OKX REST v5 签名
func (t *OKXTrader) sign(ts, method, requestPath, body string) string {
    msg := ts + strings.ToUpper(method) + requestPath + body
    mac := hmac.New(sha256.New, []byte(t.secretKey))
    mac.Write([]byte(msg))
    return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// doRequest 执行私有请求（GET）
func (t *OKXTrader) doRequest(method, path string, query map[string]string) ([]byte, error) {
    // 构造URL与query
    u, _ := url.Parse(t.baseURL)
    u.Path = path
    q := u.Query()
    for k, v := range query {
        q.Set(k, v)
    }
    u.RawQuery = q.Encode()

    ts := time.Now().UTC().Format(time.RFC3339)
    // 注意签名使用 requestPath + "?" + query（若有）
    requestPath := path
    if u.RawQuery != "" {
        requestPath = requestPath + "?" + u.RawQuery
    }
    sign := t.sign(ts, method, requestPath, "")

    req, _ := http.NewRequest(method, u.String(), nil)
    req.Header.Set("OK-ACCESS-KEY", t.apiKey)
    req.Header.Set("OK-ACCESS-SIGN", sign)
    req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
    req.Header.Set("OK-ACCESS-PASSPHRASE", t.passphrase)
    req.Header.Set("Content-Type", "application/json")

    resp, err := t.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    if resp.StatusCode != http.StatusOK {
        return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
    }
    return body, nil
}

// doRequestJSON 执行带JSON主体的私有请求（POST/DELETE等）
func (t *OKXTrader) doRequestJSON(method, path string, query map[string]string, bodyObj map[string]interface{}) ([]byte, error) {
    // 构造URL与query
    u, _ := url.Parse(t.baseURL)
    u.Path = path
    q := u.Query()
    for k, v := range query {
        q.Set(k, v)
    }
    u.RawQuery = q.Encode()

    // 编码JSON主体
    var bodyStr string
    if bodyObj != nil {
        b, _ := json.Marshal(bodyObj)
        bodyStr = string(b)
    } else {
        bodyStr = ""
    }

    ts := time.Now().UTC().Format(time.RFC3339)
    requestPath := path
    if u.RawQuery != "" {
        requestPath = requestPath + "?" + u.RawQuery
    }
    sign := t.sign(ts, method, requestPath, bodyStr)

    req, _ := http.NewRequest(method, u.String(), strings.NewReader(bodyStr))
    req.Header.Set("OK-ACCESS-KEY", t.apiKey)
    req.Header.Set("OK-ACCESS-SIGN", sign)
    req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
    req.Header.Set("OK-ACCESS-PASSPHRASE", t.passphrase)
    req.Header.Set("Content-Type", "application/json")

    resp, err := t.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    if resp.StatusCode != http.StatusOK {
        return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
    }
    return body, nil
}

// GetBalance 获取账户余额
// 映射到系统统一字段：totalWalletBalance, availableBalance, totalUnrealizedProfit
func (t *OKXTrader) GetBalance() (map[string]interface{}, error) {
    // 账户余额（统一账户）
    // 文档: GET /api/v5/account/balance （参见 OKX API Guide）
    // 若调用失败，返回零值以保证系统运行（配合占位密钥）。
    body, err := t.doRequest("GET", "/api/v5/account/balance", map[string]string{})
    if err != nil {
        // 账户余额失败，尝试资金账户余额作为回退
        return t.getBalanceFallback()
    }

    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil {
        return t.getBalanceFallback()
    }

    code, _ := resp["code"].(string)
    if code != "0" {
        return t.getBalanceFallback()
    }

    data, _ := resp["data"].([]interface{})
    wallet := 0.0
    avail := 0.0
    unreal := 0.0
    if len(data) > 0 {
        first, _ := data[0].(map[string]interface{})
        // totalEq（账户总净值）
        if v, ok := first["totalEq"].(string); ok {
            if f, e := parseFloat(v); e == nil {
                wallet = f
            }
        }
        // details 列出各币种余额（取 USDT 的 availBal/cashBal）
        if details, ok := first["details"].([]interface{}); ok {
            for _, d := range details {
                dm, _ := d.(map[string]interface{})
                if dm["ccy"] == "USDT" {
                    // 参考 yuanbao.py：取 USDT 的可用余额最大值（availBal/cashBal/bal）
                    usdtAvail := 0.0
                    if v, ok := dm["availBal"].(string); ok {
                        if f, e := parseFloat(v); e == nil {
                            if f > usdtAvail { usdtAvail = f }
                        }
                    }
                    if v, ok := dm["cashBal"].(string); ok {
                        if f, e := parseFloat(v); e == nil {
                            if f > usdtAvail { usdtAvail = f }
                        }
                    }
                    if v, ok := dm["bal"].(string); ok { // 某些场景返回总余额 bal
                        if f, e := parseFloat(v); e == nil {
                            if f > usdtAvail { usdtAvail = f }
                        }
                    }
                    avail = usdtAvail

                    // unrealized pnl（如未提供则为0）
                    if v, ok := dm["upl"].(string); ok {
                        if f, e := parseFloat(v); e == nil {
                            unreal = f
                        }
                    }
                }
            }
        }
    }

    // 若USDT可用余额仍为0，尝试资金账户余额作为回退
    if avail <= 0 {
        fb, _ := t.getBalanceFallback()
        return fb, nil
    }

    return map[string]interface{}{
        "totalWalletBalance":    wallet,
        "availableBalance":      avail,
        "totalUnrealizedProfit": unreal,
    }, nil
}

// GetPositions 获取所有持仓（统一字段）
func (t *OKXTrader) GetPositions() ([]map[string]interface{}, error) {
    // 文档: GET /api/v5/account/positions
    body, err := t.doRequest("GET", "/api/v5/account/positions", map[string]string{
        "instType": "SWAP",
    })
    if err != nil {
        return []map[string]interface{}{}, nil
    }

    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil {
        return []map[string]interface{}{}, nil
    }
    code, _ := resp["code"].(string)
    if code != "0" {
        return []map[string]interface{}{}, nil
    }
    data, _ := resp["data"].([]interface{})
    out := make([]map[string]interface{}, 0, len(data))
    for _, it := range data {
        m, _ := it.(map[string]interface{})
        instId, _ := m["instId"].(string)
        posSide, _ := m["posSide"].(string) // long/short
        side := "long"
        if strings.ToLower(posSide) == "short" {
            side = "short"
        }
        qty := parseStringFloat(m["pos"])        // 张或数量
        if qty == 0 {
            // 仅返回非零持仓，参考 yuanbao.py 的逻辑
            continue
        }
        entry := parseStringFloat(m["avgPx"])    // 开仓均价
        mark := parseStringFloat(m["markPx"])    // 标记价格
        lev := parseStringFloat(m["lever"])      // 杠杆
        liq := parseStringFloat(m["liqPx"])      // 强平价

        out = append(out, map[string]interface{}{
            "symbol":            instId,
            "side":              side,
            "positionAmt":       qty,
            "entryPrice":        entry,
            "markPrice":         mark,
            "leverage":          lev,
            "liquidationPrice":  liq,
        })
    }
    return out, nil
}

// OpenLong 开多仓（市价单，cross模式，支持预设杠杆）
func (t *OKXTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
    instId := toOKXInstID(symbol)
    if leverage > 0 {
        _ = t.SetLeverage(instId, leverage)
    }
    params := map[string]interface{}{
        "instId": instId,
        "tdMode": "cross",
        "side":   "buy",
        "ordType": "market",
        "sz":     formatSize(quantity),
    }
    body, err := t.doRequestJSON("POST", "/api/v5/trade/order", nil, params)
    if err != nil {
        return nil, err
    }
    return parseOrderResponse(body)
}

// OpenShort 开空仓（市价单，cross模式，支持预设杠杆）
func (t *OKXTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
    instId := toOKXInstID(symbol)
    if leverage > 0 {
        _ = t.SetLeverage(instId, leverage)
    }
    params := map[string]interface{}{
        "instId": instId,
        "tdMode": "cross",
        "side":   "sell",
        "ordType": "market",
        "sz":     formatSize(quantity),
    }
    body, err := t.doRequestJSON("POST", "/api/v5/trade/order", nil, params)
    if err != nil {
        return nil, err
    }
    return parseOrderResponse(body)
}

// CloseLong 平多仓（reduceOnly市价单）
func (t *OKXTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
    instId := toOKXInstID(symbol)
    // 数量为0时，自动查询当前多仓数量
    if quantity == 0 {
        positions, _ := t.GetPositions()
        for _, p := range positions {
            if p["symbol"] == instId && p["side"] == "long" {
                if v, ok := p["positionAmt"].(float64); ok { quantity = v }
                break
            }
        }
        if quantity == 0 { return nil, fmt.Errorf("没有找到 %s 的多仓", instId) }
    }
    params := map[string]interface{}{
        "instId":     instId,
        "tdMode":     "cross",
        "side":       "sell",
        "ordType":    "market",
        "sz":         formatSize(quantity),
        "reduceOnly": true,
    }
    body, err := t.doRequestJSON("POST", "/api/v5/trade/order", nil, params)
    if err != nil { return nil, err }
    return parseOrderResponse(body)
}

// CloseShort 平空仓（reduceOnly市价单）
func (t *OKXTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
    instId := toOKXInstID(symbol)
    if quantity == 0 {
        positions, _ := t.GetPositions()
        for _, p := range positions {
            if p["symbol"] == instId && p["side"] == "short" {
                if v, ok := p["positionAmt"].(float64); ok { quantity = v }
                break
            }
        }
        if quantity == 0 { return nil, fmt.Errorf("没有找到 %s 的空仓", instId) }
    }
    params := map[string]interface{}{
        "instId":     instId,
        "tdMode":     "cross",
        "side":       "buy",
        "ordType":    "market",
        "sz":         formatSize(quantity),
        "reduceOnly": true,
    }
    body, err := t.doRequestJSON("POST", "/api/v5/trade/order", nil, params)
    if err != nil { return nil, err }
    return parseOrderResponse(body)
}

// SetLeverage 设置杠杆（官方私有API）
func (t *OKXTrader) SetLeverage(symbol string, leverage int) error {
    instId := toOKXInstID(symbol)
    params := map[string]interface{}{
        "instId": instId,
        "mgnMode": "cross",
        "lever":  fmt.Sprintf("%d", leverage),
    }
    body, err := t.doRequestJSON("POST", "/api/v5/account/set-leverage", nil, params)
    if err != nil { return err }
    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) == nil {
        if code, _ := resp["code"].(string); code == "0" { return nil }
        return fmt.Errorf("OKX SetLeverage失败: %v", resp)
    }
    return fmt.Errorf("OKX SetLeverage解析失败")
}

// GetMarketPrice 获取市场最新价
func (t *OKXTrader) GetMarketPrice(symbol string) (float64, error) {
    instId := toOKXInstID(symbol)
    body, err := t.doRequest("GET", "/api/v5/market/ticker", map[string]string{"instId": instId})
    if err != nil { return 0, err }
    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil { return 0, fmt.Errorf("价格响应解析失败") }
    if code, _ := resp["code"].(string); code != "0" { return 0, fmt.Errorf("OKX价格API错误: %s", code) }
    data, _ := resp["data"].([]interface{})
    if len(data) == 0 { return 0, fmt.Errorf("无ticker数据") }
    first, _ := data[0].(map[string]interface{})
    price := parseStringFloat(first["last"])
    if price <= 0 { price = parseStringFloat(first["askPx"]) }
    if price <= 0 { price = parseStringFloat(first["bidPx"]) }
    if price <= 0 { return 0, fmt.Errorf("无有效价格") }
    return price, nil
}

// CancelAllOrders 取消该instId的所有挂单
func (t *OKXTrader) CancelAllOrders(symbol string) error {
    instId := toOKXInstID(symbol)
    // 列出未成交订单
    body, err := t.doRequest("GET", "/api/v5/trade/orders-pending", map[string]string{"instId": instId})
    if err != nil { return err }
    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil { return fmt.Errorf("pending订单解析失败") }
    if code, _ := resp["code"].(string); code != "0" { return nil }
    data, _ := resp["data"].([]interface{})
    for _, d := range data {
        m, _ := d.(map[string]interface{})
        ordId, _ := m["ordId"].(string)
        if ordId == "" { continue }
        cancelBody := map[string]interface{}{"instId": instId, "ordId": ordId}
        _, _ = t.doRequestJSON("POST", "/api/v5/trade/cancel-order", nil, cancelBody)
    }
    return nil
}

// SetStopLoss/SetTakeProfit 暂不实现（OKX条件单较复杂，出于范围）
func (t *OKXTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
    return errors.New("OKX SetStopLoss 暂未实现")
}
func (t *OKXTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
    return errors.New("OKX SetTakeProfit 暂未实现")
}

// FormatQuantity 格式化数量到正确的精度（简单返回字符串）
func (t *OKXTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
    return fmt.Sprintf("%f", quantity), nil
}

// parseFloat 安全解析字符串为浮点数
func parseFloat(s string) (float64, error) {
    var f float64
    _, err := fmt.Sscanf(s, "%f", &f)
    if err != nil {
        return 0, err
    }
    return f, nil
}

func parseStringFloat(v interface{}) float64 {
    if v == nil {
        return 0
    }
    if s, ok := v.(string); ok {
        if f, err := parseFloat(s); err == nil {
            return f
        }
    }
    return 0
}

// getBalanceFallback 使用资金账户API作为回退，获取USDT余额
// 文档: GET /api/v5/asset/balances?ccy=USDT
func (t *OKXTrader) getBalanceFallback() (map[string]interface{}, error) {
    body, err := t.doRequest("GET", "/api/v5/asset/balances", map[string]string{"ccy": "USDT"})
    if err != nil {
        return map[string]interface{}{
            "totalWalletBalance":    0.0,
            "availableBalance":      0.0,
            "totalUnrealizedProfit": 0.0,
        }, nil
    }
    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil {
        return map[string]interface{}{
            "totalWalletBalance":    0.0,
            "availableBalance":      0.0,
            "totalUnrealizedProfit": 0.0,
        }, nil
    }
    code, _ := resp["code"].(string)
    if code != "0" {
        return map[string]interface{}{
            "totalWalletBalance":    0.0,
            "availableBalance":      0.0,
            "totalUnrealizedProfit": 0.0,
        }, nil
    }
    data, _ := resp["data"].([]interface{})
    usdt := 0.0
    if len(data) > 0 {
        for _, d := range data {
            dm, _ := d.(map[string]interface{})
            if dm["ccy"] == "USDT" {
                usdt = parseStringFloat(dm["bal"]) // 资金账户余额字段为 bal
                break
            }
        }
    }
    return map[string]interface{}{
        "totalWalletBalance":    usdt,
        "availableBalance":      usdt,
        "totalUnrealizedProfit": 0.0,
    }, nil
}

// ===== 辅助函数 =====
func formatSize(qty float64) string {
    // 简单转换为字符串，不做精度裁剪（可按需求增强）
    return fmt.Sprintf("%f", qty)
}

func toOKXInstID(symbol string) string {
    s := strings.TrimSpace(symbol)
    if s == "" { return "BTC-USDT-SWAP" }
    // 已是OKX格式
    if strings.Contains(s, "-") && strings.Contains(s, "SWAP") { return s }
    // 形如 BTCUSDT -> BTC-USDT-SWAP
    upper := strings.ToUpper(s)
    // 简单分割：后四位作为quote（USDT/USDC等），其余作为base
    if len(upper) > 4 {
        base := upper[:len(upper)-4]
        quote := upper[len(upper)-4:]
        return base + "-" + quote + "-SWAP"
    }
    return s
}

func parseOrderResponse(body []byte) (map[string]interface{}, error) {
    var resp map[string]interface{}
    if json.Unmarshal(body, &resp) != nil { return nil, fmt.Errorf("订单响应解析失败") }
    code, _ := resp["code"].(string)
    if code != "0" { return nil, fmt.Errorf("下单失败: %v", resp) }
    data, _ := resp["data"].([]interface{})
    out := map[string]interface{}{"success": true}
    if len(data) > 0 {
        first, _ := data[0].(map[string]interface{})
        out["ordId"] = first["ordId"]
        out["clOrdId"] = first["clOrdId"]
        out["instId"] = first["instId"]
        out["state"] = first["state"]
    }
    return out, nil
}
