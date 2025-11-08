package trader

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "log"
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

    // 失败节流与统计
    failureMu   sync.Mutex
    lastFail    map[string]time.Time // key: symbol|side
    failCount   map[string]int       // key: symbol|side
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
        lastFail:   make(map[string]time.Time),
        failCount:  make(map[string]int),
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
        return nil, fmt.Errorf("failed to parse balance response: %w", err)
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
        return nil, fmt.Errorf("failed to get positions: %w", err)
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
        return nil, fmt.Errorf("failed to parse positions response: %w", err)
    }
    if payload.Code != "0" {
        return nil, fmt.Errorf("OKX positions API error: code=%s msg=%s", payload.Code, payload.Msg)
    }

    // 返回空数组而不是 null，以便前端一致处理
    result := make([]map[string]interface{}, 0)
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
    // 节流：检测近期失败是否需要冷却
    if err := o.throttleIfNeeded(symbol, "long"); err != nil {
        return nil, err
    }

    // 设置杠杆（失败不阻断开仓，继续尝试下单）
    if err := o.SetLeverage(symbol, leverage); err != nil {
        log.Printf("⚠️ 设置杠杆失败(继续尝试下单): %v", err)
    }
    time.Sleep(2500 * time.Millisecond)

    // 保证金/余额预检与动态缩量，准备合法下单尺寸
    instID, sz, usedQty, _, requiredMargin, avail, err := o.precheckAndPrepareOrder(symbol, "long", quantity, leverage)
    if err != nil {
        return nil, err
    }

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
            
            // 特殊处理账户模式错误 (51010) - 清除持仓模式缓存并重试
            if resp.Data[0].SCode == "51010" && strings.Contains(resp.Data[0].SMsg, "account mode") {
                o.posModeCache = "" // 清除缓存，强制重新检测持仓模式
                o.posModeCacheTime = time.Time{}
                log.Printf("⚠️ 检测到账户模式错误51010，已清除持仓模式缓存，请重新尝试下单")
            }
            // 资金不足错误：返回结构化错误并节流
            if resp.Data[0].SCode == "51008" {
                friendly, suggestion := MapOkxError(resp.Data[0].SCode, resp.Data[0].SMsg)
                o.recordFailure(symbol, "long")
                return nil, &OrderError{
                    Exchange:          "OKX",
                    Symbol:            symbol,
                    Side:              "open_long",
                    Quantity:          usedQty,
                    Leverage:          leverage,
                    RequiredMarginUSD: requiredMargin,
                    AvailableUSD:      avail,
                    Code:              resp.Data[0].SCode,
                    Message:           resp.Data[0].SMsg,
                    Friendly:          friendly,
                    Suggestion:        suggestion,
                }
            }
        }
        // 针对 51000/51010 执行一次自动重试：刷新持仓模式 -> 重新设置杠杆 -> 延时 -> 重新下单
        if resp.Code == "51000" || (len(resp.Data) > 0 && (resp.Data[0].SCode == "51000" || resp.Data[0].SCode == "51010")) {
            log.Printf("⚠️ 触发51000/51010错误，开始自动重试开多：刷新账户模式并重新设置杠杆")
            o.posModeCache = ""
            o.posModeCacheTime = time.Time{}
            posMode = o.getPositionMode()
            if err := o.SetLeverage(symbol, leverage); err != nil {
                log.Printf("⚠️ 重试设置杠杆失败(继续尝试下单): %v", err)
            }
            time.Sleep(2500 * time.Millisecond)
            // 重新构建请求
            req = map[string]interface{}{
                "instId": instID,
                "tdMode": "isolated",
                "side":   "buy",
                "ordType": "market",
                "sz":     sz,
            }
            if strings.EqualFold(posMode, "long_short_mode") {
                req["posSide"] = "long"
            }
            payloadBytes, _ = json.Marshal(req)
            respBody, err = o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
            if err == nil {
                if err := json.Unmarshal(respBody, &resp); err == nil {
                    if resp.Code == "0" && len(resp.Data) > 0 && resp.Data[0].OrdID != "" {
                        return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
                    }
                }
            }
        }
        o.recordFailure(symbol, "long")
        return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        // 针对 51000/51010 执行一次自动重试
        if resp.Data[0].SCode == "51000" || resp.Data[0].SCode == "51010" {
            log.Printf("⚠️ 触发sCode=%s错误，开始自动重试开多：刷新账户模式并重新设置杠杆", resp.Data[0].SCode)
            o.posModeCache = ""
            o.posModeCacheTime = time.Time{}
            posMode = o.getPositionMode()
            if err := o.SetLeverage(symbol, leverage); err != nil {
                log.Printf("⚠️ 重试设置杠杆失败(继续尝试下单): %v", err)
            }
            time.Sleep(2500 * time.Millisecond)
            req = map[string]interface{}{
                "instId": instID,
                "tdMode": "isolated",
                "side":   "buy",
                "ordType": "market",
                "sz":     sz,
            }
            if strings.EqualFold(posMode, "long_short_mode") {
                req["posSide"] = "long"
            }
            payloadBytes, _ = json.Marshal(req)
            respBody, err = o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
            if err == nil {
                if err := json.Unmarshal(respBody, &resp); err == nil {
                    if resp.Code == "0" && len(resp.Data) > 0 && resp.Data[0].OrdID != "" {
                        return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
                    }
                }
            }
        }
        // 资金不足错误：返回结构化错误并节流
        if resp.Data[0].SCode == "51008" {
            friendly, suggestion := MapOkxError(resp.Data[0].SCode, resp.Data[0].SMsg)
            o.recordFailure(symbol, "long")
            return nil, &OrderError{
                Exchange:          "OKX",
                Symbol:            symbol,
                Side:              "open_long",
                Quantity:          usedQty,
                Leverage:          leverage,
                RequiredMarginUSD: requiredMargin,
                AvailableUSD:      avail,
                Code:              resp.Data[0].SCode,
                Message:           resp.Data[0].SMsg,
                Friendly:          friendly,
                Suggestion:        suggestion,
            }
        }
        o.recordFailure(symbol, "long")
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
    // 节流：检测近期失败是否需要冷却
    if err := o.throttleIfNeeded(symbol, "short"); err != nil {
        return nil, err
    }

    // 设置杠杆（失败不阻断开仓，继续尝试下单）
    if err := o.SetLeverage(symbol, leverage); err != nil {
        log.Printf("⚠️ 设置杠杆失败(继续尝试下单): %v", err)
    }
    time.Sleep(2500 * time.Millisecond)

    // 保证金/余额预检与动态缩量，准备合法下单尺寸
    instID, sz, usedQty, _, requiredMargin, avail, err := o.precheckAndPrepareOrder(symbol, "short", quantity, leverage)
    if err != nil {
        return nil, err
    }

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
    err = json.Unmarshal(respBody, &resp)
    if err != nil { return nil, fmt.Errorf("解析下单响应失败: %w", err) }
    if resp.Code != "0" {
        detail := ""
        if len(resp.Data) > 0 && (resp.Data[0].SCode != "" || resp.Data[0].SMsg != "") {
            detail = fmt.Sprintf(" detail: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
            
            // 特殊处理账户模式错误 (51010) - 清除持仓模式缓存并重试
            if resp.Data[0].SCode == "51010" && strings.Contains(resp.Data[0].SMsg, "account mode") {
                o.posModeCache = "" // 清除缓存，强制重新检测持仓模式
                o.posModeCacheTime = time.Time{}
                log.Printf("⚠️ 检测到账户模式错误51010，已清除持仓模式缓存，请重新尝试下单")
            }
            if resp.Data[0].SCode == "51008" {
                friendly, suggestion := MapOkxError(resp.Data[0].SCode, resp.Data[0].SMsg)
                o.recordFailure(symbol, "short")
                return nil, &OrderError{
                    Exchange:          "OKX",
                    Symbol:            symbol,
                    Side:              "open_short",
                    Quantity:          usedQty,
                    Leverage:          leverage,
                    RequiredMarginUSD: requiredMargin,
                    AvailableUSD:      avail,
                    Code:              resp.Data[0].SCode,
                    Message:           resp.Data[0].SMsg,
                    Friendly:          friendly,
                    Suggestion:        suggestion,
                }
            }
        }
        // 针对 51000/51010 执行一次自动重试：刷新持仓模式 -> 重新设置杠杆 -> 延时 -> 重新下单
        if resp.Code == "51000" || (len(resp.Data) > 0 && (resp.Data[0].SCode == "51000" || resp.Data[0].SCode == "51010")) {
            log.Printf("⚠️ 触发51000/51010错误，开始自动重试开空：刷新账户模式并重新设置杠杆")
            o.posModeCache = ""
            o.posModeCacheTime = time.Time{}
            posMode = o.getPositionMode()
            err = o.SetLeverage(symbol, leverage)
            if err != nil {
                log.Printf("⚠️ 重试设置杠杆失败(继续尝试下单): %v", err)
            }
            time.Sleep(2500 * time.Millisecond)
            // 重新构建请求
            req = map[string]interface{}{
                "instId": instID,
                "tdMode": "isolated",
                "side":   "sell",
                "ordType": "market",
                "sz":     sz,
            }
            if strings.EqualFold(posMode, "long_short_mode") {
                req["posSide"] = "short"
            }
            payloadBytes, _ = json.Marshal(req)
            respBody, err = o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
            if err == nil {
                err = json.Unmarshal(respBody, &resp)
                if err == nil {
                    if resp.Code == "0" && len(resp.Data) > 0 && resp.Data[0].OrdID != "" {
                        return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
                    }
                }
            }
        }
        o.recordFailure(symbol, "short")
        return nil, fmt.Errorf("OKX下单失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        // 针对 51000/51010 执行一次自动重试
        if resp.Data[0].SCode == "51000" || resp.Data[0].SCode == "51010" {
            log.Printf("⚠️ 触发sCode=%s错误，开始自动重试开空：刷新账户模式并重新设置杠杆", resp.Data[0].SCode)
            o.posModeCache = ""
            o.posModeCacheTime = time.Time{}
            posMode = o.getPositionMode()
            err = o.SetLeverage(symbol, leverage)
            if err != nil {
                log.Printf("⚠️ 重试设置杠杆失败(继续尝试下单): %v", err)
            }
            time.Sleep(2500 * time.Millisecond)
            req = map[string]interface{}{
                "instId": instID,
                "tdMode": "isolated",
                "side":   "sell",
                "ordType": "market",
                "sz":     sz,
            }
            if strings.EqualFold(posMode, "long_short_mode") {
                req["posSide"] = "short"
            }
            payloadBytes, _ = json.Marshal(req)
            respBody, err = o.doSignedRequest("POST", "/api/v5/trade/order", string(payloadBytes))
            if err == nil {
                if err := json.Unmarshal(respBody, &resp); err == nil {
                    if resp.Code == "0" && len(resp.Data) > 0 && resp.Data[0].OrdID != "" {
                        return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
                    }
                }
            }
        }
        if resp.Data[0].SCode == "51008" {
            friendly, suggestion := MapOkxError(resp.Data[0].SCode, resp.Data[0].SMsg)
            o.recordFailure(symbol, "short")
            return nil, &OrderError{
                Exchange:          "OKX",
                Symbol:            symbol,
                Side:              "open_short",
                Quantity:          usedQty,
                Leverage:          leverage,
                RequiredMarginUSD: requiredMargin,
                AvailableUSD:      avail,
                Code:              resp.Data[0].SCode,
                Message:           resp.Data[0].SMsg,
                Friendly:          friendly,
                Suggestion:        suggestion,
            }
        }
        o.recordFailure(symbol, "short")
        return nil, fmt.Errorf("OKX下单失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// ===== 预检、缩量与节流辅助 =====

// precheckAndPrepareOrder 执行保证金预检，必要时动态缩量，并返回合法的下单尺寸字符串
func (o *OKXTrader) precheckAndPrepareOrder(symbol string, side string, quantity float64, leverage int) (string, string, float64, float64, float64, float64, error) {
    instID := toOKXInstID(symbol)
    // 规格与张数计算
    ctVal, lotSz, minSz, exists := o.getInstrumentSpec(instID)
    if !exists {
        return "", "", 0, 0, 0, 0, fmt.Errorf("合约不存在或不支持: %s", instID)
    }
    if ctVal <= 0 {
        ctVal = 1.0
    }
    contracts := quantity / ctVal
    if contracts <= 0 {
        return "", "", 0, 0, 0, 0, fmt.Errorf("下单数量过小")
    }
    // 当前价格与保证金需求
    price, err := o.GetMarketPrice(symbol)
    if err != nil || price <= 0 {
        return "", "", 0, 0, 0, 0, fmt.Errorf("获取价格失败: %v", err)
    }
    requiredMargin := (quantity * price) / float64(leverage)
    // 预留手续费与滑点缓冲
    requiredMargin *= 1.002
    // 账户可用余额
    bal, err := o.GetBalance()
    if err != nil {
        return "", "", 0, 0, 0, 0, fmt.Errorf("获取账户余额失败: %v", err)
    }
    avail := 0.0
    if v, ok := bal["availableBalance"].(float64); ok {
        avail = v
    }

    // 动态缩量以适配可用余额
    if avail < requiredMargin {
        maxUSD := avail * float64(leverage)
        // 安全折扣，避免再次因小数/步进导致失败
        maxUSD *= 0.98
        newQty := maxUSD / price
        if newQty <= 0 {
            friendly, suggestion := MapOkxError("51008", "insufficient margin")
            return "", "", 0, price, requiredMargin, avail, &OrderError{
                Exchange:          "OKX",
                Symbol:            symbol,
                Side:              "open_" + side,
                Quantity:          quantity,
                Leverage:          leverage,
                RequiredMarginUSD: requiredMargin,
                AvailableUSD:      avail,
                Code:              "51008",
                Message:           "insufficient margin",
                Friendly:          friendly,
                Suggestion:        suggestion,
            }
        }
        // 重新计算合约张数并向下取整到合法步进
        contracts = newQty / ctVal
        if lotSz > 0 {
            steps := math.Floor(contracts/lotSz)
            contracts = steps * lotSz
        }
        if contracts < minSz || contracts <= 0 {
            friendly, suggestion := MapOkxError("51008", "insufficient margin for min size")
            return "", "", 0, price, requiredMargin, avail, &OrderError{
                Exchange:          "OKX",
                Symbol:            symbol,
                Side:              "open_" + side,
                Quantity:          quantity,
                Leverage:          leverage,
                RequiredMarginUSD: requiredMargin,
                AvailableUSD:      avail,
                Code:              "51008",
                Message:           "insufficient margin for min size",
                Friendly:          friendly,
                Suggestion:        suggestion,
            }
        }
    }

    // 步进/最小张数校验（无论是否缩量）
    if lotSz > 0 {
        steps := math.Floor(contracts/lotSz)
        contracts = steps * lotSz
    }
    if contracts < minSz || contracts <= 0 {
        return "", "", 0, price, requiredMargin, avail, fmt.Errorf("下单数量过小，最小张数为 %.6f", minSz)
    }
    sz := fmt.Sprintf("%.6f", contracts)
    usedQty := contracts * ctVal
    return instID, sz, usedQty, price, requiredMargin, avail, nil
}

// throttleIfNeeded 如果近期失败过多，则返回节流错误
func (o *OKXTrader) throttleIfNeeded(symbol, side string) error {
    key := symbol + "|" + side
    o.failureMu.Lock()
    defer o.failureMu.Unlock()
    last, ok := o.lastFail[key]
    if !ok {
        return nil
    }
    count := o.failCount[key]
    cooldown := 60 * time.Second
    if count >= 3 {
        cooldown = 5 * time.Minute
    }
    if time.Since(last) < cooldown {
        remaining := cooldown - time.Since(last)
        friendly := "近期该交易对下单连续失败，已进入冷却期。"
        suggestion := fmt.Sprintf("请等待 %.0f 秒后重试，或降低下单数量。", remaining.Seconds())
        return &OrderError{
            Exchange:   "OKX",
            Symbol:     symbol,
            Side:       "open_" + side,
            Quantity:   0,
            Leverage:   0,
            Code:       "THROTTLE",
            Message:    "cooldown",
            Friendly:   friendly,
            Suggestion: suggestion,
        }
    }
    return nil
}

// recordFailure 记录一次失败，用于节流
func (o *OKXTrader) recordFailure(symbol, side string) {
    key := symbol + "|" + side
    o.failureMu.Lock()
    defer o.failureMu.Unlock()
    o.lastFail[key] = time.Now()
    o.failCount[key] = o.failCount[key] + 1
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
    // 对合约张数按最小步长取整，避免因数量精度导致下单失败
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    if ct, lot, min, exists := o.getInstrumentSpec(instID); exists {
        if ct <= 0 { ct = 1.0 }
        if lot > 0 {
            steps := math.Floor(contracts/lot)
            contracts = steps * lot
        }
        if contracts < min || contracts <= 0 {
            return nil, fmt.Errorf("下单数量过小，最小张数为 %.6f", min)
        }
    }
    sz := fmt.Sprintf("%.6f", contracts)

    // 使用结构体生成 JSON，保证字段类型正确（reduceOnly 为布尔）
    posMode := o.getPositionMode()
    // 检测该持仓的保证金模式（isolated/cross），避免模式不匹配导致失败
    mgnMode := o.getPositionMarginMode(instID, "long")
    if mgnMode == "" { mgnMode = "isolated" }
    // 观测性日志：记录将要平仓的关键参数
    log.Printf("[OKX CloseLong] instID=%s posMode=%s mgnMode=%s contracts=%.6f", instID, posMode, mgnMode, contracts)
    req := map[string]interface{}{
        "instId":     instID,
        "tdMode":     mgnMode,
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
            
            // 特殊处理账户模式错误 (51010) - 清除持仓模式缓存并重试
            if resp.Data[0].SCode == "51010" && strings.Contains(resp.Data[0].SMsg, "account mode") {
                o.posModeCache = "" // 清除缓存，强制重新检测持仓模式
                o.posModeCacheTime = time.Time{}
                log.Printf("⚠️ 检测到账户模式错误51010，已清除持仓模式缓存，请重新尝试下单")
            }
        }
        return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX平仓失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
    // 成功后清除持仓缓存，确保后续查询实时刷新
    o.cachedPositions = nil
    o.positionsCacheTime = time.Time{}
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
    // 对合约张数按最小步长取整，避免因数量精度导致下单失败
    if contracts <= 0 { return nil, fmt.Errorf("下单数量过小") }
    if ct, lot, min, exists := o.getInstrumentSpec(instID); exists {
        if ct <= 0 { ct = 1.0 }
        if lot > 0 {
            steps := math.Floor(contracts/lot)
            contracts = steps * lot
        }
        if contracts < min || contracts <= 0 {
            return nil, fmt.Errorf("下单数量过小，最小张数为 %.6f", min)
        }
    }
    sz := fmt.Sprintf("%.6f", contracts)

    // 使用结构体生成 JSON，保证字段类型正确（reduceOnly 为布尔）
    posMode := o.getPositionMode()
    // 检测该持仓的保证金模式（isolated/cross），与开仓保持一致
    mgnMode := o.getPositionMarginMode(instID, "short")
    if mgnMode == "" { mgnMode = "isolated" }
    // 观测性日志：记录将要平仓的关键参数
    log.Printf("[OKX CloseShort] instID=%s posMode=%s mgnMode=%s contracts=%.6f", instID, posMode, mgnMode, contracts)
    req := map[string]interface{}{
        "instId":     instID,
        "tdMode":     mgnMode,
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
            
            // 特殊处理账户模式错误 (51010) - 清除持仓模式缓存并重试
            if resp.Data[0].SCode == "51010" && strings.Contains(resp.Data[0].SMsg, "account mode") {
                o.posModeCache = "" // 清除缓存，强制重新检测持仓模式
                o.posModeCacheTime = time.Time{}
                log.Printf("⚠️ 检测到账户模式错误51010，已清除持仓模式缓存，请重新尝试下单")
            }
        }
        return nil, fmt.Errorf("OKX平仓失败: code=%s msg=%s%s", resp.Code, resp.Msg, detail)
    }
    if len(resp.Data) > 0 && resp.Data[0].SCode != "" && resp.Data[0].SCode != "0" {
        return nil, fmt.Errorf("OKX平仓失败: sCode=%s sMsg=%s", resp.Data[0].SCode, resp.Data[0].SMsg)
    }
    // 成功后清除持仓缓存，确保后续查询实时刷新
    o.cachedPositions = nil
    o.positionsCacheTime = time.Time{}
    return map[string]interface{}{"orderId": resp.Data[0].OrdID}, nil
}

// SetLeverage 设置杠杆
func (o *OKXTrader) SetLeverage(symbol string, leverage int) error {
    instID := toOKXInstID(symbol)

    // 检测账户持仓模式：long_short_mode 需要传 posSide；net_mode 不需要
    posMode := o.getPositionMode()

    // 优先尝试在双向持仓模式下分别为 long/short 设置杠杆，以避免未知侧的参数错误
    if strings.EqualFold(posMode, "long_short_mode") {
        // 为 long/short 两侧各设置一次杠杆
        for _, side := range []string{"long", "short"} {
            payload := fmt.Sprintf(`{"instId":"%s","lever":"%d","mgnMode":"isolated","posSide":"%s"}`, instID, leverage, side)
            respBody, err := o.doSignedRequest("POST", "/api/v5/account/set-leverage", payload)
            if err != nil {
                return fmt.Errorf("设置杠杆失败(%s): %w", side, err)
            }
            var resp struct { Code string `json:"code"`; Msg string `json:"msg"` }
            if err := json.Unmarshal(respBody, &resp); err != nil {
                return fmt.Errorf("解析设置杠杆响应失败(%s): %w", side, err)
            }
            if resp.Code != "0" {
                // 如果因账户模式导致错误，清除持仓模式缓存，便于后续重新检测
                if resp.Code == "51010" && strings.Contains(resp.Msg, "account mode") {
                    o.posModeCache = ""
                    o.posModeCacheTime = time.Time{}
                }
                return fmt.Errorf("设置杠杆失败(%s): code=%s msg=%s", side, resp.Code, resp.Msg)
            }
        }
        return nil
    }

    // 净持仓模式或未知模式：不传 posSide
    payload := fmt.Sprintf(`{"instId":"%s","lever":"%d","mgnMode":"isolated"}`, instID, leverage)
    respBody, err := o.doSignedRequest("POST", "/api/v5/account/set-leverage", payload)
    if err != nil { return err }
    var resp struct { Code string `json:"code"`; Msg string `json:"msg"` }
    if err := json.Unmarshal(respBody, &resp); err != nil { return fmt.Errorf("解析设置杠杆响应失败: %w", err) }
    if resp.Code != "0" {
        // 如果提示需要 posSide，说明模式检测可能不准确，尝试为两侧设置一次
        if resp.Code == "51000" && strings.Contains(strings.ToLower(resp.Msg), "posside") {
            for _, side := range []string{"long", "short"} {
                payload := fmt.Sprintf(`{"instId":"%s","lever":"%d","mgnMode":"isolated","posSide":"%s"}`, instID, leverage, side)
                respBody2, err2 := o.doSignedRequest("POST", "/api/v5/account/set-leverage", payload)
                if err2 != nil { return fmt.Errorf("设置杠杆失败(%s): %w", side, err2) }
                var resp2 struct { Code string `json:"code"`; Msg string `json:"msg"` }
                if err := json.Unmarshal(respBody2, &resp2); err != nil { return fmt.Errorf("解析设置杠杆响应失败(%s): %w", side, err) }
                if resp2.Code != "0" { return fmt.Errorf("设置杠杆失败(%s): code=%s msg=%s", side, resp2.Code, resp2.Msg) }
            }
            return nil
        }
        return fmt.Errorf("设置杠杆失败: code=%s msg=%s", resp.Code, resp.Msg)
    }
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
        // 双向持仓模式：posSide 为 long/short，直接按方向匹配
        if strings.EqualFold(p.PosSide, posSide) {
            contracts := parseFloat(p.Pos)
            if contracts < 0 {
                contracts = -contracts
            }
            if contracts > 0 {
                return contracts, nil
            }
            continue
        }
        // 净持仓模式：posSide 可能为 "net" 或空，使用张数符号判断方向
        if p.PosSide == "net" || p.PosSide == "" {
            contracts := parseFloat(p.Pos)
            if strings.EqualFold(posSide, "short") && contracts < 0 {
                return -contracts, nil
            }
            if strings.EqualFold(posSide, "long") && contracts > 0 {
                return contracts, nil
            }
            // 方向不匹配则继续查找
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
            // OKX 文档字段为 fillPx（成交价格），部分示例/兼容可能出现 px；这里双字段兼容
            FillPx  string `json:"fillPx"`
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
        // 特殊处理账户模式错误 (51010) - 清除持仓模式缓存
        if payload.Code == "51010" && strings.Contains(payload.Msg, "account mode") {
            o.posModeCache = ""
            o.posModeCacheTime = time.Time{}
            log.Printf("⚠️ 检测到账户模式错误51010，已清除持仓模式缓存，请重新尝试获取成交记录")
        }
        return nil, fmt.Errorf("OKX fills API error: code=%s msg=%s", payload.Code, payload.Msg)
    }

    var result []map[string]interface{}
    for _, f := range payload.Data {
        symbol := fromOKXInstID(f.InstID)
        // 兼容 fillPx 与 px，优先使用 fillPx
        priceStr := f.FillPx
        if priceStr == "" { priceStr = f.Px }
        price := parseFloat(priceStr)
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

// getPositionMarginMode 查询指定合约与方向的保证金模式（返回 "isolated" 或 "cross"，未知返回空字符串）
func (o *OKXTrader) getPositionMarginMode(instID string, posSide string) string {
    path := fmt.Sprintf("/api/v5/account/positions?instId=%s", instID)
    respBody, err := o.doSignedRequest("GET", path, "")
    if err != nil {
        return ""
    }
    var payload struct {
        Code string `json:"code"`
        Msg  string `json:"msg"`
        Data []struct {
            InstID  string `json:"instId"`
            PosSide string `json:"posSide"`
            MgnMode string `json:"mgnMode"`
        } `json:"data"`
    }
    if err := json.Unmarshal(respBody, &payload); err != nil {
        return ""
    }
    if payload.Code != "0" {
        return ""
    }
    for _, p := range payload.Data {
        if p.InstID != instID {
            continue
        }
        // 双向持仓模式优先匹配 posSide；净持仓模式下 posSide 可能为空，允许匹配空
        if strings.EqualFold(p.PosSide, posSide) || p.PosSide == "" {
            if strings.EqualFold(p.MgnMode, "isolated") {
                return "isolated"
            }
            if strings.EqualFold(p.MgnMode, "cross") {
                return "cross"
            }
        }
    }
    return ""
}