package trader

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "sync"
    "time"
)

// OKXTrader OKX永续合约交易器（最小实现，优先支持DryRun与价格获取）
type OKXTrader struct {
    apiKey     string
    secretKey  string
    passphrase string
    client     *http.Client
    baseURL    string

    // 简单缓存
    cachedBalance     map[string]interface{}
    balanceCacheTime  time.Time
    cachedPositions   []map[string]interface{}
    positionsCacheTime time.Time

    // 合约面值缓存
    ctValCache map[string]float64
    cacheMu    sync.RWMutex
}

// NewOKXTrader 创建OKX交易器
func NewOKXTrader(apiKey, secretKey, passphrase string) (*OKXTrader, error) {
    return &OKXTrader{
        apiKey:     apiKey,
        secretKey:  secretKey,
        passphrase: passphrase,
        // 与 okx.py 保持一致，强制使用 127.0.0.1:7897 作为代理端口
        client: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: func(_ *http.Request) (*url.URL, error) {
            return url.Parse("http://127.0.0.1:7897")
        }}},
        baseURL:    "https://www.okx.com",
        ctValCache: make(map[string]float64),
    }, nil
}

// ===== Trader 接口实现 =====

// GetBalance 获取账户余额（私有接口，如果未配置密钥返回错误以便上层容错）
func (o *OKXTrader) GetBalance() (map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX API keys are not configured")
    }
    // 缓存60秒
    if o.cachedBalance != nil && time.Since(o.balanceCacheTime) < 60*time.Second {
        return o.cachedBalance, nil
    }

    path := "/api/v5/account/balance"
    body := ""
    respBody, err := o.doSignedRequest("GET", path, body)
    if err != nil {
        return nil, fmt.Errorf("failed to get account balance: %w", err)
    }

    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            TotalEq string `json:"totalEq"`
            Details []struct {
                Ccy      string `json:"ccy"`
                AvailBal string `json:"availBal"`
                Eq       string `json:"eq"`
            } `json:"details"`
        } `json:"data"`
    }

    if err := json.Unmarshal(respBody, &payload); err != nil {
        return nil, fmt.Errorf("failed to parse balance response: %w")
    }
    if payload.Code != "0" || len(payload.Data) == 0 {
        return nil, fmt.Errorf("OKX balance API error: code=%s msg=%s", payload.Code, payload.Msg)
    }

    // 总净值
    totalEq := parseFloat(payload.Data[0].TotalEq)

    // 可用余额（优先USDT）
    available := 0.0
    wallet := 0.0
    for _, d := range payload.Data[0].Details {
        if d.Ccy == "USDT" {
            available = parseFloat(d.AvailBal)
            wallet = parseFloat(d.Eq)
            break
        }
    }
    if wallet == 0 { // 回退：用总净值作为钱包近似
        wallet = totalEq
    }

    result := map[string]interface{}{
        "totalWalletBalance":    wallet,
        "availableBalance":      available,
        "totalUnrealizedProfit": 0.0, // 未实现盈亏由持仓计算
    }
    o.cachedBalance = result
    o.balanceCacheTime = time.Now()
    return result, nil
}

