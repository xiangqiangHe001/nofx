package trader

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"strings"
	"time"
)

// AutoTraderConfig 鑷姩浜ゆ槗閰嶇疆锛堢畝鍖栫増 - AI鍏ㄦ潈鍐崇瓥锛?
type AutoTraderConfig struct {
	// Trader鏍囪瘑
	ID      string // Trader鍞竴鏍囪瘑锛堢敤浜庢棩蹇楃洰褰曠瓑锛?
	Name    string // Trader鏄剧ず鍚嶇О
	AIModel string // AI妯″瀷: "qwen" 鎴?"deepseek"

	// 浜ゆ槗骞冲彴閫夋嫨
	Exchange string // "binance", "hyperliquid" 鎴?"aster"

	// 甯佸畨API閰嶇疆
	BinanceAPIKey    string
	BinanceSecretKey string

	// Hyperliquid閰嶇疆
	HyperliquidPrivateKey string
	HyperliquidWalletAddr string
	HyperliquidTestnet    bool

	// Aster閰嶇疆
	AsterUser       string // Aster涓婚挶鍖呭湴鍧€
	AsterSigner     string // Aster API閽卞寘鍦板潃
	AsterPrivateKey string // Aster API閽卞寘绉侀挜

	CoinPoolAPIURL string

	// AI閰嶇疆
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 鑷畾涔堿I API閰嶇疆
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// 鎵弿閰嶇疆
	ScanInterval time.Duration // 鎵弿闂撮殧锛堝缓璁?鍒嗛挓锛?

	// 璐︽埛閰嶇疆
	InitialBalance float64 // 鍒濆閲戦锛堢敤浜庤绠楃泩浜忥紝闇€鎵嬪姩璁剧疆锛?

	// 鏉犳潌閰嶇疆
	BTCETHLeverage  int // BTC鍜孍TH鐨勬潬鏉嗗€嶆暟
	AltcoinLeverage int // 灞卞甯佺殑鏉犳潌鍊嶆暟

	// 椋庨櫓鎺у埗锛堜粎浣滀负鎻愮ず锛孉I鍙嚜涓诲喅瀹氾級
	MaxDailyLoss    float64       // 鏈€澶ф棩浜忔崯鐧惧垎姣旓紙鎻愮ず锛?
	MaxDrawdown     float64       // 鏈€澶у洖鎾ょ櫨鍒嗘瘮锛堟彁绀猴級
	StopTradingTime time.Duration // 瑙﹀彂椋庢帶鍚庢殏鍋滄椂闀?
}

// AutoTrader 鑷姩浜ゆ槗鍣?
type AutoTrader struct {
	id                    string // Trader鍞竴鏍囪瘑
	name                  string // Trader鏄剧ず鍚嶇О
	aiModel               string // AI妯″瀷鍚嶇О
	exchange              string // 浜ゆ槗骞冲彴鍚嶇О
	config                AutoTraderConfig
	trader                Trader // 浣跨敤Trader鎺ュ彛锛堟敮鎸佸骞冲彴锛?
	mcpClient             *mcp.Client
	decisionLogger        *logger.DecisionLogger // 鍐崇瓥鏃ュ織璁板綍鍣?
	initialBalance        float64
	dailyPnL              float64
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	startTime             time.Time        // 绯荤粺鍚姩鏃堕棿
	callCount             int              // AI璋冪敤娆℃暟
	positionFirstSeenTime map[string]int64 // 鎸佷粨棣栨鍑虹幇鏃堕棿 (symbol_side -> timestamp姣)
}

