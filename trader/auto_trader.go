package trader

import (
    "encoding/json"
    "fmt"
    "log"
    "math"
    "strconv"
    "strings"
    "time"

    "nofx/decision"
    "nofx/logger"
    "nofx/mcp"
    "nofx/pool"
)

// AutoTraderConfig 自动交易器配置（与 manager.TraderManager 构造保持一致）
type AutoTraderConfig struct {
    ID                    string
    Name                  string
    AIModel               string
    Exchange              string

    // Binance
    BinanceAPIKey    string
    BinanceSecretKey string

    // Hyperliquid
    HyperliquidPrivateKey string
    HyperliquidWalletAddr string
    HyperliquidTestnet    bool

    // Aster
    AsterUser       string
    AsterSigner     string
    AsterPrivateKey string

    // OKX
    OKXAPIKey     string
    OKXSecretKey  string
    OKXPassphrase string

    // AI Keys / Custom API
    UseQwen         bool
    DeepSeekKey     string
    QwenKey         string
    CustomAPIURL    string
    CustomAPIKey    string
    CustomModelName string

    // Runtime / Risk / Leverage
    ScanInterval    time.Duration // 扫描周期，直接使用time.Duration
    InitialBalance  float64
    BTCETHLeverage  int
    AltcoinLeverage int
    MaxDailyLoss    float64
    MaxDrawdown     float64
    StopTradingTime time.Duration

    // External services
    CoinPoolAPIURL string
}

