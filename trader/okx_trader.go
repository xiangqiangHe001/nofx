package trader

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "math"
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

    // 合约规格缓存
    ctValCache map[string]float64
    lotSzCache map[string]float64
    minSzCache map[string]float64
    cacheMu    sync.RWMutex

    // 持仓模式缓存（long_short_mode 或 net_mode）
    posModeCache      string
    posModeCacheTime  time.Time
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
        lotSzCache: make(map[string]float64),
        minSzCache: make(map[string]float64),
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
    // 设置杠杆（失败则直接返回，避免风控拒绝导致通用错误）
    if err := o.SetLeverage(symbol, leverage); err != nil {
        return nil, fmt.Errorf("设置杠杆失败: %w", err)
    }

    // 获取合约规格（面值、最小张数、步进张数）并校验
    ctVal, lotSz, minSz, exists := o.getInstrumentSpec(instID)
    if !exists {
        return nil, fmt.Errorf("合约不存在或不支持: %s", instID)
    }
    if ctVal <= 0 {
        ctVal = 1.0
    }
    // 计算合约张数，并按 lotSz 向下取整到合法步进
    contracts := quantity / ctVal
    if contracts <= 0 {
        return nil, fmt.Errorf("下单数量过小")
    }
    if lotSz > 0 {
        steps := math.Floor(contracts/lotSz)
        contracts = steps * lotSz
    }
    if contracts < minSz || contracts <= 0 {
        return nil, fmt.Errorf("下单数量过小，最小张数为 %.6f", minSz)
    }
    sz := fmt.Sprintf("%.6f", contracts)

    // 检查持仓模式（双向/净持仓），决定是否传 posSide
    posMode := o.getPositionMode()
    req := map[string]interface{}{
        "instId": instID,
        "tdMode": "isolated",
        "side":   "buy",
        "ordType": "market",
        "sz":     sz,
    }
    if strings.EqualFold(posMode, "long_short_mode") {
        req["posSide"] = "long"
    }
    payloadBytes, _ := json.Marshal(req)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
    if err != nil {
        return nil, err
    }
    var resp struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            OrdID string `json:"ordId"`
            SCode string `json:"sCode"`
            SMsg  string `json:"sMsg"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &resp); err != nil {
        return nil, fmt.Errorf("解析下单响应失败: %w", err)
    }
    if resp.Code != "0" {
        detail := ""
        if len(resp.Data) > 0 && (resp.Data[0].SCode != "" || resp.Data[0].SMsg != "") {
            detail = fmt.Sprintf(" detail: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
        }
        return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX下单失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
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
    if err := o.SetLeverage(symbol, leverage); err != nil {
        return nil, fmt.Errorf("设置杠杆失败: %w", err)
    }

    // 获取合约规格并校验
    ctVal, lotSz, minSz, exists := o.getInstrumentSpec(instID)
    if !exists {
        return nil, fmt.Errorf("合约不存在或不支持: %s", instID)
    }
    if ctVal <= 0 { ctVal = 1.0 }
    contracts := quantity / ctVal
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    if lotSz > 0 {
        steps := math.Floor(contracts/lotSz)
        contracts = steps * lotSz
    }
    if contracts < minSz || contracts <= 0 {
        return nil, fmt.Errorf("下单数量过小，最小张数为 %.6f", minSz)
    }
    sz := fmt.Sprintf("%.6f", contracts)

    // 检查持仓模式（双向/净持仓），决定是否传 posSide
    posMode := o.getPositionMode()
    req := map[string]interface{}{
        "instId": instID,
        "tdMode": "isolated",
        "side":   "sell",
        "ordType": "market",
        "sz":     sz,
    }
    if strings.EqualFold(posMode, "long_short_mode") {
        req["posSide"] = "short"
    }
    payloadBytes, _ := json.Marshal(req)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
    if err != nil { return nil, err }
    var resp struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            OrdID string `json:"ordId"`
            SCode string `json:"sCode"`
            SMsg  string `json:"sMsg"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" {
        detail := ""
        if len(resp.Data) > 0 && (resp.Data[0].SCode != "" || resp.Data[0].SMsg != "") {
            detail = fmt.Sprintf(" detail: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
        }
        return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX下单失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
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
    // 支持 quantity==0 表示全平仓：查询当前持仓张数
    contracts := quantity / ctVal
    if quantity <= 0 {
        c, err := o.getPositionContracts(instID, "long")
        if err != nil {
            return nil, fmt.Errorf("无法获取多仓持仓张数: %w", err)
        }
        contracts = c
    }
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    sz := fmt.Sprintf("%.3f", contracts)

    // 使用结构体生成 JSON，保证字段类型正确（reduceOnly 为布尔）
    posMode := o.getPositionMode()
    req := map[string]interface{}{
        "instId":     instID,
        "tdMode":     "isolated",
        "side":       "sell",
        "ordType":    "market",
        "sz":         sz,
        "reduceOnly": true,
    }
    if strings.EqualFold(posMode, "long_short_mode") {
        req["posSide"] = "long"
    }
    payloadBytes, _ := json.Marshal(req)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
    if err != nil { return nil, err }
    var resp struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            OrdID string `json:"ordId"`
            SCode string `json:"sCode"`
            SMsg  string `json:"sMsg"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" {
        detail := ""
        if len(resp.Data) > 0 && (resp.Data[0].SCode != "" || resp.Data[0].SMsg != "") {
            detail = fmt.Sprintf(" detail: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
        }
        return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX平仓失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
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
    // 支持 quantity==0 表示全平仓：查询当前持仓张数
    contracts := quantity / ctVal
    if quantity <= 0 {
        c, err := o.getPositionContracts(instID, "short")
        if err != nil {
            return nil, fmt.Errorf("无法获取空仓持仓张数: %w", err)
        }
        contracts = c
    }
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    sz := fmt.Sprintf("%.3f", contracts)

    // 使用结构体生成 JSON，保证字段类型正确（reduceOnly 为布尔）
    posMode := o.getPositionMode()
    req := map[string]interface{}{
        "instId":     instID,
        "tdMode":     "isolated",
        "side":       "buy",
        "ordType":    "market",
        "sz":         sz,
        "reduceOnly": true,
    }
    if strings.EqualFold(posMode, "long_short_mode") {
        req["posSide"] = "short"
    }
    payloadBytes, _ := json.Marshal(req)
    respBody, err := o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
    if err != nil { return nil, err }
    var resp struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            OrdID string `json:"ordId"`
            SCode string `json:"sCode"`
            SMsg  string `json:"sMsg"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &resp); err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" {
        detail := ""
        if len(resp.Data) > 0 && (resp.Data[0].SCode != "" || resp.Data[0].SMsg != "") {
            detail = fmt.Sprintf(" detail: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
        }
        return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX平仓失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// SetLeverage 设置杠杆
func (o *OKXTrader) SetLeverage(symbol string, leverage int) error {
    instID := toOKXInstID(symbol)
    // 与下单使用的 tdMode 保持一致（isolated），减少模式不一致导致的失败
    payload := fmt.Sprintf(`{"instId":"%s","lever":"%d","mgnMode":"isolated"}`, instID, leverage)
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
    return fmt.Sprintf("%.6f", quantity), nil
}

// ===== 额外的OKX专用方法 =====

// getPositionContracts 查询指定合约与方向的当前持仓张数（返回绝对值）
func (o *OKXTrader) getPositionContracts(instID string, posSide string) (float64, error) {
    path := fmt.Sprintf("/api/v5/account/positions?instId=%s", instID)
    respBody, err := o.doSignedRequest("GET", path, "")
    if err != nil {
        return 0, err
    }
    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            InstID  string `json:"instId"`
            Pos     string `json:"pos"`
            PosSide string `json:"posSide"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &payload); err != nil {
        return 0, err
    }
    if payload.Code != "0" {
        return 0, fmt.Errorf("OKX positions API error: code=%s msg=%s", payload.Code, payload.Msg)
    }
    for _, p := range payload.Data {
        if p.InstID != instID {
            continue
        }
        // 双向持仓模式优先匹配 posSide；净持仓模式下 posSide 可能为空，使用张数符号判断方向
        if strings.EqualFold(p.PosSide, posSide) || p.PosSide == "" {
            contracts := parseFloat(p.Pos)
            if contracts < 0 {
                contracts = -contracts
            }
            if contracts > 0 {
                return contracts, nil
            }
        }
    }
    return 0, fmt.Errorf("no %s position for %s", posSide, instID)
}

// GetFills 获取近期成交记录（私有接口）
func (o *OKXTrader) GetFills(limit int) ([]map[string]interface{}, error) {
    if o.apiKey == "" || o.secretKey == "" || o.passphrase == "" {
        return nil, fmt.Errorf("OKX API keys are not configured")
    }
    if limit <= 0 {
        limit = 50
    }
    path := fmt.Sprintf("/api/v5/trade/fills?instType=SWAP&limit=%d", limit)
    respBody, err := o.doSignedRequest("GET", path, "")
    if err != nil {
        return nil, err
    }
    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            InstID  string `json:"instId"`
            Side    string `json:"side"`
            PosSide string `json:"posSide"`
            Px      string `json:"px"`
            FillSz  string `json:"fillSz"`
            TradeID string `json:"tradeId"`
            Ts      string `json:"ts"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &payload); err != nil {
        return nil, fmt.Errorf("解析成交记录失败: %w", err)
    }
    if payload.Code != "0" {
        return nil, fmt.Errorf("OKX fills API error: code=%s msg=%s", payload.Code, payload.Msg)
    }

    var result []map[string]interface{}
    for _, f := range payload.Data {
        symbol := fromOKXInstID(f.InstID)
        price := parseFloat(f.Px)
        contracts := parseFloat(f.FillSz)
        ctVal := o.getCTVal(f.InstID)
        if ctVal <= 0 { ctVal = 1.0 }
        qty := contracts * ctVal
        result = append(result, map[string]interface{}{
            "symbol":    symbol,
            "inst_id":   f.InstID,
            "side":      f.Side,
            "pos_side":  f.PosSide,
            "price":     price,
            "contracts": contracts,
            "quantity":  qty,
            "trade_id":  f.TradeID,
            "timestamp": f.Ts,
        })
    }
    return result, nil
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
            LotSz string `json:"lotSz"`
            MinSz string `json:"minSz"`
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
    lot := parseFloat(payload.Data[0].LotSz)
    min := parseFloat(payload.Data[0].MinSz)
    o.cacheMu.Lock()
    o.ctValCache[instID] = ct
    if lot > 0 { o.lotSzCache[instID] = lot }
    if min > 0 { o.minSzCache[instID] = min }
    o.cacheMu.Unlock()
    return ct
}