// NewAutoTrader 鍒涘缓鑷姩浜ゆ槗鍣?
func NewAutoTrader(config AutoTraderConfig) (*AutoTrader, error) {
	// 璁剧疆榛樿鍊?
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	mcpClient := mcp.New()

	// 鍒濆鍖朅I
	if config.AIModel == "custom" {
		// 浣跨敤鑷畾涔堿PI
		mcpClient.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("馃 [%s] 浣跨敤鑷畾涔堿I API: %s (妯″瀷: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		// 浣跨敤Qwen
		mcpClient.SetQwenAPIKey(config.QwenKey, "")
		log.Printf("馃 [%s] 浣跨敤闃块噷浜慟wen AI", config.Name)
	} else {
		// 榛樿浣跨敤DeepSeek
		mcpClient.SetDeepSeekAPIKey(config.DeepSeekKey)
		log.Printf("馃 [%s] 浣跨敤DeepSeek AI", config.Name)
	}

	// 鍒濆鍖栧竵绉嶆睜API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 璁剧疆榛樿浜ゆ槗骞冲彴
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 鏍规嵁閰嶇疆鍒涘缓瀵瑰簲鐨勪氦鏄撳櫒
	var trader Trader
	var err error

	switch config.Exchange {
	case "binance":
		log.Printf("馃彟 [%s] 浣跨敤甯佸畨鍚堢害浜ゆ槗", config.Name)
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey)
	case "hyperliquid":
		log.Printf("馃彟 [%s] 浣跨敤Hyperliquid浜ゆ槗", config.Name)
		trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("鍒濆鍖朒yperliquid浜ゆ槗鍣ㄥけ璐? %w", err)
		}
	case "aster":
		log.Printf("馃彟 [%s] 浣跨敤Aster浜ゆ槗", config.Name)
		trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("鍒濆鍖朅ster浜ゆ槗鍣ㄥけ璐? %w", err)
		}
	case "okx":
log.Printf(" [%s] 使用OKX交易", config.Name)
trader, err = NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase)
if err != nil {
return nil, fmt.Errorf("初始化OKX交易器失败: %w", err)
}
default:
		return nil, fmt.Errorf("涓嶆敮鎸佺殑浜ゆ槗骞冲彴: %s", config.Exchange)
	}

	// 楠岃瘉鍒濆閲戦閰嶇疆
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("鍒濆閲戦蹇呴』澶т簬0锛岃鍦ㄩ厤缃腑璁剧疆InitialBalance")
	}

	// 鍒濆鍖栧喅绛栨棩蹇楄褰曞櫒锛堜娇鐢╰rader ID鍒涘缓鐙珛鐩綍锛?
	logDir := fmt.Sprintf("decision_logs/%s", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		config:                config,
		trader:                trader,
		mcpClient:             mcpClient,
		decisionLogger:        decisionLogger,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
	}, nil
}

// Run 杩愯鑷姩浜ゆ槗涓诲惊鐜?
func (at *AutoTrader) Run() error {
	at.isRunning = true
	log.Println("馃殌 AI椹卞姩鑷姩浜ゆ槗绯荤粺鍚姩")
	log.Printf("馃挵 鍒濆浣欓: %.2f USDT", at.initialBalance)
	log.Printf("鈿欙笍  鎵弿闂撮殧: %v", at.config.ScanInterval)
	log.Println("馃 AI灏嗗叏鏉冨喅瀹氭潬鏉嗐€佷粨浣嶅ぇ灏忋€佹鎹熸鐩堢瓑鍙傛暟")

	ticker := time.NewTicker(at.config.ScanInterval)
	defer ticker.Stop()

	// 棣栨绔嬪嵆鎵ц
	if err := at.runCycle(); err != nil {
		log.Printf("鉂?鎵ц澶辫触: %v", err)
	}

	for at.isRunning {
		select {
		case <-ticker.C:
			if err := at.runCycle(); err != nil {
				log.Printf("鉂?鎵ц澶辫触: %v", err)
			}
		}
	}

	return nil
}

// Stop 鍋滄鑷姩浜ゆ槗
func (at *AutoTrader) Stop() {
	at.isRunning = false
	log.Println("鈴?鑷姩浜ゆ槗绯荤粺鍋滄")
}