// AutoTrader 自动交易器主体
type AutoTrader struct {
    id       string
    name     string
    aiModel  string
    exchange string

    client Trader

    decisionLogger *logger.DecisionLogger

    // 运行状态
    isRunning bool
    startTime time.Time
    lastReset time.Time
    stopUntil time.Time
    callCount int

    // 资金与周期
    initialBalance float64
    scanInterval   time.Duration

    // AI & leverage
    aiClient        *mcp.Client
    btcethLeverage  int
    altcoinLeverage int

    // 执行控制
    executionEnabled bool
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(cfg AutoTraderConfig) (*AutoTrader, error) {
    if cfg.ID == "" || cfg.Name == "" {
        return nil, fmt.Errorf("缺少ID或Name")
    }

    exch := strings.ToLower(strings.TrimSpace(cfg.Exchange))
    if exch == "" {
        exch = "binance"
    }

    var client Trader
    switch exch {
    case "binance":
        client = NewFuturesTrader(cfg.BinanceAPIKey, cfg.BinanceSecretKey)
    case "hyperliquid":
        cl, err := NewHyperliquidTrader(cfg.HyperliquidPrivateKey, cfg.HyperliquidWalletAddr, cfg.HyperliquidTestnet)
        if err != nil {
            return nil, err
        }
        client = cl
    case "aster":
        cl, err := NewAsterTrader(cfg.AsterUser, cfg.AsterSigner, cfg.AsterPrivateKey)
        if err != nil {
            return nil, err
        }
        client = cl
    case "okx":
        cl, err := NewOKXTrader(cfg.OKXAPIKey, cfg.OKXSecretKey, cfg.OKXPassphrase)
        if err != nil {
            return nil, err
        }
        client = cl
    default:
        return nil, fmt.Errorf("不支持的交易所: %s", exch)
    }

    scan := cfg.ScanInterval
    if scan <= 0 {
        scan = 3 * time.Minute // 默认3分钟
    }

    logDir := fmt.Sprintf("decision_logs/%s", cfg.ID)

    at := &AutoTrader{
        id:             cfg.ID,
        name:           cfg.Name,
        aiModel:        cfg.AIModel,
        exchange:       exch,
        client:         client,
        decisionLogger: logger.NewDecisionLogger(logDir),
        isRunning:      false,
        startTime:      time.Time{},
        lastReset:      time.Now(),
        initialBalance: cfg.InitialBalance,
        scanInterval:   scan,
        btcethLeverage:  cfg.BTCETHLeverage,
        altcoinLeverage: cfg.AltcoinLeverage,
    }

    // 初始化AI客户端
    ai := mcp.New()
    model := strings.ToLower(strings.TrimSpace(cfg.AIModel))
    switch model {
    case "qwen":
        ai.SetQwenAPIKey(cfg.QwenKey, "")
    case "custom":
        ai.SetCustomAPI(cfg.CustomAPIURL, cfg.CustomAPIKey, cfg.CustomModelName)
    default:
        ai.SetDeepSeekAPIKey(cfg.DeepSeekKey)
    }
    at.aiClient = ai

    return at, nil
}

// 基本信息
func (a *AutoTrader) GetID() string      { return a.id }
func (a *AutoTrader) GetName() string    { return a.name }
func (a *AutoTrader) GetAIModel() string { return a.aiModel }

// 决策日志器
func (a *AutoTrader) GetDecisionLogger() *logger.DecisionLogger { return a.decisionLogger }

// GetOKXClient 如果当前交易所是 OKX，返回底层 OKXTrader 客户端；否则返回nil
func (a *AutoTrader) GetOKXClient() *OKXTrader {
    if strings.ToLower(a.exchange) != "okx" {
        return nil
    }
    if cl, ok := a.client.(*OKXTrader); ok {
        return cl
    }
    return nil
}

// 运行
func (a *AutoTrader) Run() error {
    if a.isRunning {
        return nil
    }
    a.isRunning = true
    a.startTime = time.Now()
    log.Printf(" 启动Trader: %s (%s, %s)", a.name, strings.ToUpper(a.aiModel), strings.ToUpper(a.exchange))

    // 先执行一次决策周期，避免首次空转
    a.runDecisionCycle()

    // 周期性执行决策
    go func() {
        ticker := time.NewTicker(a.scanInterval)
        defer ticker.Stop()
        for a.isRunning {
            <-ticker.C
            a.runDecisionCycle()
        }
    }()

    return nil
}

// 停止
func (a *AutoTrader) Stop() { a.isRunning = false }

// 状态信息
func (a *AutoTrader) GetStatus() map[string]interface{} {
    runtimeMinutes := 0.0
    startStr := ""
    if !a.startTime.IsZero() {
        runtimeMinutes = time.Since(a.startTime).Minutes()
        startStr = a.startTime.Format(time.RFC3339)
    }
    stopUntil := ""
    if !a.stopUntil.IsZero() {
        stopUntil = a.stopUntil.Format(time.RFC3339)
    }
    return map[string]interface{}{
        "trader_id":       a.id,
        "trader_name":     a.name,
        "ai_model":        a.aiModel,
        "is_running":      a.isRunning,
        "start_time":      startStr,
        "runtime_minutes": runtimeMinutes,
        "call_count":      a.callCount,
        "initial_balance": a.initialBalance,
        "scan_interval":   a.scanInterval.String(),
        "stop_until":      stopUntil,
        "last_reset_time": a.lastReset.Format(time.RFC3339),
        "ai_provider":     a.aiModel,
        "execution_enabled": a.executionEnabled,
    }
}

// SetExecutionEnabled 设置是否启用自动执行（仅记录建议时仍保留周期分析与日志）
func (a *AutoTrader) SetExecutionEnabled(enabled bool) { a.executionEnabled = enabled }
// IsExecutionEnabled 获取自动执行开关状态
func (a *AutoTrader) IsExecutionEnabled() bool { return a.executionEnabled }

// 账户信息聚合
func (a *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
    // 如果是 OKX，优先使用 OKX 原始私有接口数据对齐 yuanbao.py
    if okx := a.GetOKXClient(); okx != nil {
        rawAcc, err := okx.GetRawAccountBalance()
        if err != nil {
            // 回退到通用路径
        } else if code, _ := rawAcc["code"].(string); code == "0" {
            dataList, _ := rawAcc["data"].([]interface{})
            if len(dataList) > 0 {
                first, _ := dataList[0].(map[string]interface{})

                // totalEq 为账户总净值（Total Equity）
                totalEquity := 0.0
                if s, ok := first["totalEq"].(string); ok {
                    if f, e := strconvParseFloat(s); e == nil { totalEquity = f }
                }

                // 可用余额：优先使用顶层 availEq；否则从 USDT details.availBal 提取
                availableBalance := 0.0
                if s, ok := first["availEq"].(string); ok {
                    if f, e := strconvParseFloat(s); e == nil { availableBalance = f }
                } else if details, ok := first["details"].([]interface{}); ok {
                    for _, d := range details {
                        dm, _ := d.(map[string]interface{})
                        if dm["ccy"] == "USDT" {
                            if s2, ok2 := dm["availBal"].(string); ok2 {
                                if f2, e2 := strconvParseFloat(s2); e2 == nil { availableBalance = f2 }
                            }
                            // 若USDT的availBal缺失，尝试cashBal作为可用余额近似
                            if availableBalance <= 0 {
                                if s3, ok3 := dm["cashBal"].(string); ok3 {
                                    if f3, e3 := strconvParseFloat(s3); e3 == nil { availableBalance = f3 }
                                }
                            }
                            break
                        }
                    }
                    // 若未找到USDT或其可用余额为0，求和所有币种的availBal作为总体可用近似
                    if availableBalance <= 0 {
                        sumAvail := 0.0
                        for _, d := range details {
                            dm, _ := d.(map[string]interface{})
                            if s2, ok2 := dm["availBal"].(string); ok2 {
                                if f2, e2 := strconvParseFloat(s2); e2 == nil { sumAvail += f2 }
                            }
                        }
                        if sumAvail > 0 { availableBalance = sumAvail }
                    }
                }
                
                // 进一步回退：若可用余额仍为0，使用统一余额接口回退
                if availableBalance <= 0 {
                    if balMap, err := okx.GetBalance(); err == nil && balMap != nil {
                        if v := getFloat(balMap, "availableBalance"); v > 0 {
                            availableBalance = v
                        }
                    }
                }

                // 未实现盈亏：从原始持仓 upl 字段汇总，仅统计非零持仓
                unrealizedProfit := 0.0
                rawPos, err := okx.GetRawPositions()
                positionCount := 0
                if err == nil {
                    if code2, _ := rawPos["code"].(string); code2 == "0" {
                        if posList, ok := rawPos["data"].([]interface{}); ok {
                            for _, it := range posList {
                                pm, _ := it.(map[string]interface{})
                                // 过滤零持仓
                                posStr, _ := pm["pos"].(string)
                                posQty := 0.0
                                if f, e := strconvParseFloat(posStr); e == nil { posQty = f }
                                if math.Abs(posQty) <= 0 { continue }
                                positionCount++
                                if uplStr, ok := pm["upl"].(string); ok {
                                    if f, e := strconvParseFloat(uplStr); e == nil {
                                        unrealizedProfit += f
                                    }
                                }
                            }
                        }
                    }
                }

                // 钱包余额：优先使用USDT的cashBal；否则近似为 Equity - 未实现盈亏
                walletBalance := totalEquity - unrealizedProfit
                if details, ok := first["details"].([]interface{}); ok {
                    for _, d := range details {
                        dm, _ := d.(map[string]interface{})
                        if dm["ccy"] == "USDT" {
                            if s2, ok2 := dm["cashBal"].(string); ok2 {
                                if f2, e2 := strconvParseFloat(s2); e2 == nil { walletBalance = f2 }
                            }
                            break
                        }
                    }
                }
                // 回退：若钱包余额仍为0，使用统一余额接口回退
                if walletBalance <= 0 {
                    if balMap, err := okx.GetBalance(); err == nil && balMap != nil {
                        if v := getFloat(balMap, "totalWalletBalance"); v > 0 {
                            walletBalance = v
                        }
                    }
                }

                // 保证金占用：优先使用顶层 imr；否则按持仓计算（名义价值/杠杆）
                marginUsed := 0.0
                if s, ok := first["imr"].(string); ok {
                    if f, e := strconvParseFloat(s); e == nil { marginUsed = f }
                }
                if marginUsed == 0 {
                    // 使用标准化持仓计算保证金
                    positionsNorm, err := a.client.GetPositions()
                    if err == nil {
                        for _, p := range positionsNorm {
                            qty := getFloat(p, "positionAmt")
                            entry := getFloat(p, "entryPrice")
                            lev := getFloat(p, "leverage")
                            if qty > 0 && entry > 0 && lev > 0 {
                                positionValue := qty * entry
                                marginUsed += positionValue / lev
                            }
                        }
                    }
                }

                totalPnL := totalEquity - a.initialBalance
                totalPnLPct := 0.0
                if a.initialBalance > 0 {
                    totalPnLPct = (totalPnL / a.initialBalance) * 100
                }
                marginUsedPct := 0.0
                if totalEquity > 0 {
                    marginUsedPct = (marginUsed / totalEquity) * 100
                }

                return map[string]interface{}{
                    "total_equity":         round2(totalEquity),
                    "wallet_balance":       round2(walletBalance),
                    "unrealized_profit":    round2(unrealizedProfit),
                    "available_balance":    round2(availableBalance),
                    "total_pnl":            round2(totalPnL),
                    "total_pnl_pct":        round2(totalPnLPct),
                    "total_unrealized_pnl": round2(unrealizedProfit),
                    "initial_balance":      round2(a.initialBalance),
                    "daily_pnl":            0.0,
                    "position_count":       positionCount,
                    "margin_used":          round2(marginUsed),
                    "margin_used_pct":      round2(marginUsedPct),
                }, nil
            }
        }
    }

    // 通用路径（非OKX或OKX原始数据不可用时）
    bal, err := a.client.GetBalance()
    if err != nil {
        return nil, err
    }
    positions, err := a.client.GetPositions()
    if err != nil {
        positions = []map[string]interface{}{}
    }

    wallet := getFloat(bal, "totalWalletBalance")
    avail := getFloat(bal, "availableBalance")
    unreal := getFloat(bal, "totalUnrealizedProfit")

    totalEquity := wallet + unreal
    totalPnL := totalEquity - a.initialBalance
    totalPnLPct := 0.0
    if a.initialBalance > 0 {
        totalPnLPct = (totalPnL / a.initialBalance) * 100
    }

    marginUsed := 0.0
    for _, p := range positions {
        qty := getFloat(p, "positionAmt")
        entry := getFloat(p, "entryPrice")
        lev := getFloat(p, "leverage")
        if qty > 0 && entry > 0 && lev > 0 {
            positionValue := qty * entry
            marginUsed += positionValue / lev
        }
    }
    marginUsedPct := 0.0
    if totalEquity > 0 {
        marginUsedPct = (marginUsed / totalEquity) * 100
    }

    return map[string]interface{}{
        "total_equity":        round2(totalEquity),
        "wallet_balance":      round2(wallet),
        "unrealized_profit":   round2(unreal),
        "available_balance":   round2(avail),
        "total_pnl":           round2(totalPnL),
        "total_pnl_pct":       round2(totalPnLPct),
        "total_unrealized_pnl": round2(unreal),
        "initial_balance":     round2(a.initialBalance),
        "daily_pnl":           0.0,
        "position_count":      len(positions),
        "margin_used":         round2(marginUsed),
        "margin_used_pct":     round2(marginUsedPct),
    }, nil
}

// GetPositions 转换为API需要的字段命名
func (a *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
    raw, err := a.client.GetPositions()
    if err != nil {
        return nil, err
    }
    out := make([]map[string]interface{}, 0, len(raw))
    for _, p := range raw {
        qty := getFloat(p, "positionAmt")
        entry := getFloat(p, "entryPrice")
        mark := getFloat(p, "markPrice")
        lev := getFloat(p, "leverage")
        side, _ := p["side"].(string)
        liq := getFloat(p, "liquidationPrice")

        pnl := 0.0
        if qty > 0 && entry > 0 && mark > 0 {
            if side == "short" {
                pnl = qty * (entry - mark)
            } else {
                pnl = qty * (mark - entry)
            }
        }
        positionValue := qty * entry
        marginUsed := 0.0
        if lev > 0 {
            marginUsed = positionValue / lev
        }
        pnlPct := 0.0
        if marginUsed > 0 {
            pnlPct = (pnl / marginUsed) * 100
        }

        out = append(out, map[string]interface{}{
            "symbol":             p["symbol"],
            "side":               side,
            "entry_price":        round2(entry),
            "mark_price":         round2(mark),
            "quantity":           round3(qty),
            "leverage":           round2(lev),
            "unrealized_pnl":     round2(pnl),
            "unrealized_pnl_pct": round2(pnlPct),
            "liquidation_price":  round2(liq),
            "margin_used":        round2(marginUsed),
        })
    }
    return out, nil
}

// 辅助函数
func getFloat(m map[string]interface{}, key string) float64 {
    v, ok := m[key]
    if !ok {
        return 0
    }
    switch t := v.(type) {
    case float64:
        return t
    case float32:
        return float64(t)
    case int:
        return float64(t)
    case int64:
        return float64(t)
    case string:
        f, _ := strconvParseFloat(t)
        return f
    default:
        return 0
    }
}

func strconvParseFloat(s string) (float64, error) {
    if strings.TrimSpace(s) == "" {
        return 0, nil
    }
    return strconv.ParseFloat(s, 64)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// runDecisionCycle 构建上下文、获取AI决策并记录日志
func (a *AutoTrader) runDecisionCycle() {
    a.callCount++

    // 获取账户信息与持仓
    account, err := a.GetAccountInfo()
    if err != nil {
        log.Printf("⚠️ 获取账户信息失败，使用默认账户快照: %v", err)
        account = map[string]interface{}{
            "total_equity":       a.initialBalance,
            "available_balance":  a.initialBalance,
            "total_pnl":          0.0,
            "total_pnl_pct":      0.0,
            "margin_used":        0.0,
            "margin_used_pct":    0.0,
            "position_count":     0,
        }
    }
    posList, err := a.GetPositions()
    if err != nil {
        log.Printf("⚠️ 获取持仓失败: %v", err)
        posList = []map[string]interface{}{}
    }

    // 构建决策上下文中的账户与持仓结构
    accInfo := decision.AccountInfo{
        TotalEquity:      getFloat(account, "total_equity"),
        AvailableBalance: getFloat(account, "available_balance"),
        TotalPnL:         getFloat(account, "total_pnl"),
        TotalPnLPct:      getFloat(account, "total_pnl_pct"),
        MarginUsed:       getFloat(account, "margin_used"),
        MarginUsedPct:    getFloat(account, "margin_used_pct"),
        PositionCount:    int(getFloat(map[string]interface{}{"position_count": float64(len(posList))}, "position_count")),
    }

    var positions []decision.PositionInfo
    nowMs := time.Now().UnixMilli()
    for _, p := range posList {
        positions = append(positions, decision.PositionInfo{
            Symbol:           toString(p["symbol"]),
            Side:             toString(p["side"]),
            EntryPrice:       getFloat(p, "entry_price"),
            MarkPrice:        getFloat(p, "mark_price"),
            Quantity:         getFloat(p, "quantity"),
            Leverage:         int(getFloat(p, "leverage")),
            UnrealizedPnL:    getFloat(p, "unrealized_pnl"),
            UnrealizedPnLPct: getFloat(p, "unrealized_pnl_pct"),
            LiquidationPrice: getFloat(p, "liquidation_price"),
            MarginUsed:       getFloat(p, "margin_used"),
            UpdateTime:       nowMs,
        })
    }

    // 合并币种池（AI500 + OI Top）作为候选列表
    merged, err := pool.GetMergedCoinPool(20)
    candidateCoins := make([]decision.CandidateCoin, 0)
    if err != nil {
        log.Printf("⚠️ 获取合并币种池失败: %v", err)
    } else {
        for _, symbol := range merged.AllSymbols {
            candidateCoins = append(candidateCoins, decision.CandidateCoin{
                Symbol:  symbol,
                Sources: merged.SymbolSources[symbol],
            })
        }
    }

    // 历史表现（用于系统提示中的夏普比率反馈）
    perf, _ := a.decisionLogger.AnalyzePerformance(100)

    // 构建上下文
    ctx := &decision.Context{
        CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
        RuntimeMinutes:  int(time.Since(a.startTime).Minutes()),
        CallCount:       a.callCount,
        Account:         accInfo,
        Positions:       positions,
        CandidateCoins:  candidateCoins,
        Performance:     perf,
        BTCETHLeverage:  a.btcethLeverage,
        AltcoinLeverage: a.altcoinLeverage,
    }

    // 调用AI决策引擎
    fullDecision, err := decision.GetFullDecision(ctx, a.aiClient)
    record := &logger.DecisionRecord{
        AccountState: logger.AccountSnapshot{
            TotalBalance:          accInfo.TotalEquity,
            AvailableBalance:      accInfo.AvailableBalance,
            TotalUnrealizedProfit: accInfo.TotalPnL,
            PositionCount:         accInfo.PositionCount,
            MarginUsedPct:         accInfo.MarginUsedPct,
        },
        Positions:      []logger.PositionSnapshot{},
        CandidateCoins: []string{},
        ExecutionLog:   []string{},
        Success:        err == nil,
    }

    for _, p := range positions {
        record.Positions = append(record.Positions, logger.PositionSnapshot{
            Symbol:           p.Symbol,
            Side:             p.Side,
            PositionAmt:      p.Quantity,
            EntryPrice:       p.EntryPrice,
            MarkPrice:        p.MarkPrice,
            UnrealizedProfit: p.UnrealizedPnL,
            Leverage:         float64(p.Leverage),
            LiquidationPrice: p.LiquidationPrice,
        })
    }
    for _, c := range candidateCoins {
        record.CandidateCoins = append(record.CandidateCoins, c.Symbol)
    }

    if err != nil {
        record.InputPrompt = ""
        record.CoTTrace = ""
        record.DecisionJSON = "[]"
        record.ErrorMessage = fmt.Sprintf("AI决策失败: %v", err)
        record.ExecutionLog = append(record.ExecutionLog, "AI调用失败，未执行交易，仅记录账户与持仓快照")
        _ = a.decisionLogger.LogDecision(record)
        return
    }

    decBytes, _ := json.Marshal(fullDecision.Decisions)
    record.InputPrompt = fullDecision.UserPrompt
    record.CoTTrace = fullDecision.CoTTrace
    record.DecisionJSON = string(decBytes)
    if !a.executionEnabled {
        record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("AI生成%d条决策；自动执行已禁用，本周期仅分析并记录", len(fullDecision.Decisions)))
    } else {
        // 目前未接入真实下单路径，保留提示与日志结构
        record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("AI生成%d条决策；自动执行已启用（当前实现未下单，仅记录建议）", len(fullDecision.Decisions)))
    }

    if err := a.decisionLogger.LogDecision(record); err != nil {
        log.Printf("⚠️ 记录决策失败: %v", err)
    }
}

func toString(v interface{}) string {
    if v == nil { return "" }
    switch t := v.(type) {
    case string:
        return t
    default:
        return fmt.Sprintf("%v", t)
    }
}