// GetPositions 获取所有持仓
func (o *OKXTrader) GetPositions() ([]map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX API keys are not configured")
    }
    // 缓存30秒
    if o.cachedPositions != nil && time.Since(o.positionsCacheTime) < 30*time.Second {
        return o.cachedPositions, nil
    }

    path := "/api/v5/account/positions?instType=SWAP"
    body := ""
    respBody, err := o.doSignedRequest("GET", path, body)
    if err != nil {
        return nil, fmt.Errorf("failed to get positions: %w")
    }

    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            InstID  string `json:"instId"`
            Pos     string `json:"pos"`
            PosSide string `json:"posSide"`
            AvgPx   string `json:"avgPx"`
            Lever   string `json:"lever"`
            Upl     string `json:"upl"`
            LiqPx   string `json:"liqPx"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &payload); err != nil {
        return nil, fmt.Errorf("failed to parse positions response: %w")
    }
    if payload.Code != "0" {
        return nil, fmt.Errorf("OKX positions API error: code=%s msg=%s", payload.Code, payload.Msg)
    }

    var result []map[string]interface{}
    for _, p := range payload.Data {
        qtyContracts := parseFloat(p.Pos)
        if qtyContracts == 0 {
            continue
        }

        // 获取合约面值ctVal用于换算数量到标的单位
        ctVal := o.getCTVal(p.InstID)
        qty := qtyContracts * ctVal

        // 获取标记价格（使用ticker的last）
        markPrice, _ := o.GetMarketPrice(fromOKXInstID(p.InstID))

        entry := parseFloat(p.AvgPx)
        lev := parseFloat(p.Lever)
        upl := parseFloat(p.Upl)
        liq := parseFloat(p.LiqPx)

        // 优先使用 posSide 判断方向（双向持仓模式下，pos 始终为正数）
        side := "long"
        if strings.EqualFold(p.PosSide, "short") {
            side = "short"
        } else if strings.EqualFold(p.PosSide, "long") {
            side = "long"
        } else {
            // 兼容净持仓模式：使用数量符号判断
            if qtyContracts < 0 {
                side = "short"
                qty = -qty
            }
        }

        result = append(result, map[string]interface{}{
            "symbol":           fromOKXInstID(p.InstID),
            "side":             side,
            "positionAmt":      qty,
            "entryPrice":       entry,
            "markPrice":        markPrice,
            "unRealizedProfit": upl,
            "leverage":         lev,
            "liquidationPrice": liq,
        })
    }

    o.cachedPositions = result
    o.positionsCacheTime = time.Now()
    return result, nil
}

// OpenLong 开多仓
func (o *OKXTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX未配置API密钥")
    }
    instID := toOKXInstID(symbol)
    // 设置杠杆（容错，不阻塞下单）
    _ = o.SetLeverage(symbol, leverage)

    // 计算合约张数
    ctVal := o.getCTVal(instID)
    if ctVal <= 0 {
        ctVal = 1.0
    }
    contracts := quantity / ctVal
    if contracts <= 0 {
        return nil, fmt.Errorf("下单数量过小")
    }
    // OKX张数按整数或最小增量处理（保守：四舍五入到3位小数）
    sz := fmt.Sprintf("%.3f", contracts)

    payload := fmt.Sprintf(`{"instId":"%s","tdMode":"cross","side":"buy","ordType":"market","sz":"%s"}`, instID, sz)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", payload)
    if err != nil {
        return nil, err
    }
    var resp struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct { OrdID string `json:"ordId"` } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &resp); err != nil {
        return nil, fmt.Errorf("解析下单响应失败: %w", err)
    }
    if resp.Code != "0" {
        return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s", resp.Code, resp.Msg)
    }
    result := map[string]interface{}{"orderId": resp.Data[0].OrdID}
    return result, nil
}

// OpenShort 开空仓
func (o *OKXTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX未配置API密钥")
    }
    instID := toOKXInstID(symbol)
    _ = o.SetLeverage(symbol, leverage)

    ctVal := o.getCTVal(instID)
    if ctVal <= 0 { ctVal = 1.0 }
    contracts := quantity / ctVal
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    sz := fmt.Sprintf("%.3f", contracts)

    payload := fmt.Sprintf(`{"instId":"%s","tdMode":"cross","side":"sell","ordType":"market","sz":"%s"}`, instID, sz)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", payload)
    if err != nil { return nil, err }
    var resp struct { Code string `json:"code"`; Msg string `json:"msg"`; Data []struct{ OrdID string `json:"ordId"` } `json:"data"` }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" { return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s", resp.Code, resp.Msg) }
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// CloseLong 平多仓
func (o *OKXTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX未配置API密钥")
    }
    instID := toOKXInstID(symbol)
    ctVal := o.getCTVal(instID)
    if ctVal <= 0 { ctVal = 1.0 }
    contracts := quantity / ctVal
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    sz := fmt.Sprintf("%.3f", contracts)

    payload := fmt.Sprintf(`{"instId":"%s","tdMode":"cross","side":"sell","ordType":"market","sz":"%s","reduceOnly":"true"}`, instID, sz)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", payload)
    if err != nil { return nil, err }
    var resp struct { Code string `json:"code"`; Msg string `json:"msg"`; Data []struct{ OrdID string `json:"ordId"` } `json:"data"` }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" { return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s", resp.Code, resp.Msg) }
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// CloseShort 平空仓
func (o *OKXTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX未配置API密钥")
    }
    instID := toOKXInstID(symbol)
    ctVal := o.getCTVal(instID)
    if ctVal <= 0 { ctVal = 1.0 }
    contracts := quantity / ctVal
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    sz := fmt.Sprintf("%.3f", contracts)

    payload := fmt.Sprintf(`{"instId":"%s","tdMode":"cross","side":"buy","ordType":"market","sz":"%s","reduceOnly":"true"}`, instID, sz)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", payload)
    if err != nil { return nil, err }
    var resp struct { Code string `json:"code"`; Msg string `json:"msg"`; Data []struct{ OrdID string `json:"ordId"` } `json:"data"` }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" { return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s", resp.Code, resp.Msg) }
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// SetLeverage 设置杠杆
func (o *OKXTrader) SetLeverage(symbol string, leverage int) error {
    instID := toOKXInstID(symbol)
    payload := fmt.Sprintf(`{"instId":"%s","lever":"%d","mgnMode":"cross"}`, instID, leverage)
    respBody, err := o.doSignedRequest("POST", "/api/v5/account/set-leverage", payload)
    if err != nil { return err }
    var resp struct { Code string `json:"code"`; Msg string `json:"msg"` }
    if err := json.Unmarshal(respBody, &resp); err != nil { return fmt.Errorf("解析设置杠杆响应失败: %w", err) }
    if resp.Code != "0" { return fmt.Errorf("设置杠杆失败: code=%s msg=%s", resp.Code, resp.Msg) }
    return nil
}

// GetMarketPrice 获取市场价格（使用OKX公开行情）
func (o *OKXTrader) GetMarketPrice(symbol string) (float64, error) {
    instID := toOKXInstID(symbol)
    url := fmt.Sprintf("%s/api/v5/market/ticker?instId=%s", o.baseURL, instID)
    resp, err := o.client.Get(url)
    if err != nil {
        return 0, err
    }
    defer resp.Body.Close()

    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            Last string `json:"last"`
        } `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
        return 0, err
    }
    if payload.Code != "0" || len(payload.Data) == 0 {
        return 0, fmt.Errorf("OKX ticker API error: code=%s msg=%s", payload.Code, payload.Msg)
    }
    return parseFloat(payload.Data[0].Last), nil
}