// runCycle 杩愯涓€涓氦鏄撳懆鏈燂紙浣跨敤AI鍏ㄦ潈鍐崇瓥锛?
func (at *AutoTrader) runCycle() error {
	at.callCount++

	log.Printf("\n" + strings.Repeat("=", 70))
	log.Printf("鈴?%s - AI鍐崇瓥鍛ㄦ湡 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Printf(strings.Repeat("=", 70))

	// 鍒涘缓鍐崇瓥璁板綍
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 妫€鏌ユ槸鍚﹂渶瑕佸仠姝氦鏄?
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		log.Printf("鈴?椋庨櫓鎺у埗锛氭殏鍋滀氦鏄撲腑锛屽墿浣?%.0f 鍒嗛挓", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("椋庨櫓鎺у埗鏆傚仠涓紝鍓╀綑 %.0f 鍒嗛挓", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 2. 閲嶇疆鏃ョ泩浜忥紙姣忓ぉ閲嶇疆锛?
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("馃搮 鏃ョ泩浜忓凡閲嶇疆")
	}

	// 3. 鏀堕泦浜ゆ槗涓婁笅鏂?
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("鏋勫缓浜ゆ槗涓婁笅鏂囧け璐? %v", err)
		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("鏋勫缓浜ゆ槗涓婁笅鏂囧け璐? %w", err)
	}

	// 淇濆瓨璐︽埛鐘舵€佸揩鐓?
	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: ctx.Account.TotalPnL,
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	// 淇濆瓨鎸佷粨蹇収
	for _, pos := range ctx.Positions {
		record.Positions = append(record.Positions, logger.PositionSnapshot{
			Symbol:           pos.Symbol,
			Side:             pos.Side,
			PositionAmt:      pos.Quantity,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			UnrealizedProfit: pos.UnrealizedPnL,
			Leverage:         float64(pos.Leverage),
			LiquidationPrice: pos.LiquidationPrice,
		})
	}

	// 淇濆瓨鍊欓€夊竵绉嶅垪琛?
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	log.Printf("馃搳 璐︽埛鍑€鍊? %.2f USDT | 鍙敤: %.2f USDT | 鎸佷粨: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 4. 璋冪敤AI鑾峰彇瀹屾暣鍐崇瓥
	log.Println("馃 姝ｅ湪璇锋眰AI鍒嗘瀽骞跺喅绛?..")
	decision, err := decision.GetFullDecision(ctx, at.mcpClient)

	// 鍗充娇鏈夐敊璇紝涔熶繚瀛樻€濈淮閾俱€佸喅绛栧拰杈撳叆prompt锛堢敤浜巇ebug锛?
	if decision != nil {
		record.InputPrompt = decision.UserPrompt
		record.CoTTrace = decision.CoTTrace
		if len(decision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(decision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("鑾峰彇AI鍐崇瓥澶辫触: %v", err)

		// 鎵撳嵃AI鎬濈淮閾撅紙鍗充娇鏈夐敊璇級
		if decision != nil && decision.CoTTrace != "" {
			log.Printf("\n" + strings.Repeat("-", 70))
			log.Println("馃挱 AI鎬濈淮閾惧垎鏋愶紙閿欒鎯呭喌锛?")
			log.Println(strings.Repeat("-", 70))
			log.Println(decision.CoTTrace)
			log.Printf(strings.Repeat("-", 70) + "\n")
		}

		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("鑾峰彇AI鍐崇瓥澶辫触: %w", err)
	}

	// 5. 鎵撳嵃AI鎬濈淮閾?
	log.Printf("\n" + strings.Repeat("-", 70))
	log.Println("馃挱 AI鎬濈淮閾惧垎鏋?")
	log.Println(strings.Repeat("-", 70))
	log.Println(decision.CoTTrace)
	log.Printf(strings.Repeat("-", 70) + "\n")

	// 6. 鎵撳嵃AI鍐崇瓥
	log.Printf("馃搵 AI鍐崇瓥鍒楄〃 (%d 涓?:\n", len(decision.Decisions))
	for i, d := range decision.Decisions {
		log.Printf("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
		if d.Action == "open_long" || d.Action == "open_short" {
			log.Printf("      鏉犳潌: %dx | 浠撲綅: %.2f USDT | 姝㈡崯: %.4f | 姝㈢泩: %.4f",
				d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
		}
	}
	log.Println()

	// 7. 瀵瑰喅绛栨帓搴忥細纭繚鍏堝钩浠撳悗寮€浠擄紙闃叉浠撲綅鍙犲姞瓒呴檺锛?
	sortedDecisions := sortDecisionsByPriority(decision.Decisions)

	log.Println("馃攧 鎵ц椤哄簭锛堝凡浼樺寲锛? 鍏堝钩浠撯啋鍚庡紑浠?)
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 鎵ц鍐崇瓥骞惰褰曠粨鏋?
	for _, d := range sortedDecisions {
		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			log.Printf("鉂?鎵ц鍐崇瓥澶辫触 (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("鉂?%s %s 澶辫触: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("鉁?%s %s 鎴愬姛", d.Symbol, d.Action))
			// 鎴愬姛鎵ц鍚庣煭鏆傚欢杩?
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 8. 淇濆瓨鍐崇瓥璁板綍
	if err := at.decisionLogger.LogDecision(record); err != nil {
		log.Printf("鈿?淇濆瓨鍐崇瓥璁板綍澶辫触: %v", err)
	}

	return nil
}

// buildTradingContext 鏋勫缓浜ゆ槗涓婁笅鏂?
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 鑾峰彇璐︽埛淇℃伅
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇璐︽埛浣欓澶辫触: %w", err)
	}

	// 鑾峰彇璐︽埛瀛楁
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 閽卞寘浣欓 + 鏈疄鐜扮泩浜?
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 2. 鑾峰彇鎸佷粨淇℃伅
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇鎸佷粨澶辫触: %w", err)
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 褰撳墠鎸佷粨鐨刱ey闆嗗悎锛堢敤浜庢竻鐞嗗凡骞充粨鐨勮褰曪級
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 绌轰粨鏁伴噺涓鸿礋锛岃浆涓烘鏁?
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// 璁＄畻鍗犵敤淇濊瘉閲戯紙浼扮畻锛?
		leverage := 10 // 榛樿鍊硷紝瀹為檯搴旇浠庢寔浠撲俊鎭幏鍙?
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// 璁＄畻鐩堜簭鐧惧垎姣?
		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		// 璺熻釜鎸佷粨棣栨鍑虹幇鏃堕棿
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 鏂版寔浠擄紝璁板綍褰撳墠鏃堕棿
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		positionInfos = append(positionInfos, decision.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
		})
	}

	// 娓呯悊宸插钩浠撶殑鎸佷粨璁板綍
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 3. 鑾峰彇鍚堝苟鐨勫€欓€夊竵绉嶆睜锛圓I500 + OI Top锛屽幓閲嶏級
	// 鏃犺鏈夋病鏈夋寔浠擄紝閮藉垎鏋愮浉鍚屾暟閲忕殑甯佺锛堣AI鐪嬪埌鎵€鏈夊ソ鏈轰細锛?
	// AI浼氭牴鎹繚璇侀噾浣跨敤鐜囧拰鐜版湁鎸佷粨鎯呭喌锛岃嚜宸卞喅瀹氭槸鍚﹁鎹粨
	const ai500Limit = 20 // AI500鍙栧墠20涓瘎鍒嗘渶楂樼殑甯佺

	// 鑾峰彇鍚堝苟鍚庣殑甯佺姹狅紙AI500 + OI Top锛?
	mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇鍚堝苟甯佺姹犲け璐? %w", err)
	}

	// 鏋勫缓鍊欓€夊竵绉嶅垪琛紙鍖呭惈鏉ユ簮淇℃伅锛?
	var candidateCoins []decision.CandidateCoin
	for _, symbol := range mergedPool.AllSymbols {
		sources := mergedPool.SymbolSources[symbol]
		candidateCoins = append(candidateCoins, decision.CandidateCoin{
			Symbol:  symbol,
			Sources: sources, // "ai500" 鍜?鎴?"oi_top"
		})
	}

	log.Printf("馃搵 鍚堝苟甯佺姹? AI500鍓?d + OI_Top20 = 鎬昏%d涓€欓€夊竵绉?,
		ai500Limit, len(candidateCoins))

	// 4. 璁＄畻鎬荤泩浜?
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 鍒嗘瀽鍘嗗彶琛ㄧ幇锛堟渶杩?00涓懆鏈燂紝閬垮厤闀挎湡鎸佷粨鐨勪氦鏄撹褰曚涪澶憋級
	// 鍋囪姣?鍒嗛挓涓€涓懆鏈燂紝100涓懆鏈?= 5灏忔椂锛岃冻澶熻鐩栧ぇ閮ㄥ垎浜ゆ槗
	performance, err := at.decisionLogger.AnalyzePerformance(100)
	if err != nil {
		log.Printf("鈿狅笍  鍒嗘瀽鍘嗗彶琛ㄧ幇澶辫触: %v", err)
		// 涓嶅奖鍝嶄富娴佺▼锛岀户缁墽琛岋紙浣嗚缃畃erformance涓簄il浠ラ伩鍏嶄紶閫掗敊璇暟鎹級
		performance = nil
	}

	// 6. 鏋勫缓涓婁笅鏂?
	ctx := &decision.Context{
		CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  at.config.BTCETHLeverage,  // 浣跨敤閰嶇疆鐨勬潬鏉嗗€嶆暟
		AltcoinLeverage: at.config.AltcoinLeverage, // 浣跨敤閰嶇疆鐨勬潬鏉嗗€嶆暟
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
		Performance:    performance, // 娣诲姞鍘嗗彶琛ㄧ幇鍒嗘瀽
	}

	return ctx, nil
}

// executeDecisionWithRecord 鎵цAI鍐崇瓥骞惰褰曡缁嗕俊鎭?
func (at *AutoTrader) executeDecisionWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "hold", "wait":
		// 鏃犻渶鎵ц锛屼粎璁板綍
		return nil
	case "okx":
log.Printf(" [%s] 使用OKX交易", config.Name)
trader, err = NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase)
if err != nil {
return nil, fmt.Errorf("初始化OKX交易器失败: %w", err)
}
default:
		return fmt.Errorf("鏈煡鐨刟ction: %s", decision.Action)
	}
}