// getInstrumentSpec 获取合约规格（ctVal, lotSz, minSz）并返回是否存在
func (o *OKXTrader) getInstrumentSpec(instID string) (ctVal, lotSz, minSz float64, exists bool) {
    // 先尝试缓存
    o.cacheMu.RLock()
    ctVal, ctOk := o.ctValCache[instID]
    lotSz, lotOk := o.lotSzCache[instID]
    minSz, minOk := o.minSzCache[instID]
    o.cacheMu.RUnlock()
    if ctOk && lotOk && minOk && ctVal > 0 {
        return ctVal, lotSz, minSz, true
    }
    // 直接调用公共接口
    url := fmt.Sprintf("%s/api/v5/public/instruments?instType=SWAP&instId=%s", o.baseURL, instID)
    resp, err := o.client.Get(url)
    if err != nil {
        return 0, 0, 0, false
    }
    defer resp.Body.Close()
    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            CtVal string `json:"ctVal"`
            LotSz string `json:"lotSz"`
            MinSz string `json:"minSz"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
        return 0, 0, 0, false
    }
    if payload.Code != "0" || len(payload.Data) == 0 {
        return 0, 0, 0, false
    }
    ctVal = parseFloat(payload.Data[0].CtVal)
    lotSz = parseFloat(payload.Data[0].LotSz)
    minSz = parseFloat(payload.Data[0].MinSz)
    if ctVal <= 0 { ctVal = 1.0 }
    o.cacheMu.Lock()
    o.ctValCache[instID] = ctVal
    if lotSz > 0 { o.lotSzCache[instID] = lotSz }
    if minSz > 0 { o.minSzCache[instID] = minSz }
    o.cacheMu.Unlock()
    return ctVal, lotSz, minSz, true
}

// getPositionMode 获取账户持仓模式（long_short_mode 或 net_mode），失败时返回空字符串
func (o *OKXTrader) getPositionMode() string {
    // 优先返回缓存（60秒）
    if o.posModeCache != "" && time.Since(o.posModeCacheTime) < 60*time.Second {
        return o.posModeCache
    }

    // 尝试读取账户配置（最多两次）
    var posMode string
    for i := 0; i < 2; i++ {
        respBody, err := o.doSignedRequest("GET", "/api/v5/account/config", "")
        if err != nil {
            continue
        }
        var payload struct {
            Code string `json:"code"`
            Msg  string `json:"msg"`
            Data []struct {
                PosMode string `json:"posMode"`
            } `json:"data"`
        }
        if err := json.Unmarshal(respBody, &payload); err != nil {
            continue
        }
        if payload.Code == "0" && len(payload.Data) > 0 && payload.Data[0].PosMode != "" {
            posMode = payload.Data[0].PosMode
            break
        }
    }

    // 若成功获取，写入缓存
    if posMode != "" {
        o.posModeCache = posMode
        o.posModeCacheTime = time.Now()
        return posMode
    }

    // 回退：尝试通过持仓数据推断
    respBody, err := o.doSignedRequest("GET", "/api/v5/account/positions?instType=SWAP", "")
    if err == nil {
        var payload struct {
            Code string `json:"code"`
            Msg  string `json:"msg"`
            Data []struct {
                PosSide string `json:"posSide"`
            } `json:"data"`
        }
        if json.Unmarshal(respBody, &payload) == nil && payload.Code == "0" && len(payload.Data) > 0 {
            hasPosSide := false
            for _, p := range payload.Data {
                if strings.TrimSpace(p.PosSide) != "" {
                    hasPosSide = true
                    break
                }
            }
            if hasPosSide {
                o.posModeCache = "long_short_mode"
            } else {
                o.posModeCache = "net_mode"
            }
            o.posModeCacheTime = time.Now()
            return o.posModeCache
        }
    }

    // 仍失败时返回空字符串
    return ""
}