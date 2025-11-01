package trader

import (
    "fmt"
    "log"
    "math"
    "strconv"
    "strings"
    "time"

    "nofx/logger"
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
    }

    return at, nil
}

// 基本信息
func (a *AutoTrader) GetID() string      { return a.id }
func (a *AutoTrader) GetName() string    { return a.name }
func (a *AutoTrader) GetAIModel() string { return a.aiModel }

// 决策日志器
func (a *AutoTrader) GetDecisionLogger() *logger.DecisionLogger { return a.decisionLogger }

// 运行
func (a *AutoTrader) Run() error {
    if a.isRunning {
        return nil
    }
    a.isRunning = true
    a.startTime = time.Now()
    log.Printf(" 启动Trader: %s (%s, %s)", a.name, strings.ToUpper(a.aiModel), strings.ToUpper(a.exchange))

    // 最小工作循环：仅用于统计与心跳，避免空转
    go func() {
        ticker := time.NewTicker(a.scanInterval)
        defer ticker.Stop()
        for a.isRunning {
            a.callCount++
            <-ticker.C
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
    }
}

// 账户信息聚合
func (a *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
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