// executeOpenLongWithRecord 鎵ц寮€澶氫粨骞惰褰曡缁嗕俊鎭?
func (at *AutoTrader) executeOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  馃搱 寮€澶氫粨: %s", decision.Symbol)

	// 鈿狅笍 鍏抽敭锛氭鏌ユ槸鍚﹀凡鏈夊悓甯佺鍚屾柟鍚戞寔浠擄紝濡傛灉鏈夊垯鎷掔粷寮€浠擄紙闃叉浠撲綅鍙犲姞瓒呴檺锛?
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("鉂?%s 宸叉湁澶氫粨锛屾嫆缁濆紑浠撲互闃叉浠撲綅鍙犲姞瓒呴檺銆傚闇€鎹粨锛岃鍏堢粰鍑?close_long 鍐崇瓥", decision.Symbol)
			}
		}
	}

	// 鑾峰彇褰撳墠浠锋牸
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 璁＄畻鏁伴噺
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 寮€浠?
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 璁板綍璁㈠崟ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  鉁?寮€浠撴垚鍔燂紝璁㈠崟ID: %v, 鏁伴噺: %.4f", order["orderId"], quantity)

	// 璁板綍寮€浠撴椂闂?
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 璁剧疆姝㈡崯姝㈢泩
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		log.Printf("  鈿?璁剧疆姝㈡崯澶辫触: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
		log.Printf("  鈿?璁剧疆姝㈢泩澶辫触: %v", err)
	}

	return nil
}