// SetStopLoss 设置止损
func (o *OKXTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
    return fmt.Errorf("OKX止损暂未实现")
}

// SetTakeProfit 设置止盈
func (o *OKXTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
    return fmt.Errorf("OKX止盈暂未实现")
}

// CancelAllOrders 取消所有挂单
func (o *OKXTrader) CancelAllOrders(symbol string) error {
    return fmt.Errorf("OKX取消挂单暂未实现")
}

// FormatQuantity 简单格式化（OKX最小数量因合约不同而异，这里采用保守的3位小数）
func (o *OKXTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
    return fmt.Sprintf("%.3f", quantity), nil
}

// ===== 辅助函数 =====

// toOKXInstID 将标准化的Binance符号转换为OKX instId（永续合约）
// 例: BTCUSDT -> BTC-USDT-SWAP
func toOKXInstID(symbol string) string {
    s := strings.ToUpper(symbol)
    if strings.HasSuffix(s, "USDT") {
        base := strings.TrimSuffix(s, "USDT")
        return base + "-USDT-SWAP"
    }
    // 回退：直接加 -SWAP
    return s + "-SWAP"
}

// fromOKXInstID 将OKX instId转换为标准符号
func fromOKXInstID(instID string) string {
    // 例: BTC-USDT-SWAP -> BTCUSDT
    up := strings.ToUpper(instID)
    up = strings.TrimSuffix(up, "-SWAP")
    up = strings.ReplaceAll(up, "-", "")
    return up
}

func parseFloat(s string) float64 {
    var f float64
    _, _ = fmt.Sscanf(s, "%f", &f)
    return f
}

// buildSignature 生成OKX签名（预留）
func (o *OKXTrader) buildSignature(timestamp, method, path, body string) string {
    payload := timestamp + method + path + body
    mac := hmac.New(sha256.New, []byte(o.secretKey))
    mac.Write([]byte(payload))
    return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// doSignedRequest 执行签名请求
func (o *OKXTrader) doSignedRequest(method, path, body string) ([]byte, error) {
    ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
    sig := o.buildSignature(ts, method, path, body)

    var reader io.Reader
    if method == "POST" || method == "PUT" {
        reader = strings.NewReader(body)
    }
    req, err := http.NewRequest(method, o.baseURL+path, reader)
    if err != nil {
        return nil, err
    }
    req.Header.Set("OK-ACCESS-KEY", o.apiKey)
    req.Header.Set("OK-ACCESS-SIGN", sig)
    req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
    req.Header.Set("OK-ACCESS-PASSPHRASE", o.passphrase)
    req.Header.Set("Content-Type", "application/json")

    resp, err := o.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    b, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    return b, nil
}

// getCTVal 获取合约面值ctVal（OKX合约规格）
func (o *OKXTrader) getCTVal(instID string) float64 {
    o.cacheMu.RLock()
    v, ok := o.ctValCache[instID]
    o.cacheMu.RUnlock()
    if ok && v > 0 {
        return v
    }

    url := fmt.Sprintf("%s/api/v5/public/instruments?instType=SWAP&instId=%s", o.baseURL, instID)
    resp, err := o.client.Get(url)
    if err != nil {
        return 1.0
    }
    defer resp.Body.Close()

    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            CtVal string `json:"ctVal"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
        return 1.0
    }
    if payload.Code != "0" || len(payload.Data) == 0 {
        return 1.0
    }
    ct := parseFloat(payload.Data[0].CtVal)
    if ct <= 0 {
        ct = 1.0
    }
    o.cacheMu.Lock()
    o.ctValCache[instID] = ct
    o.cacheMu.Unlock()
    return ct
}