// executeOpenShortWithRecord 鎵ц寮€绌轰粨骞惰褰曡缁嗕俊鎭?
func (at *AutoTrader) executeOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  馃搲 寮€绌轰粨: %s", decision.Symbol)

	// 鈿狅笍 鍏抽敭锛氭鏌ユ槸鍚﹀凡鏈夊悓甯佺鍚屾柟鍚戞寔浠擄紝濡傛灉鏈夊垯鎷掔粷寮€浠擄紙闃叉浠撲綅鍙犲姞瓒呴檺锛?
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("鉂?%s 宸叉湁绌轰粨锛屾嫆缁濆紑浠撲互闃叉浠撲綅鍙犲姞瓒呴檺銆傚闇€鎹粨锛岃鍏堢粰鍑?close_short 鍐崇瓥", decision.Symbol)
			}
		}
	}

	// 鑾峰彇褰撳墠浠锋牸
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 璁＄畻鏁伴噺
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 寮€浠?
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 璁板綍璁㈠崟ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  鉁?寮€浠撴垚鍔燂紝璁㈠崟ID: %v, 鏁伴噺: %.4f", order["orderId"], quantity)

	// 璁板綍寮€浠撴椂闂?
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 璁剧疆姝㈡崯姝㈢泩
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		log.Printf("  鈿?璁剧疆姝㈡崯澶辫触: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		log.Printf("  鈿?璁剧疆姝㈢泩澶辫触: %v", err)
	}

	return nil
}

// executeCloseLongWithRecord 鎵ц骞冲浠撳苟璁板綍璇︾粏淇℃伅
func (at *AutoTrader) executeCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  馃攧 骞冲浠? %s", decision.Symbol)

	// 鑾峰彇褰撳墠浠锋牸
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 骞充粨
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = 鍏ㄩ儴骞充粨
	if err != nil {
		return err
	}

	// 璁板綍璁㈠崟ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  鉁?骞充粨鎴愬姛")
	return nil
}

// executeCloseShortWithRecord 鎵ц骞崇┖浠撳苟璁板綍璇︾粏淇℃伅
func (at *AutoTrader) executeCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  馃攧 骞崇┖浠? %s", decision.Symbol)

	// 鑾峰彇褰撳墠浠锋牸
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 骞充粨
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = 鍏ㄩ儴骞充粨
	if err != nil {
		return err
	}

	// 璁板綍璁㈠崟ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  鉁?骞充粨鎴愬姛")
	return nil
}

// GetID 鑾峰彇trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 鑾峰彇trader鍚嶇О
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 鑾峰彇AI妯″瀷
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetDecisionLogger 鑾峰彇鍐崇瓥鏃ュ織璁板綍鍣?
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetStatus 鑾峰彇绯荤粺鐘舵€侊紙鐢ㄤ簬API锛?
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	return map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      at.isRunning,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}
}

// GetAccountInfo 鑾峰彇璐︽埛淇℃伅锛堢敤浜嶢PI锛?
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇浣欓澶辫触: %w", err)
	}

	// 鑾峰彇璐︽埛瀛楁
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 閽卞寘浣欓 + 鏈疄鐜扮泩浜?
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 鑾峰彇鎸佷粨璁＄畻鎬讳繚璇侀噾
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇鎸佷粨澶辫触: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnL := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnL += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return map[string]interface{}{
		// 鏍稿績瀛楁
		"total_equity":      totalEquity,           // 璐︽埛鍑€鍊?= wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // 閽卞寘浣欓锛堜笉鍚湭瀹炵幇鐩堜簭锛?
		"unrealized_profit": totalUnrealizedProfit, // 鏈疄鐜扮泩浜忥紙浠嶢PI锛?
		"available_balance": availableBalance,      // 鍙敤浣欓

		// 鐩堜簭缁熻
		"total_pnl":            totalPnL,           // 鎬荤泩浜?= equity - initial
		"total_pnl_pct":        totalPnLPct,        // 鎬荤泩浜忕櫨鍒嗘瘮
		"total_unrealized_pnl": totalUnrealizedPnL, // 鏈疄鐜扮泩浜忥紙浠庢寔浠撹绠楋級
		"initial_balance":      at.initialBalance,  // 鍒濆浣欓
		"daily_pnl":            at.dailyPnL,        // 鏃ョ泩浜?

		// 鎸佷粨淇℃伅
		"position_count":  len(positions),  // 鎸佷粨鏁伴噺
		"margin_used":     totalMarginUsed, // 淇濊瘉閲戝崰鐢?
		"margin_used_pct": marginUsedPct,   // 淇濊瘉閲戜娇鐢ㄧ巼
	}, nil
}

// GetPositions 鑾峰彇鎸佷粨鍒楄〃锛堢敤浜嶢PI锛?
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("鑾峰彇鎸佷粨澶辫触: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		marginUsed := (quantity * markPrice) / float64(leverage)

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// sortDecisionsByPriority 瀵瑰喅绛栨帓搴忥細鍏堝钩浠擄紝鍐嶅紑浠擄紝鏈€鍚巋old/wait
// 杩欐牱鍙互閬垮厤鎹粨鏃朵粨浣嶅彔鍔犺秴闄?
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// 瀹氫箟浼樺厛绾?
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // 鏈€楂樹紭鍏堢骇锛氬厛骞充粨
		case "open_long", "open_short":
			return 2 // 娆′紭鍏堢骇锛氬悗寮€浠?
		case "hold", "wait":
			return 3 // 鏈€浣庝紭鍏堢骇锛氳鏈?
		case "okx":
log.Printf(" [%s] 使用OKX交易", config.Name)
trader, err = NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase)
if err != nil {
return nil, fmt.Errorf("初始化OKX交易器失败: %w", err)
}
default:
			return 999 // 鏈煡鍔ㄤ綔鏀炬渶鍚?
		}
	}

	// 澶嶅埗鍐崇瓥鍒楄〃
	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	// 鎸変紭鍏堢骇鎺掑簭
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

