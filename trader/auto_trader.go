package trader

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"nofx/prompt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// summarizeDecisionError 将较长的错误信息压缩为简洁摘要，用于展示到前端卡片
// 规则：
// - 去除思维链附加段（"=== AI思维链分析 ==="/"=== AI Chain of Thought ==="）
// - 标准化常见错误标签并拼接简短原因
// - 仅保留首行，移除末尾的方括号细节块
// - 限制最大长度（160字符）
func summarizeDecisionError(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return t
	}

	// 去除思维链附加内容
	if i := strings.Index(t, "=== AI思维链分析 ==="); i != -1 {
		t = strings.TrimSpace(t[:i])
	}
	if i := strings.Index(t, "=== AI Chain of Thought ==="); i != -1 {
		t = strings.TrimSpace(t[:i])
	}

	// 统一标签
	lower := strings.ToLower(t)
	label := ""
	switch {
	case strings.Contains(lower, "failed to parse ai response"):
		label = "AI决策解析失败"
	case strings.Contains(t, "提取决策失败"):
		label = "AI决策提取失败"
	case strings.Contains(t, "JSON解析失败") || (strings.Contains(lower, "json") && strings.Contains(lower, "parse")):
		label = "AI决策JSON解析失败"
	case strings.Contains(t, "决策验证失败"):
		label = "AI决策校验未通过"
	case strings.Contains(t, "无法找到JSON数组起始") || strings.Contains(t, "无法找到JSON数组结束"):
		label = "AI未输出有效JSON决策数组"
	case strings.Contains(lower, "failed to call ai api"):
		label = "AI接口调用失败"
	case strings.Contains(lower, "failed to fetch market data"):
		label = "市场数据获取失败"
	}

	// 仅保留首行并提取冒号后的原因
	firstLine := t
	if idx := strings.Index(firstLine, "\n"); idx != -1 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	compact := firstLine
	if idx := strings.Index(compact, ":"); idx != -1 {
		compact = strings.TrimSpace(compact[idx+1:])
	}
	// 去除末尾方括号细节
	if strings.HasSuffix(compact, "]") {
		if lidx := strings.LastIndex(compact, "["); lidx != -1 {
			compact = strings.TrimSpace(compact[:lidx])
		}
	}

	out := firstLine
	if label != "" {
		if compact != "" {
			out = label + ": " + compact
		} else {
			out = label
		}
	}

	// 限制最大长度（按rune计数避免截断半个字符）
	const maxLen = 160
	if utf8.RuneCountInString(out) > maxLen {
		// 截断为 maxLen-1 并添加省略号
		runes := []rune(out)
		out = string(runes[:maxLen-1]) + "…"
	}
	return out
}

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID      string // Trader唯一标识（用于日志目录等）
	Name    string // Trader显示名称
	AIModel string // AI模型: "qwen" 或 "deepseek"

	// 交易平台选择
	Exchange string // "binance", "hyperliquid", "aster" 或 "okx"

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string

	// Hyperliquid配置
	HyperliquidPrivateKey string
	HyperliquidTestnet    bool
	HyperliquidWalletAddr string

	// Aster配置
	AsterUser       string // Aster主钱包地址
	AsterSigner     string // Aster API钱包地址
	AsterPrivateKey string // Aster API钱包私钥

	// OKX配置
	OKXAPIKey     string
	OKXSecretKey  string
	OKXPassphrase string

	CoinPoolAPIURL string

	// AI配置
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 自定义AI API配置
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// 扫描配置
	ScanInterval time.Duration // 扫描间隔（建议3分钟）

	// 账户配置
	InitialBalance              float64 // 初始金额（用于计算盈亏，需手动设置）
	ExtraInvestment             float64 // 额外投入金额（追加入金），用于计算真实投入基线
	AutoCalibrateInitialBalance bool    // 是否自动对齐基线（含入金校准）
	CalibrationThreshold        float64 // 触发自动校准的最小差额（USDT）
	PersistInitialBalance       bool    // 是否持久化初始余额到本地文件
	InitialBalanceStateDir      string  // 初始余额状态文件目录

	// 杠杆配置
	BTCETHLeverage  int // BTC和ETH的杠杆倍数
	AltcoinLeverage int // 山寨币的杠杆倍数

	// 风险控制（仅作为提示，AI可自主决定）
	MaxDailyLoss    float64       // 最大日亏损百分比（提示）
	MaxDrawdown     float64       // 最大回撤百分比（提示）
	StopTradingTime time.Duration // 触发风控后暂停时长
	DryRun          bool          // 是否 DryRun（演示模式，跳过真实下单）
	// 决策校验阈值
	MinRiskRewardRatio float64 // 最小风险回报比（硬性校验）
	// 保证金使用率上限（百分比）
	MaxMarginUsagePct float64
	CycleWeights      struct {
		W4h  float64
		W1h  float64
		W15m float64
		W3m  float64
	}
	InvertDecisionSide bool
}

// AutoTrader 自动交易器
type AutoTrader struct {
	id             string      // Trader唯一标识
	name           string      // Trader显示名称
	aiModel        string      // AI模型名称
	exchange       string      // 交易平台名称
	aiClient       *mcp.Client // 每个trader独立的AI客户端，避免全局冲突
	config         AutoTraderConfig
	trader         Trader                 // 使用Trader接口（支持多平台）
	decisionLogger *logger.DecisionLogger // 决策日志记录器
	initialBalance float64
	dailyPnL       float64
	// 每日盈亏基线（当天开头的净值，用于计算日盈亏）
	dailyBaseline         float64
	dailyBaselineDate     string // 格式: YYYY-MM-DD
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	startTime             time.Time        // 系统启动时间
	callCount             int              // AI调用次数
	positionFirstSeenTime map[string]int64 // 持仓首次出现时间 (symbol_side -> timestamp毫秒)
	executionEnabled      bool             // 是否启用自动执行

	// 基线自动对齐与持久化
	autoCalibrateBaseline bool
	calibrationThreshold  float64
	baselineStatePath     string
	// 投资调整（动态入金/出金）
	investmentAdjustments []InvestmentAdjustment
	investmentStatePath   string
	lastInvestmentSync    time.Time
	// 扫描间隔配置的生效时间（用于前端展示“scan_interval_minutes 生效时间”）
	scanIntervalAppliedAt time.Time
	// 轮询降级触发的默认阈值（百分比），若未设置算法单则启用保护
	fallbackStopLossPct   float64 // 默认 -5% (long: 跌5%止损；short: 涨5%止损)
	fallbackTakeProfitPct float64 // 默认 +10% (long: 涨10%止盈；short: 跌10%止盈)

	// 软止损/止盈阈值（仅在AI周期内评估触发），按持仓方向记录绝对价格
	softStopPrices map[string]float64 // key: symbol_side -> stop price
    softTakePrices map[string]float64 // key: symbol_side -> take price

    // 周期控制
    lastCycleStart  time.Time
    nextCycleDue    time.Time
    cycleTolerance  time.Duration
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig) (*AutoTrader, error) {
	// 设置默认值
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

	// 初始化AI（按trader隔离客户端，避免共享全局defaultClient导致相互覆盖）
	var aiClient = mcp.New()
	if config.AIModel == "custom" {
		aiClient.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		aiClient.SetQwenAPIKey(config.QwenKey, "")
		log.Printf("🤖 [%s] 使用阿里云Qwen AI", config.Name)
	} else {
		aiClient.SetDeepSeekAPIKey(config.DeepSeekKey)
		log.Printf("🤖 [%s] 使用DeepSeek AI", config.Name)
	}

	// 初始化币种池API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 设置默认交易平台
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 根据配置创建对应的交易器
	var trader Trader
	var err error

	switch config.Exchange {
	case "binance":
		log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey)
	case "hyperliquid":
		log.Printf("🏦 [%s] 使用Hyperliquid交易", config.Name)
		trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("初始化Hyperliquid交易器失败: %w", err)
		}
	case "aster":
		log.Printf("🏦 [%s] 使用Aster交易", config.Name)
		trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("初始化Aster交易器失败: %w", err)
		}
	case "okx":
		log.Printf("🏦 [%s] 使用OKX永续合约交易", config.Name)
		trader, err = NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase)
		if err != nil {
			return nil, fmt.Errorf("初始化OKX交易器失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的交易平台: %s", config.Exchange)
	}

	// 验证初始金额配置
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("初始金额必须大于0，请在配置中设置InitialBalance")
	}

	// 初始化决策日志记录器（使用trader ID创建独立目录，统一到项目根 decision_logs）
	logDir := filepath.Join("decision_logs", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	at := &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		aiClient:              aiClient,
		config:                config,
		trader:                trader,
		decisionLogger:        decisionLogger,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		executionEnabled:      true,
		autoCalibrateBaseline: config.AutoCalibrateInitialBalance,
		calibrationThreshold:  config.CalibrationThreshold,
		scanIntervalAppliedAt: time.Now(),
		fallbackStopLossPct:   -20.0,
		fallbackTakeProfitPct: 10.0,
		softStopPrices:        make(map[string]float64),
		softTakePrices:        make(map[string]float64),
	}

	// 初始余额持久化加载（可选）
	if config.PersistInitialBalance && config.InitialBalanceStateDir != "" {
		safeID := strings.ReplaceAll(config.ID, " ", "_")
		fileName := fmt.Sprintf("initial_balance_%s.json", safeID)
		at.baselineStatePath = filepath.Join(config.InitialBalanceStateDir, fileName)
		// 当启用自动校准时，优先使用配置中的初始资金，使后续差额以“投资调整”记录，而非直接覆盖初始值
		if !config.AutoCalibrateInitialBalance {
			if v, err := at.loadInitialBalanceFromFile(); err == nil && v > 0 {
				at.initialBalance = v
				log.Printf("🧷 [%s] 读取持久化初始余额: %.2f", config.Name, v)
			}
		} else {
			log.Printf("🧷 [%s] 忽略持久化初始余额，使用配置初始资金以便按账户变化记录投资调整", config.Name)
		}
		// 尝试加载当日基线（若存在）
		if db, dd, err := at.loadDailyBaselineFromFile(); err == nil && db > 0 && dd != "" {
			at.dailyBaseline = db
			at.dailyBaselineDate = dd
			log.Printf("🧷 [%s] 读取当日基线: date=%s baseline=%.2f", config.Name, dd, db)
		}
		// 初始化投资调整状态文件路径并加载
		invFile := fmt.Sprintf("investments_%s.json", safeID)
		at.investmentStatePath = filepath.Join(config.InitialBalanceStateDir, invFile)
		if list, err := at.loadInvestmentAdjustmentsFromFile(); err == nil {
			at.investmentAdjustments = list
			if len(list) > 0 {
				log.Printf("🧷 [%s] 读取投资调整记录 %d 条", config.Name, len(list))
			}
		}
	}

    // 周期容差（用于对齐边界与时间门控容错）
    at.cycleTolerance = 3 * time.Second
    return at, nil
}

// Run 运行自动交易主循环
func (at *AutoTrader) Run() error {
	at.isRunning = true
	log.Println("🚀 AI驱动自动交易系统启动")
	log.Printf("💰 初始余额: %.2f USDT | 额外投入: %.2f USDT | 总投入: %.2f USDT", at.initialBalance, at.config.ExtraInvestment, at.initialBalance+at.config.ExtraInvestment)
	log.Printf("⚙️  扫描间隔: %v", at.config.ScanInterval)
	log.Printf("🛡️  最小风险回报比: %.2f", at.config.MinRiskRewardRatio)
	log.Printf("🔧 杠杆上限: BTC/ETH=%dx | 山寨=%dx", at.config.BTCETHLeverage, at.config.AltcoinLeverage)
	log.Printf("🧯 保证金使用率上限: %.0f%%", at.config.MaxMarginUsagePct)
	// 显示当前启动使用的提示词模板（来自环境变量 NOFX_PROMPT_VARIANT，未设置则回退为默认）
	func() {
		variant := os.Getenv("NOFX_PROMPT_VARIANT")
		if strings.TrimSpace(variant) == "" {
			variant = prompt.DefaultVariant
		}
		// 提示系统模板文件名，便于快速确认实际使用的模板
		log.Printf("🧩 当前提示词模板: %s (prompt/system_%s.txt)", variant, variant)
	}()
	log.Println("🤖 AI将全权决定杠杆、仓位大小、止损止盈等参数")

	interval := at.config.ScanInterval
	now := time.Now()
	intervalSec := int64(interval / time.Second)
	remainder := now.Unix() % intervalSec
	delay := time.Duration(0)
	if intervalSec > 0 && remainder != 0 {
		delay = time.Duration(intervalSec-remainder) * time.Second
	}
    if delay > 0 {
        log.Printf("⌛ 对齐扫描周期，等待 %s 后执行首次决策", delay.String())
        time.Sleep(delay)
    }

    // 记录本次周期起点与下次到期
    at.lastCycleStart = time.Now()
    at.nextCycleDue = at.lastCycleStart.Add(interval)

    err := at.runCycle()
    if err != nil {
        log.Printf("❌ 执行失败: %v", err)
    }

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

    for at.isRunning {
        <-ticker.C
        // 防止提前于对齐边界运行：若尚未到达下一个周期到期点，则跳过本次触发
        now := time.Now()
        if now.Add(at.cycleTolerance).Before(at.nextCycleDue) {
            continue
        }
        at.lastCycleStart = now
        at.nextCycleDue = at.lastCycleStart.Add(interval)

        err = at.runCycle()
        if err != nil {
            log.Printf("❌ 执行失败: %v", err)
        }
    }

	return nil
}

// Stop 停止自动交易
func (at *AutoTrader) Stop() {
	at.isRunning = false
	log.Println("⏹ 自动交易系统停止")
}

// enforceFallbackSLTP 轮询降级触发止损/止盈（简单保护：默认 -5% / +10%）
func (at *AutoTrader) enforceFallbackSLTP(positions []map[string]interface{}) {
	if !at.executionEnabled {
		return
	}
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		entryPrice, _ := pos["entryPrice"].(float64)
		markPrice, _ := pos["markPrice"].(float64)
		qty, _ := pos["positionAmt"].(float64)
		if qty < 0 {
			qty = -qty
		}
		if entryPrice <= 0 || markPrice <= 0 || qty <= 0 {
			continue
		}

		// 计算涨跌百分比（相对入场价）
		changePct := 0.0
		if side == "long" {
			changePct = ((markPrice - entryPrice) / entryPrice) * 100
			// long: 跌到止损或涨到止盈
			if changePct <= at.fallbackStopLossPct {
				log.Printf("  🛡️  Fallback SL 触发: %s long Δ=%.2f%%，平仓保护", symbol, changePct)
				_, _ = at.trader.CloseLong(symbol, 0)
			} else if changePct >= at.fallbackTakeProfitPct {
				log.Printf("  🛡️  Fallback TP 触发: %s long Δ=%.2f%%，平仓止盈", symbol, changePct)
				_, _ = at.trader.CloseLong(symbol, 0)
			}
		} else {
			changePct = ((entryPrice - markPrice) / entryPrice) * 100
			// short: 涨到止损或跌到止盈
			if changePct <= at.fallbackStopLossPct {
				log.Printf("  🛡️  Fallback SL 触发: %s short Δ=%.2f%%，平仓保护", symbol, changePct)
				_, _ = at.trader.CloseShort(symbol, 0)
			} else if changePct >= at.fallbackTakeProfitPct {
				log.Printf("  🛡️  Fallback TP 触发: %s short Δ=%.2f%%，平仓止盈", symbol, changePct)
				_, _ = at.trader.CloseShort(symbol, 0)
			}
		}
	}
}

// investedBaseline 返回用于计算总盈亏的真实投入基线（初始余额 + 额外投入）
func (at *AutoTrader) investedBaseline() float64 {
	base := at.initialBalance
	if at.config.ExtraInvestment > 0 {
		base += at.config.ExtraInvestment
	}
	// 动态投资调整累计
	for _, adj := range at.investmentAdjustments {
		base += adj.Amount
	}
	return base
}

// runCycle 运行一个交易周期（使用AI全权决策）
func (at *AutoTrader) runCycle() error {
	at.callCount++

	log.Print("\n" + strings.Repeat("=", 70))
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Print(strings.Repeat("=", 70))

	// 创建决策记录
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 检查是否需要停止交易
	if time.Now().Before(at.stopUntil) {
		remaining := time.Until(at.stopUntil)
		log.Printf("⏸ 风险控制：暂停交易中，剩余 %.0f 分钟", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = summarizeDecisionError(fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes()))
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 2. 检查日期切换并确保当日基线存在（以 runCycle 时刻的净值作为当天初始值）
	// 实际日盈亏计算在 GetAccountInfo 中完成，这里仅在跨日时清理旧值
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.lastResetTime = time.Now()
		// 将 dailyBaselineDate 置为当天，具体数值在下一次账户读取时初始化
		at.dailyBaselineDate = time.Now().Format("2006-01-02")
		at.dailyBaseline = 0
		_ = at.saveDailyBaselineToFile()
		log.Println("📅 新的一天开始，日基线待初始化")
	}

	// 3. 收集交易上下文
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = summarizeDecisionError(fmt.Sprintf("构建交易上下文失败: %v", err))
		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("构建交易上下文失败: %w", err)
	}

	// 保存账户状态快照
	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: ctx.Account.TotalPnL,
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	// 保存持仓快照
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

	// 保存候选币种列表
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	// 计算回退系统提示词（用于确保日志总是包含 system_prompt 字段）
	variant := os.Getenv("NOFX_PROMPT_VARIANT")
	if strings.TrimSpace(variant) == "" {
		variant = prompt.DefaultVariant
	}
	fallbackSystemPrompt := prompt.RenderSystemPrompt(variant, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, at.config.MinRiskRewardRatio, at.config.MaxMarginUsagePct)

	log.Printf("Account equity: %.2f USDT | Available: %.2f USDT | Positions: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 4. 调用AI获取完整决策
	log.Println("Requesting AI analysis and decisions...")
	decision, err := decision.GetFullDecisionWithClient(at.aiClient, ctx)

	// 先写入回退系统提示词，确保日志包含该字段（若后续有AI返回则覆盖）
	record.SystemPrompt = fallbackSystemPrompt

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if decision != nil {
		record.SystemPrompt = decision.SystemPrompt
		record.InputPrompt = decision.UserPrompt
		record.CoTTrace = decision.CoTTrace
		if len(decision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(decision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		} else {
			// 保证第二步JSON决策数组在无决策时也有可视化输出
			record.DecisionJSON = "[]"
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = summarizeDecisionError(fmt.Sprintf("获取AI决策失败: %v", err))

		// 打印AI思维链（即使有错误）
		if decision != nil && decision.CoTTrace != "" {
			log.Print("\n" + strings.Repeat("-", 70))
			log.Println("💭 AI思维链分析（错误情况）:")
			log.Println(strings.Repeat("-", 70))
			log.Println(decision.CoTTrace)
			log.Print(strings.Repeat("-", 70) + "\n")
		}

		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("获取AI决策失败: %w", err)
	}

	// 5. 打印AI思维链
	log.Print("\n" + strings.Repeat("-", 70))
	log.Println("💭 AI思维链分析:")
	log.Println(strings.Repeat("-", 70))
	log.Println(decision.CoTTrace)
	log.Print(strings.Repeat("-", 70) + "\n")

	// 6. 打印AI决策
	log.Printf("📋 AI决策列表 (%d 个):\n", len(decision.Decisions))
	for i, d := range decision.Decisions {
		log.Printf("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
		if d.Action == "open_long" || d.Action == "open_short" {
			log.Printf("      杠杆: %dx | 仓位: %.2f USDT | 止损: %.4f | 止盈: %.4f",
				d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
		}
	}
	log.Println()

	// 7. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	sortedDecisions := sortDecisionsByPriority(decision.Decisions)

	log.Println("🔄 执行顺序（已优化）: 先平仓→后开仓")
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 执行决策并记录结果（在未启用自动执行时进行模拟记录以便统计）
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

		if at.config.InvertDecisionSide {
			old := d.Action
			switch strings.ToLower(d.Action) {
			case "open_long":
				d.Action = "open_short"
			case "open_short":
				d.Action = "open_long"
			}
			if d.Action != old {
				msg := fmt.Sprintf("↔️ 方向反转：%s %s→%s", d.Symbol, old, d.Action)
				log.Println(msg)
				record.ExecutionLog = append(record.ExecutionLog, msg)
				actionRecord.Action = d.Action
				md, mErr := market.Get(d.Symbol)
				if mErr == nil {
					price := md.CurrentPrice
					if price > 0 && d.StopLoss > 0 && d.TakeProfit > 0 {
						if old == "open_long" && d.Action == "open_short" {
							dSL := price - d.StopLoss
							dTP := d.TakeProfit - price
							if dSL < 0 {
								dSL = -dSL
							}
							if dTP < 0 {
								dTP = -dTP
							}
							d.StopLoss = price + dSL
							d.TakeProfit = price - dTP
						} else if old == "open_short" && d.Action == "open_long" {
							dSL := d.StopLoss - price
							dTP := price - d.TakeProfit
							if dSL < 0 {
								dSL = -dSL
							}
							if dTP < 0 {
								dTP = -dTP
							}
							d.StopLoss = price - dSL
							d.TakeProfit = price + dTP
						}
						adj := fmt.Sprintf("ℹ️ 反转后SL/TP归一化：%s 价%.4f SL%.4f TP%.4f", d.Symbol, price, d.StopLoss, d.TakeProfit)
						log.Println(adj)
						record.ExecutionLog = append(record.ExecutionLog, adj)
					}
				}
			}
		}

		if !at.executionEnabled {
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("Auto-trading disabled: simulate %s %s", d.Symbol, d.Action))
		}

		// 预算与持仓硬约束：对 open_* 执行前进行全局保证金与持仓数量校验
		if d.Action == "open_long" || d.Action == "open_short" {
			// 获取当前账户信息
			acct, err := at.GetAccountInfo()
			if err != nil {
				log.Printf("⚠️ 获取账户信息失败，跳过开仓 %s %s: %v", d.Symbol, d.Action, err)
				actionRecord.Error = fmt.Sprintf("get account info failed: %v", err)
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("%s %s skipped: account info error", d.Symbol, d.Action))
				record.Decisions = append(record.Decisions, actionRecord)
				continue
			}

			// 安全转换辅助
			toFloat := func(v interface{}) float64 {
				switch x := v.(type) {
				case float64:
					return x
				case float32:
					return float64(x)
				case int:
					return float64(x)
				case int64:
					return float64(x)
				case string:
					if f, err := strconv.ParseFloat(x, 64); err == nil {
						return f
					}
					return 0
				default:
					return 0
				}
			}
			toInt := func(v interface{}) int {
				switch x := v.(type) {
				case int:
					return x
				case int64:
					return int(x)
				case float64:
					return int(x)
				case string:
					if i, err := strconv.Atoi(x); err == nil {
						return i
					}
					return 0
				default:
					return 0
				}
			}

			totalEquity := toFloat(acct["total_equity"])   // 钱包 + 未实现盈亏
			marginUsed := toFloat(acct["margin_used"])     // 当前占用保证金
			positionCount := toInt(acct["position_count"]) // 当前持仓数量

			// 最多持仓≤3
			if positionCount >= 3 {
				msg := fmt.Sprintf("❌ 持仓数量已达上限(%d)，拒绝新开仓 %s", positionCount, d.Symbol)
				log.Println(msg)
				actionRecord.Error = "position_count_limit"
				record.ExecutionLog = append(record.ExecutionLog, msg)
				record.Decisions = append(record.Decisions, actionRecord)
				continue
			}

			// 动态调整：在不超过配置的保证金上限前提下，优先提升杠杆至配置值，其次缩小仓位USD
			// 可用保证金空间（按 max_margin_usage_pct 上限）
			if totalEquity <= 0 {
				log.Printf("⚠️ total_equity<=0，无法计算保证金占用，暂不应用全局预算限制：%s %s", d.Symbol, d.Action)
			} else {
				maxBudget := (at.config.MaxMarginUsagePct / 100.0) * totalEquity
				allowedMargin := maxBudget - marginUsed
				if allowedMargin <= 0 {
					msg := fmt.Sprintf("❌ 全局保证金上限已用尽(%.2f%%≥%.0f%%)，拒绝开仓 %s", (marginUsed/totalEquity)*100, at.config.MaxMarginUsagePct, d.Symbol)
					log.Println(msg)
					actionRecord.Error = "margin_budget_exhausted"
					record.ExecutionLog = append(record.ExecutionLog, msg)
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				}

				// 计算配置期望杠杆（BTC/ETH 使用 BTCETHLeverage，其它币使用 AltcoinLeverage）
				symU := strings.ToUpper(d.Symbol)
				cfgLev := at.config.AltcoinLeverage
				if strings.HasPrefix(symU, "BTC") || strings.HasPrefix(symU, "ETH") {
					cfgLev = at.config.BTCETHLeverage
				}
				// 目标杠杆：限定在 [1, cfgLev]
				targetLev := d.Leverage
				if targetLev <= 0 {
					targetLev = 1
				}
				if targetLev > cfgLev {
					targetLev = cfgLev
				}

				// 计算在保持原USD下满足预算所需的杠杆，并限制不超过配置上限
				requiredLev := int(math.Ceil(d.PositionSizeUSD / allowedMargin))
				if requiredLev < 1 {
					requiredLev = 1
				}
				if requiredLev < targetLev {
					requiredLev = targetLev
				}
				proposedLev := requiredLev
				if proposedLev > cfgLev {
					proposedLev = cfgLev
				}

				// 在提议杠杆下的允许USD
				allowedUSD := allowedMargin * float64(proposedLev)

				if d.PositionSizeUSD > allowedUSD {
					// 原计划USD超出预算，在提升杠杆至所需后仍不足，则缩仓到允许USD
					oldUSD := d.PositionSizeUSD
					oldLev := d.Leverage
					d.Leverage = proposedLev
					d.PositionSizeUSD = allowedUSD
					msg := fmt.Sprintf("⚠️ 预算门控：%s 杠杆 %dx→%dx；仓位 %.2fUSD→%.2fUSD，满足%.0f%%预算", d.Symbol, oldLev, d.Leverage, oldUSD, d.PositionSizeUSD, at.config.MaxMarginUsagePct)
					log.Println(msg)
					record.ExecutionLog = append(record.ExecutionLog, msg)
				} else if d.Leverage < proposedLev {
					// 原计划USD在预算内，但杠杆低于所需/配置，则提升杠杆以更安全地占用预算
					oldLev := d.Leverage
					d.Leverage = proposedLev
					msg := fmt.Sprintf("ℹ️ 调整杠杆以满足预算/配置：%s 杠杆 %dx→%dx；保留原USD %.2f", d.Symbol, oldLev, d.Leverage, d.PositionSizeUSD)
					log.Println(msg)
					record.ExecutionLog = append(record.ExecutionLog, msg)
				}
			}
			md, mErr := market.Get(d.Symbol)
			if mErr == nil {
				price := md.CurrentPrice
				if price <= 0 {
					msg := fmt.Sprintf("❌ 风险收益比计算失败：%s 价格无效，跳过开仓", d.Symbol)
					log.Println(msg)
					actionRecord.Error = "rr_price_invalid"
					record.ExecutionLog = append(record.ExecutionLog, msg)
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				}
				if d.PositionSizeUSD <= 0 || d.StopLoss <= 0 || d.TakeProfit <= 0 {
					msg := fmt.Sprintf("❌ 风险收益参数缺失：%s 需提供止损、止盈与仓位USD，跳过开仓", d.Symbol)
					log.Println(msg)
					actionRecord.Error = "rr_params_missing"
					record.ExecutionLog = append(record.ExecutionLog, msg)
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				}
				qty := d.PositionSizeUSD / price
				var risk, reward float64
				if d.Action == "open_long" {
					risk = price - d.StopLoss
					reward = d.TakeProfit - price
				} else {
					risk = d.StopLoss - price
					reward = price - d.TakeProfit
				}
				if risk <= 0 || reward <= 0 {
					msg := fmt.Sprintf("❌ 风险收益结构不合法：%s risk=%.4f reward=%.4f，跳过开仓", d.Symbol, risk, reward)
					log.Println(msg)
					actionRecord.Error = "rr_invalid_structure"
					record.ExecutionLog = append(record.ExecutionLog, msg)
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				}
				rr := reward / risk
				rrMsg := fmt.Sprintf("ℹ️ 风险收益比: %.2f (最小要求 %.2f)", rr, at.config.MinRiskRewardRatio)
				log.Println(rrMsg)
				record.ExecutionLog = append(record.ExecutionLog, rrMsg)
				if rr < at.config.MinRiskRewardRatio {
					msg := fmt.Sprintf("❌ 风险收益比未达标：%s %.2f < %.2f，跳过开仓", d.Symbol, rr, at.config.MinRiskRewardRatio)
					log.Println(msg)
					actionRecord.Error = "rr_below_min"
					record.ExecutionLog = append(record.ExecutionLog, msg)
					record.Decisions = append(record.Decisions, actionRecord)
					continue
				}
				usdMsg := fmt.Sprintf("ℹ️ 风险收益: 风险%.2fUSD 收益%.2fUSD", risk*qty, reward*qty)
				log.Println(usdMsg)
				record.ExecutionLog = append(record.ExecutionLog, usdMsg)
			}
		}

		if d.Action == "close_long" || d.Action == "close_short" {
			side := "long"
			if d.Action == "close_short" {
				side = "short"
			}
			posKey := d.Symbol + "_" + side
            if ts, ok := at.positionFirstSeenTime[posKey]; ok && ts > 0 {
                held := time.Since(time.UnixMilli(ts))
                if held+at.cycleTolerance < at.config.ScanInterval {
                    msg := fmt.Sprintf("⏸ 时间门控：%s %s 持仓仅%.0f分钟，跳过本次平仓以遵循%.0f分钟扫描节奏", d.Symbol, side, held.Minutes(), at.config.ScanInterval.Minutes())
                    log.Println(msg)
                    actionRecord.Error = "time_gate"
                    record.ExecutionLog = append(record.ExecutionLog, msg)
                    record.Decisions = append(record.Decisions, actionRecord)
                    continue
                }
            }
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			log.Printf("Decision execution failed (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("%s %s failed: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			if !at.executionEnabled {
				// 标记为模拟成功，避免被统计层过滤
				actionRecord.Error = "execution_disabled_simulated"
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("%s %s succeeded (simulated)", d.Symbol, d.Action))
			} else {
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("%s %s succeeded", d.Symbol, d.Action))
			}
			// 成功（含模拟）后短暂延迟
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

    // 8. 保存决策记录
    if err := at.decisionLogger.LogDecision(record); err != nil {
        log.Printf("Failed to save decision record: %v", err)
    }

    // 9. 亏损保护（硬性要求）：触发后在规定时间段内停止所有AI分析
    // 规则：
    // - 连续亏损3笔 → 暂停2小时
    // - 单笔亏损≥10%（相对保证金）→ 暂停1小时
    // - 当日总亏损≥20%（相对当日基线）→ 暂停2小时
    // - 连续亏损3笔 且 当日亏损≥20% → 暂停6小时
    func() {
        // 读取账户当日盈亏信息
        accountInfo, _ := at.GetAccountInfo()
        dailyPnL := 0.0
        dailyBaseline := at.dailyBaseline
        if v, ok := accountInfo["daily_pnl"].(float64); ok {
            dailyPnL = v
        }
        dailyLossPct := 0.0
        if dailyBaseline > 0 && dailyPnL < 0 {
            dailyLossPct = (-(dailyPnL) / dailyBaseline) * 100
        }

        // 分析最近交易结果
        var lastTradeLossPct float64
        consecutiveLosses := 0
        if at.decisionLogger != nil {
            if perf, err := at.decisionLogger.AnalyzePerformance(50); err == nil {
                // 最近交易最新在前
                for i := 0; i < len(perf.RecentTrades); i++ {
                    t := perf.RecentTrades[i]
                    if t.PnL < 0 {
                        consecutiveLosses++
                    } else if t.PnL > 0 {
                        break
                    }
                }
                if len(perf.RecentTrades) > 0 {
                    lastTradeLossPct = perf.RecentTrades[0].PnLPct
                    if lastTradeLossPct > 0 {
                        // 仅在为亏损时才生效
                        lastTradeLossPct = 0
                    } else {
                        lastTradeLossPct = -lastTradeLossPct
                    }
                }
            }
        }

        // 计算暂停时长
        pause := time.Duration(0)
        reason := ""
        condA := consecutiveLosses >= 3
        condB := dailyLossPct >= 20.0
        condC := lastTradeLossPct >= 10.0

        if condA && condB {
            pause = 6 * time.Hour
            reason = "连续亏损3笔且当日亏损≥20%，暂停6小时"
        } else if condA {
            pause = 2 * time.Hour
            reason = "连续亏损3笔，暂停2小时"
        } else if condB {
            pause = 2 * time.Hour
            reason = "当日总亏损≥20%，暂停2小时"
        } else if condC {
            pause = 1 * time.Hour
            reason = "单笔亏损≥10%，暂停1小时"
        }

        if pause > 0 {
            at.stopUntil = time.Now().Add(pause)
            msg := fmt.Sprintf("⏸ 亏损保护触发：%s（至 %s）", reason, at.stopUntil.Format("15:04"))
            log.Println(msg)
            record.ExecutionLog = append(record.ExecutionLog, msg)
        }
    }()

    return nil
}

// buildTradingContext 构建交易上下文
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 获取账户信息
	balance, err := at.trader.GetBalance()
	if err != nil {
		// 容错：在 DryRun 或密钥缺失时允许继续，使用占位数据
		log.Printf("⚠️  获取账户余额失败，使用占位数据继续: %v", err)
		balance = map[string]interface{}{
			"totalWalletBalance":    0.0,
			"totalUnrealizedProfit": 0.0,
			"availableBalance":      0.0,
		}
	}

	// 获取账户字段
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

	// DryRun保护：若余额不可用且配置了初始余额，则用初始余额作为钱包/可用
	if totalWalletBalance == 0 && totalUnrealizedProfit == 0 && at.initialBalance > 0 {
		totalWalletBalance = at.initialBalance
		availableBalance = at.initialBalance
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 2. 获取持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		// 容错：在 DryRun 或密钥缺失时允许继续，使用空持仓
		log.Printf("⚠️  获取持仓失败，使用空持仓继续: %v", err)
		positions = []map[string]interface{}{}
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 当前持仓的key集合（用于清理已平仓的记录）
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 空仓数量为负，转为正数
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// 计算占用保证金（估算）
		leverage := 10 // 默认值，实际应该从持仓信息获取
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// 计算盈亏百分比（相对保证金/加杠杆前的仓位价值）
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}

		// 跟踪持仓首次出现时间
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 新持仓，记录当前时间
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		// 从决策日志恢复开仓时间（跨重启保留）
		if at.decisionLogger != nil {
			if records, err := at.decisionLogger.GetLatestRecords(500); err == nil {
				// 从旧到新遍历，找到最近一次该symbol/side的open_* 时间
				for i := len(records) - 1; i >= 0; i-- {
					rec := records[i]
					found := false
					for _, act := range rec.Decisions {
						if (act.Action == "open_long" && side == "long") || (act.Action == "open_short" && side == "short") {
							if strings.EqualFold(act.Symbol, symbol) {
								ut := act.Timestamp.UnixMilli()
								if ut > 0 {
									updateTime = ut
								}
								found = true
								break
							}
						}
					}
					if found {
						break
					}
				}
			}
		}

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

	// 降级轮询触发止损/止盈：若算法单未能设置或被撤销，轮询检测价格触发后直接平仓
	// 在执行周期内评估：软止损/止盈（使用决策设置的绝对价格）
	for _, p := range positions {
		symbol, _ := p["symbol"].(string)
		side, _ := p["side"].(string)
		markPrice, _ := p["markPrice"].(float64)
		key := symbol + "_" + side
		stop := at.softStopPrices[key]
		take := at.softTakePrices[key]
		if stop > 0 && take > 0 && markPrice > 0 {
			if side == "long" {
				if markPrice <= stop {
					log.Printf("  🧠 软止损触发：%s long mark=%.4f ≤ stop=%.4f（在本周期执行平仓）", symbol, markPrice, stop)
					_, _ = at.trader.CloseLong(symbol, 0)
					continue
				}
				if markPrice >= take {
					log.Printf("  🧠 软止盈触发：%s long mark=%.4f ≥ take=%.4f（在本周期执行平仓）", symbol, markPrice, take)
					_, _ = at.trader.CloseLong(symbol, 0)
					continue
				}
			} else if side == "short" {
				if markPrice >= stop {
					log.Printf("  🧠 软止损触发：%s short mark=%.4f ≥ stop=%.4f（在本周期执行平仓）", symbol, markPrice, stop)
					_, _ = at.trader.CloseShort(symbol, 0)
					continue
				}
				if markPrice <= take {
					log.Printf("  🧠 软止盈触发：%s short mark=%.4f ≤ take=%.4f（在本周期执行平仓）", symbol, markPrice, take)
					_, _ = at.trader.CloseShort(symbol, 0)
					continue
				}
			}
		}
	}

	at.enforceFallbackSLTP(positions)

	// 清理已平仓的持仓记录
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 3. 获取合并的候选币种池（AI500 + OI Top，去重）
	// 无论有没有持仓，都分析相同数量的币种（让AI看到所有好机会）
	// AI会根据保证金使用率和现有持仓情况，自己决定是否要换仓
	const ai500Limit = 20 // AI500取前20个评分最高的币种

	// 获取合并后的币种池（AI500 + OI Top）
	mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
	if err != nil {
		return nil, fmt.Errorf("获取合并币种池失败: %w", err)
	}

	// 构建候选币种列表（包含来源信息）
	// 过滤无效符号，避免空、仅USDT或平台名等进入分析流程
	isValidCandidate := func(sym string) bool {
		s := strings.ToUpper(strings.TrimSpace(sym))
		if s == "" || s == "USDT" {
			return false
		}
		if !strings.HasSuffix(s, "USDT") {
			return false
		}
		if s == "OKXUSDT" {
			return false
		}
		return true
	}
	var candidateCoins []decision.CandidateCoin
	for _, symbol := range mergedPool.AllSymbols {
		if !isValidCandidate(symbol) {
			continue
		}
		sources := mergedPool.SymbolSources[symbol]
		candidateCoins = append(candidateCoins, decision.CandidateCoin{
			Symbol:  symbol,
			Sources: sources, // "ai500" 和/或 "oi_top"
		})
	}

	log.Printf("📋 合并币种池: AI500前%d + OI_Top20 = 总计%d个候选币种",
		ai500Limit, len(candidateCoins))

	// 4. 计算总盈亏（使用真实投入基线：初始余额 + 额外投入）
	invested := at.investedBaseline()
	totalPnL := totalEquity - invested
	totalPnLPct := 0.0
	if invested > 0 {
		totalPnLPct = (totalPnL / invested) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 分析历史表现（最近20个周期）
	performance, err := at.decisionLogger.AnalyzePerformance(20)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		// 不影响主流程，继续执行（但设置performance为nil以避免传递错误数据）
		performance = nil
	}

	// 6. 构建上下文
	ctx := &decision.Context{
		CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  at.config.BTCETHLeverage,  // 使用配置的杠杆倍数
		AltcoinLeverage: at.config.AltcoinLeverage, // 使用配置的杠杆倍数
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:          positionInfos,
		CandidateCoins:     candidateCoins,
		Performance:        performance, // 添加历史表现分析
		MinRiskRewardRatio: at.config.MinRiskRewardRatio,
		MaxMarginUsagePct:  at.config.MaxMarginUsagePct,
		CycleWeight4h:      at.config.CycleWeights.W4h,
		CycleWeight1h:      at.config.CycleWeights.W1h,
		CycleWeight15m:     at.config.CycleWeights.W15m,
		CycleWeight3m:      at.config.CycleWeights.W3m,
	}

	return ctx, nil
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
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
		// 无需执行，仅记录
		return nil
	default:
		return fmt.Errorf("未知的action: %s", decision.Action)
	}
}

// executeOpenLongWithRecord 执行开多仓并记录详细信息
func (at *AutoTrader) executeOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 获取当前价格并计算数量（即使在 DryRun/未执行时也补齐记录字段）
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 统一为直接执行：仅当执行被明确关闭时才模拟/跳过
	if !at.executionEnabled {
		log.Printf("  🚫 未启用执行：跳过开多 %s，杠杆=%d，仓位USD=%.2f", decision.Symbol, decision.Leverage, decision.PositionSizeUSD)
		return nil
	}

	log.Printf("  📈 开多仓: %s", decision.Symbol)

	// 全局预算与持仓上限硬约束
	{
		acct, aerr := at.GetAccountInfo()
		if aerr != nil {
			log.Printf("⚠️ 获取账户信息失败，暂不应用全局预算限制：%s %s: %v", decision.Symbol, decision.Action, aerr)
		} else {
			positionsCount := 0
			if pc, ok := acct["position_count"]; ok {
				switch x := pc.(type) {
				case int:
					positionsCount = x
				case int64:
					positionsCount = int(x)
				case float64:
					positionsCount = int(x)
				case float32:
					positionsCount = int(x)
				}
			}
			if positionsCount >= 3 {
				return fmt.Errorf("❌ 持仓数量已达上限(≥3)，拒绝开仓 %s", decision.Symbol)
			}

			marginUsed := 0.0
			if mu, ok := acct["margin_used"]; ok {
				switch x := mu.(type) {
				case float64:
					marginUsed = x
				case float32:
					marginUsed = float64(x)
				case int:
					marginUsed = float64(x)
				case int64:
					marginUsed = float64(x)
				}
			}
			totalEquity := 0.0
			if te, ok := acct["total_equity"]; ok {
				switch x := te.(type) {
				case float64:
					totalEquity = x
				case float32:
					totalEquity = float64(x)
				case int:
					totalEquity = float64(x)
				case int64:
					totalEquity = float64(x)
				}
			}
			// 动态预算适配：优先提升杠杆至配置值，其次缩小仓位USD
			if totalEquity <= 0 {
				log.Printf("⚠️ total_equity<=0，无法计算保证金占用，暂不应用全局预算限制：%s %s", decision.Symbol, decision.Action)
			} else {
				maxBudget := (at.config.MaxMarginUsagePct / 100.0) * totalEquity
				allowedMargin := maxBudget - marginUsed
				if allowedMargin <= 0 {
					return fmt.Errorf("❌ 全局保证金上限已用尽(%.2f%%≥%.0f%%)，拒绝开仓 %s", (marginUsed/totalEquity)*100, at.config.MaxMarginUsagePct, decision.Symbol)
				}

				symU := strings.ToUpper(decision.Symbol)
				cfgLev := at.config.AltcoinLeverage
				if strings.HasPrefix(symU, "BTC") || strings.HasPrefix(symU, "ETH") {
					cfgLev = at.config.BTCETHLeverage
				}
				targetLev := decision.Leverage
				if targetLev <= 0 {
					targetLev = 1
				}
				if targetLev > cfgLev {
					targetLev = cfgLev
				}

				requiredLev := int(math.Ceil(decision.PositionSizeUSD / allowedMargin))
				if requiredLev < 1 {
					requiredLev = 1
				}
				if requiredLev < targetLev {
					requiredLev = targetLev
				}
				proposedLev := requiredLev
				if proposedLev > cfgLev {
					proposedLev = cfgLev
				}
				allowedUSD := allowedMargin * float64(proposedLev)
				if decision.PositionSizeUSD > allowedUSD {
					oldUSD := decision.PositionSizeUSD
					oldLev := decision.Leverage
					decision.Leverage = proposedLev
					decision.PositionSizeUSD = allowedUSD
					log.Printf("⚠️ 预算门控：%s 杠杆 %dx→%dx；仓位 %.2fUSD→%.2fUSD，满足%.0f%%预算", decision.Symbol, oldLev, decision.Leverage, oldUSD, decision.PositionSizeUSD, at.config.MaxMarginUsagePct)
				} else if decision.Leverage < proposedLev {
					oldLev := decision.Leverage
					decision.Leverage = proposedLev
					log.Printf("ℹ️ 调整杠杆以满足预算/配置：%s 杠杆 %dx→%dx；保留原USD %.2f", decision.Symbol, oldLev, decision.Leverage, decision.PositionSizeUSD)
				}
			}
		}
	}

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_long 决策", decision.Symbol)
			}
		}
	}

	// 根据可能调整过的 USD 与杠杆，重新计算下单数量
	quantity = decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Leverage = decision.Leverage

	// 开仓
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈
	at.softStopPrices[posKey] = decision.StopLoss
	at.softTakePrices[posKey] = decision.TakeProfit
	log.Printf("  🧠 使用软止损/止盈：LONG stop=%.4f take=%.4f（仅在AI周期内评估）", decision.StopLoss, decision.TakeProfit)

	return nil
}

// executeOpenShortWithRecord 执行开空仓并记录详细信息
func (at *AutoTrader) executeOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 获取当前价格并计算数量（即使在 DryRun/未执行时也补齐记录字段）
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	quantity := decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 统一为直接执行：仅当执行被明确关闭时才模拟/跳过
	if !at.executionEnabled {
		log.Printf("  🚫 未启用执行：跳过开空 %s，杠杆=%d，仓位USD=%.2f", decision.Symbol, decision.Leverage, decision.PositionSizeUSD)
		return nil
	}

	log.Printf("  📉 开空仓: %s", decision.Symbol)

	// 全局预算与持仓上限硬约束
	{
		acct, aerr := at.GetAccountInfo()
		if aerr != nil {
			log.Printf("⚠️ 获取账户信息失败，暂不应用全局预算限制：%s %s: %v", decision.Symbol, decision.Action, aerr)
		} else {
			positionsCount := 0
			if pc, ok := acct["position_count"]; ok {
				switch x := pc.(type) {
				case int:
					positionsCount = x
				case int64:
					positionsCount = int(x)
				case float64:
					positionsCount = int(x)
				case float32:
					positionsCount = int(x)
				}
			}
			if positionsCount >= 3 {
				return fmt.Errorf("❌ 持仓数量已达上限(≥3)，拒绝开仓 %s", decision.Symbol)
			}

			marginUsed := 0.0
			if mu, ok := acct["margin_used"]; ok {
				switch x := mu.(type) {
				case float64:
					marginUsed = x
				case float32:
					marginUsed = float64(x)
				case int:
					marginUsed = float64(x)
				case int64:
					marginUsed = float64(x)
				}
			}
			totalEquity := 0.0
			if te, ok := acct["total_equity"]; ok {
				switch x := te.(type) {
				case float64:
					totalEquity = x
				case float32:
					totalEquity = float64(x)
				case int:
					totalEquity = float64(x)
				case int64:
					totalEquity = float64(x)
				}
			}
			// 动态预算适配：优先提升杠杆至配置值，其次缩小仓位USD
			if totalEquity <= 0 {
				log.Printf("⚠️ total_equity<=0，无法计算保证金占用，暂不应用全局预算限制：%s %s", decision.Symbol, decision.Action)
			} else {
				maxBudget := (at.config.MaxMarginUsagePct / 100.0) * totalEquity
				allowedMargin := maxBudget - marginUsed
				if allowedMargin <= 0 {
					return fmt.Errorf("❌ 全局保证金上限已用尽(%.2f%%≥%.0f%%)，拒绝开仓 %s", (marginUsed/totalEquity)*100, at.config.MaxMarginUsagePct, decision.Symbol)
				}

				symU := strings.ToUpper(decision.Symbol)
				cfgLev := at.config.AltcoinLeverage
				if strings.HasPrefix(symU, "BTC") || strings.HasPrefix(symU, "ETH") {
					cfgLev = at.config.BTCETHLeverage
				}
				targetLev := decision.Leverage
				if targetLev <= 0 {
					targetLev = 1
				}
				if targetLev > cfgLev {
					targetLev = cfgLev
				}

				requiredLev := int(math.Ceil(decision.PositionSizeUSD / allowedMargin))
				if requiredLev < 1 {
					requiredLev = 1
				}
				if requiredLev < targetLev {
					requiredLev = targetLev
				}
				proposedLev := requiredLev
				if proposedLev > cfgLev {
					proposedLev = cfgLev
				}

				allowedUSD := allowedMargin * float64(proposedLev)

				if decision.PositionSizeUSD > allowedUSD {
					oldUSD := decision.PositionSizeUSD
					oldLev := decision.Leverage
					decision.Leverage = proposedLev
					decision.PositionSizeUSD = allowedUSD
					log.Printf("⚠️ 预算门控：%s 杠杆 %dx→%dx；仓位 %.2fUSD→%.2fUSD，满足%.0f%%预算", decision.Symbol, oldLev, decision.Leverage, oldUSD, decision.PositionSizeUSD, at.config.MaxMarginUsagePct)
				} else if decision.Leverage < proposedLev {
					oldLev := decision.Leverage
					decision.Leverage = proposedLev
					log.Printf("ℹ️ 调整杠杆以满足预算/配置：%s 杠杆 %dx→%dx；保留原USD %.2f", decision.Symbol, oldLev, decision.Leverage, decision.PositionSizeUSD)
				}
			}
		}
	}

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_short 决策", decision.Symbol)
			}
		}
	}

	// 根据可能调整过的 USD 与杠杆，重新计算下单数量
	quantity = decision.PositionSizeUSD / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Leverage = decision.Leverage

	// 开仓
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈
	at.softStopPrices[posKey] = decision.StopLoss
	at.softTakePrices[posKey] = decision.TakeProfit
	log.Printf("  🧠 使用软止损/止盈：SHORT stop=%.4f take=%.4f（仅在AI周期内评估）", decision.StopLoss, decision.TakeProfit)

	return nil
}

// ManualOpenLong 手动开多（用于测试/调试接口）
func (at *AutoTrader) ManualOpenLong(symbol string, usd float64, leverage int) (map[string]interface{}, error) {
	if !at.executionEnabled {
		return nil, fmt.Errorf("execution disabled: 跳过开多 %s", symbol)
	}

	// 获取当前价格并计算数量
	price, err := at.trader.GetMarketPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("获取价格失败: %w", err)
	}
	if price <= 0 {
		return nil, fmt.Errorf("无效价格: %.8f", price)
	}
	if usd <= 0 {
		return nil, fmt.Errorf("USD仓位必须大于0")
	}
	quantity := usd / price

	// 杠杆限制在配置范围
	symU := strings.ToUpper(symbol)
	cfgLev := at.config.AltcoinLeverage
	if strings.HasPrefix(symU, "BTC") || strings.HasPrefix(symU, "ETH") {
		cfgLev = at.config.BTCETHLeverage
	}
	if leverage <= 0 {
		leverage = 1
	}
	if leverage > cfgLev {
		leverage = cfgLev
	}

	// 全局预算与持仓上限硬约束
	{
		acct, aerr := at.GetAccountInfo()
		if aerr != nil {
			log.Printf("⚠️ 获取账户信息失败，暂不应用全局预算限制：%s open_long: %v", symbol, aerr)
		} else {
			positionsCount := 0
			if pc, ok := acct["position_count"]; ok {
				switch x := pc.(type) {
				case int:
					positionsCount = x
				case int64:
					positionsCount = int(x)
				case float64:
					positionsCount = int(x)
				case float32:
					positionsCount = int(x)
				}
			}
			if positionsCount >= 3 {
				return nil, fmt.Errorf("❌ 持仓数量已达上限(≥3)，拒绝开仓 %s", symbol)
			}

			marginUsed := 0.0
			if mu, ok := acct["margin_used"]; ok {
				switch x := mu.(type) {
				case float64:
					marginUsed = x
				case float32:
					marginUsed = float64(x)
				case int:
					marginUsed = float64(x)
				case int64:
					marginUsed = float64(x)
				}
			}
			totalEquity := 0.0
			if te, ok := acct["total_equity"]; ok {
				switch x := te.(type) {
				case float64:
					totalEquity = x
				case float32:
					totalEquity = float64(x)
				case int:
					totalEquity = float64(x)
				case int64:
					totalEquity = float64(x)
				}
			}
			marginNeeded := 0.0
			if leverage > 0 {
				marginNeeded = usd / float64(leverage)
			}
			if totalEquity <= 0 {
				log.Printf("⚠️ total_equity<=0，无法计算保证金占用，暂不应用全局预算限制：%s open_long", symbol)
			} else {
				projectedPct := ((marginUsed + marginNeeded) / totalEquity) * 100
				if projectedPct > at.config.MaxMarginUsagePct {
					return nil, fmt.Errorf("❌ 预计保证金使用率将超限(%.2f%%→%.2f%%>%.0f%%)，拒绝开仓 %s", (marginUsed/totalEquity)*100, projectedPct, at.config.MaxMarginUsagePct, symbol)
				}
			}
		}
	}

	// 防止同向仓位叠加
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				return nil, fmt.Errorf("%s 已有多仓，拒绝重复开仓", symbol)
			}
		}
	}

	// 执行开仓
	order, err := at.trader.OpenLong(symbol, quantity, leverage)
	if err != nil {
		return nil, err
	}

	// 记录开仓时间用于风控与学习分析
	posKey := symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈（使用简单的保护：按5%/10%作为默认）
	// 注意：不同交易器实现可选择忽略或使用标记
	stopPrice := price * 0.95
	takeProfit := price * 1.10
	if err := at.trader.SetStopLoss(symbol, "LONG", quantity, stopPrice); err != nil {
		log.Printf("  ⚠ 设置止损失败(手动): %v", err)
	}
	if err := at.trader.SetTakeProfit(symbol, "LONG", quantity, takeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败(手动): %v", err)
	}
	// 写入决策日志（手动）
	var orderID int64
	if oid, ok := order["orderId"].(int64); ok {
		orderID = oid
	}
	action := logger.DecisionAction{
		Action:    "open_long",
		Symbol:    symbol,
		Quantity:  quantity,
		Leverage:  leverage,
		Price:     price,
		OrderID:   orderID,
		Timestamp: time.Now(),
		Success:   true,
	}
	record := &logger.DecisionRecord{
		Decisions:    []logger.DecisionAction{action},
		ExecutionLog: []string{fmt.Sprintf("manual open_long %s qty=%.4f lev=%d price=%.4f", symbol, quantity, leverage, price)},
		Success:      true,
	}
	if at.decisionLogger != nil {
		_ = at.decisionLogger.LogDecision(record)
	}

	return order, nil
}

// ManualOpenShort 手动开空（用于测试/调试接口）
func (at *AutoTrader) ManualOpenShort(symbol string, usd float64, leverage int) (map[string]interface{}, error) {
	if !at.executionEnabled {
		return nil, fmt.Errorf("execution disabled: 跳过开空 %s", symbol)
	}

	// 获取当前价格并计算数量
	price, err := at.trader.GetMarketPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("获取价格失败: %w", err)
	}
	if price <= 0 {
		return nil, fmt.Errorf("无效价格: %.8f", price)
	}
	if usd <= 0 {
		return nil, fmt.Errorf("USD仓位必须大于0")
	}
	quantity := usd / price

	// 杠杆限制在配置范围
	symU := strings.ToUpper(symbol)
	cfgLev := at.config.AltcoinLeverage
	if strings.HasPrefix(symU, "BTC") || strings.HasPrefix(symU, "ETH") {
		cfgLev = at.config.BTCETHLeverage
	}
	if leverage <= 0 {
		leverage = 1
	}
	if leverage > cfgLev {
		leverage = cfgLev
	}

	// 全局预算与持仓上限硬约束
	{
		acct, aerr := at.GetAccountInfo()
		if aerr != nil {
			log.Printf("⚠️ 获取账户信息失败，暂不应用全局预算限制：%s open_short: %v", symbol, aerr)
		} else {
			positionsCount := 0
			if pc, ok := acct["position_count"]; ok {
				switch x := pc.(type) {
				case int:
					positionsCount = x
				case int64:
					positionsCount = int(x)
				case float64:
					positionsCount = int(x)
				case float32:
					positionsCount = int(x)
				}
			}
			if positionsCount >= 3 {
				return nil, fmt.Errorf("❌ 持仓数量已达上限(≥3)，拒绝开仓 %s", symbol)
			}

			marginUsed := 0.0
			if mu, ok := acct["margin_used"]; ok {
				switch x := mu.(type) {
				case float64:
					marginUsed = x
				case float32:
					marginUsed = float64(x)
				case int:
					marginUsed = float64(x)
				case int64:
					marginUsed = float64(x)
				}
			}
			totalEquity := 0.0
			if te, ok := acct["total_equity"]; ok {
				switch x := te.(type) {
				case float64:
					totalEquity = x
				case float32:
					totalEquity = float64(x)
				case int:
					totalEquity = float64(x)
				case int64:
					totalEquity = float64(x)
				}
			}
			marginNeeded := 0.0
			if leverage > 0 {
				marginNeeded = usd / float64(leverage)
			}
			if totalEquity <= 0 {
				log.Printf("⚠️ total_equity<=0，无法计算保证金占用，暂不应用全局预算限制：%s open_short", symbol)
			} else {
				projectedPct := ((marginUsed + marginNeeded) / totalEquity) * 100
				if projectedPct > at.config.MaxMarginUsagePct {
					return nil, fmt.Errorf("❌ 预计保证金使用率将超限(%.2f%%→%.2f%%>%.0f%%)，拒绝开仓 %s", (marginUsed/totalEquity)*100, projectedPct, at.config.MaxMarginUsagePct, symbol)
				}
			}
		}
	}

	// 防止同向仓位叠加
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				return nil, fmt.Errorf("%s 已有空仓，拒绝重复开仓", symbol)
			}
		}
	}

	// 执行开仓
	order, err := at.trader.OpenShort(symbol, quantity, leverage)
	if err != nil {
		return nil, err
	}

	// 记录开仓时间用于风控与学习分析
	posKey := symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 设置止损止盈（使用简单的保护：按5%/10%作为默认）
	stopPrice := price * 1.05
	takeProfit := price * 0.90
	if err := at.trader.SetStopLoss(symbol, "SHORT", quantity, stopPrice); err != nil {
		log.Printf("  ⚠ 设置止损失败(手动): %v", err)
	}
	if err := at.trader.SetTakeProfit(symbol, "SHORT", quantity, takeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败(手动): %v", err)
	}
	// 写入决策日志（手动）
	var orderID int64
	if oid, ok := order["orderId"].(int64); ok {
		orderID = oid
	}
	action := logger.DecisionAction{
		Action:    "open_short",
		Symbol:    symbol,
		Quantity:  quantity,
		Leverage:  leverage,
		Price:     price,
		OrderID:   orderID,
		Timestamp: time.Now(),
		Success:   true,
	}
	record := &logger.DecisionRecord{
		Decisions:    []logger.DecisionAction{action},
		ExecutionLog: []string{fmt.Sprintf("manual open_short %s qty=%.4f lev=%d price=%.4f", symbol, quantity, leverage, price)},
		Success:      true,
	}
	if at.decisionLogger != nil {
		_ = at.decisionLogger.LogDecision(record)
	}

	return order, nil
}

// ManualCloseLong 手动平多（quantity=0 全平）
func (at *AutoTrader) ManualCloseLong(symbol string) (map[string]interface{}, error) {
	if !at.executionEnabled {
		return nil, fmt.Errorf("execution disabled: 跳过平多 %s", symbol)
	}
	// 记录当前价格和数量用于日志
	price, _ := at.trader.GetMarketPrice(symbol)
	qty := 0.0
	lev := 0
	if positions, err := at.trader.GetPositions(); err == nil {
		for _, pos := range positions {
			if ps, ok := pos["symbol"].(string); ok && ps == symbol {
				if side, ok := pos["side"].(string); ok && side == "long" {
					if q, ok := pos["positionAmt"].(float64); ok {
						qty = q
					}
					if l, ok := pos["leverage"].(float64); ok {
						lev = int(l)
					}
					break
				}
			}
		}
	}

	order, err := at.trader.CloseLong(symbol, 0)
	if err != nil {
		return nil, err
	}
	var orderID int64
	if oid, ok := order["orderId"].(int64); ok {
		orderID = oid
	}

	action := logger.DecisionAction{
		Action:    "close_long",
		Symbol:    symbol,
		Quantity:  qty,
		Leverage:  lev,
		Price:     price,
		OrderID:   orderID,
		Timestamp: time.Now(),
		Success:   true,
	}
	record := &logger.DecisionRecord{
		Decisions:    []logger.DecisionAction{action},
		ExecutionLog: []string{fmt.Sprintf("manual close_long %s qty=%.4f price=%.4f", symbol, qty, price)},
		Success:      true,
	}
	if at.decisionLogger != nil {
		_ = at.decisionLogger.LogDecision(record)
	}

	return order, nil
}

// ManualCloseShort 手动平空（quantity=0 全平）
func (at *AutoTrader) ManualCloseShort(symbol string) (map[string]interface{}, error) {
	if !at.executionEnabled {
		return nil, fmt.Errorf("execution disabled: 跳过平空 %s", symbol)
	}
	// 记录当前价格和数量用于日志
	price, _ := at.trader.GetMarketPrice(symbol)
	qty := 0.0
	lev := 0
	if positions, err := at.trader.GetPositions(); err == nil {
		for _, pos := range positions {
			if ps, ok := pos["symbol"].(string); ok && ps == symbol {
				if side, ok := pos["side"].(string); ok && side == "short" {
					if q, ok := pos["positionAmt"].(float64); ok {
						qty = q
					}
					if l, ok := pos["leverage"].(float64); ok {
						lev = int(l)
					}
					break
				}
			}
		}
	}

	order, err := at.trader.CloseShort(symbol, 0)
	if err != nil {
		return nil, err
	}
	var orderID int64
	if oid, ok := order["orderId"].(int64); ok {
		orderID = oid
	}

	action := logger.DecisionAction{
		Action:    "close_short",
		Symbol:    symbol,
		Quantity:  qty,
		Leverage:  lev,
		Price:     price,
		OrderID:   orderID,
		Timestamp: time.Now(),
		Success:   true,
	}
	record := &logger.DecisionRecord{
		Decisions:    []logger.DecisionAction{action},
		ExecutionLog: []string{fmt.Sprintf("manual close_short %s qty=%.4f price=%.4f", symbol, qty, price)},
		Success:      true,
	}
	if at.decisionLogger != nil {
		_ = at.decisionLogger.LogDecision(record)
	}

	return order, nil
}

// executeCloseLongWithRecord 执行平多仓并记录详细信息
func (at *AutoTrader) executeCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 先确保 symbol 有效；如果未指定或格式不正确，则根据当前持仓回退推断
	{
		sym := strings.TrimSpace(decision.Symbol)
		up := strings.ToUpper(sym)
		if sym == "" || up == "USDT" || up == "OKXUSDT" || !strings.HasSuffix(up, "USDT") {
			positions, perr := at.trader.GetPositions()
			if perr != nil {
				return fmt.Errorf("决策未指定有效币种，且获取持仓失败: %v", perr)
			}
			var longSyms []string
			for _, p := range positions {
				if ps, ok := p["side"].(string); ok && ps == "long" {
					if s, ok := p["symbol"].(string); ok && s != "" {
						longSyms = append(longSyms, s)
					}
				}
			}
			if len(longSyms) == 1 {
				sym = longSyms[0]
			} else if len(longSyms) == 0 {
				return fmt.Errorf("未找到可平的多仓（无 long 仓位），且决策未指定有效币种")
			} else {
				return fmt.Errorf("存在多个多仓：%v。决策未指定币种，无法确定平仓目标", longSyms)
			}
		}
		decision.Symbol = sym
	}

	// 获取当前价格（即使在 DryRun/未执行时也补齐记录字段）
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 统一为直接执行：仅当执行被明确关闭时才模拟/跳过
	if !at.executionEnabled {
		log.Printf("  🚫 未启用执行：跳过平多 %s", decision.Symbol)
		return nil
	}
	log.Printf("  🔄 平多仓: %s", decision.Symbol)

	// 平仓
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")
	return nil
}

// executeCloseShortWithRecord 执行平空仓并记录详细信息
func (at *AutoTrader) executeCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 先确保 symbol 有效；如果未指定或格式不正确，则根据当前持仓回退推断
	{
		sym := strings.TrimSpace(decision.Symbol)
		up := strings.ToUpper(sym)
		if sym == "" || up == "USDT" || up == "OKXUSDT" || !strings.HasSuffix(up, "USDT") {
			positions, perr := at.trader.GetPositions()
			if perr != nil {
				return fmt.Errorf("决策未指定有效币种，且获取持仓失败: %v", perr)
			}
			var shortSyms []string
			for _, p := range positions {
				if ps, ok := p["side"].(string); ok && ps == "short" {
					if s, ok := p["symbol"].(string); ok && s != "" {
						shortSyms = append(shortSyms, s)
					}
				}
			}
			if len(shortSyms) == 1 {
				sym = shortSyms[0]
			} else if len(shortSyms) == 0 {
				return fmt.Errorf("未找到可平的空仓（无 short 仓位），且决策未指定有效币种")
			} else {
				return fmt.Errorf("存在多个空仓：%v。决策未指定币种，无法确定平仓目标", shortSyms)
			}
		}
		decision.Symbol = sym
	}

	// 获取当前价格（即使在 DryRun/未执行时也补齐记录字段）
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 统一为直接执行：仅当执行被明确关闭时才模拟/跳过
	if !at.executionEnabled {
		log.Printf("  🚫 未启用执行：跳过平空 %s", decision.Symbol)
		return nil
	}
	log.Printf("  🔄 平空仓: %s", decision.Symbol)

	// 平仓
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 平仓成功")
	return nil
}

// GetID 获取trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 获取trader名称
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 获取AI模型
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetStatus 获取系统状态（用于API）
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	// 读取当前提示词模板变体（环境变量覆盖，默认 zhugefan）
	variant := os.Getenv("NOFX_PROMPT_VARIANT")
	if strings.TrimSpace(variant) == "" {
		variant = prompt.DefaultVariant
	}

	return map[string]interface{}{
		"trader_id":                  at.id,
		"trader_name":                at.name,
		"ai_model":                   at.aiModel,
		"exchange":                   at.exchange,
		"is_running":                 at.isRunning,
		"start_time":                 at.startTime.Format(time.RFC3339),
		"runtime_minutes":            int(time.Since(at.startTime).Minutes()),
		"call_count":                 at.callCount,
		"initial_balance":            at.initialBalance,
		"scan_interval":              at.config.ScanInterval.String(),
		"scan_interval_minutes":      int(at.config.ScanInterval.Minutes()),
		"scan_interval_effective_at": at.scanIntervalAppliedAt.Format(time.RFC3339),
		"stop_until":                 at.stopUntil.Format(time.RFC3339),
		"last_reset_time":            at.lastResetTime.Format(time.RFC3339),
		"ai_provider":                aiProvider,
		"execution_enabled":          at.executionEnabled,
		"prompt_variant":             variant,
	}
}

// GetOKXFills 获取OKX成交记录（仅当该trader为OKX）
func (at *AutoTrader) GetOKXFills(limit int) ([]map[string]interface{}, error) {
	if strings.ToLower(at.exchange) != "okx" {
		return nil, fmt.Errorf("该trader非OKX，无法获取成交记录")
	}
	okx, ok := at.trader.(*OKXTrader)
	if !ok {
		return nil, fmt.Errorf("底层trader不是OKXTrader类型")
	}
	return okx.GetFills(limit)
}

// SetExecutionEnabled 设置是否启用自动执行
func (at *AutoTrader) SetExecutionEnabled(enabled bool) {
	at.executionEnabled = enabled
}

// IsExecutionEnabled 获取自动执行开关状态
func (at *AutoTrader) IsExecutionEnabled() bool {
	return at.executionEnabled
}

// RunOnce 触发一次AI决策周期（单次）
func (at *AutoTrader) RunOnce() error {
	return at.runCycle()
}

// CloseAllPositions 平掉该Trader的所有持仓
// 返回成功平仓的持仓数量
func (at *AutoTrader) CloseAllPositions() (int, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return 0, fmt.Errorf("failed to get positions: %w", err)
	}

	closed := 0
	for _, pos := range positions {
		symbolVal, ok := pos["symbol"]
		if !ok {
			continue
		}
		symbol, _ := symbolVal.(string)

		sideVal, ok := pos["side"]
		if !ok {
			continue
		}
		side, _ := sideVal.(string)

		// 尝试读取数量（用于日志）；实际平仓使用 quantity=0 表示全部
		var qty float64
		if qv, ok := pos["positionAmt"]; ok {
			switch v := qv.(type) {
			case float64:
				qty = v
			case int:
				qty = float64(v)
			case string:
				if parsed, perr := strconv.ParseFloat(v, 64); perr == nil {
					qty = parsed
				}
			}
		}

		switch side {
		case "long":
			if _, err := at.trader.CloseLong(symbol, 0); err != nil {
				log.Printf("Failed to close long %s (qty=%.8f): %v", symbol, qty, err)
				continue
			}
			closed++
		case "short":
			if _, err := at.trader.CloseShort(symbol, 0); err != nil {
				log.Printf("Failed to close short %s (qty=%.8f): %v", symbol, qty, err)
				continue
			}
			closed++
		default:
			// unknown side, skip
			continue
		}

		// 最后尝试取消该symbol所有挂单（容错即可）
		if err := at.trader.CancelAllOrders(symbol); err != nil {
			log.Printf("Failed to cancel orders for %s: %v", symbol, err)
		}
	}

	return closed, nil
}

// RunAiCloseThenOpen 先让AI决策并执行平仓，再让AI决策并执行开仓
// 该方法将分别记录两次决策日志，确保流程清晰：第一次仅执行 close_*，第二次仅执行 open_*
func (at *AutoTrader) RunAiCloseThenOpen() (map[string]interface{}, error) {
	results := map[string]interface{}{
		"close_phase": map[string]interface{}{},
		"open_phase":  map[string]interface{}{},
	}

	// 1) 构建上下文并获取AI决策（平仓阶段）
	ctx, err := at.buildTradingContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build trading context: %w", err)
	}

	fullDecision, err := decision.GetFullDecisionWithClient(at.aiClient, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get AI decisions for close phase: %w", err)
	}

	// 过滤仅 close_* 的决策
	var closeDecisions []decision.Decision
	for _, d := range fullDecision.Decisions {
		if d.Action == "close_long" || d.Action == "close_short" {
			closeDecisions = append(closeDecisions, d)
		}
	}
	closeRecord := &logger.DecisionRecord{ExecutionLog: []string{}, Success: true}
	// 补齐提示与思维链，确保前端步骤1可视化
	closeRecord.SystemPrompt = fullDecision.SystemPrompt
	closeRecord.InputPrompt = fullDecision.UserPrompt
	closeRecord.CoTTrace = fullDecision.CoTTrace
	// 补齐JSON决策数组，确保前端步骤2在无决策时也显示为 []
	if len(closeDecisions) > 0 {
		if b, err := json.MarshalIndent(closeDecisions, "", "  "); err == nil {
			closeRecord.DecisionJSON = string(b)
		} else {
			closeRecord.DecisionJSON = "[]"
		}
	} else {
		closeRecord.DecisionJSON = "[]"
	}
	for _, d := range closeDecisions {
		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}
		err = at.executeDecisionWithRecord(&d, &actionRecord)
		if err != nil {
			actionRecord.Error = err.Error()
			closeRecord.ExecutionLog = append(closeRecord.ExecutionLog, fmt.Sprintf("%s %s failed: %v", d.Symbol, d.Action, err))
			closeRecord.Success = false
		} else {
			actionRecord.Success = true
			closeRecord.ExecutionLog = append(closeRecord.ExecutionLog, fmt.Sprintf("%s %s succeeded", d.Symbol, d.Action))
		}
		closeRecord.Decisions = append(closeRecord.Decisions, actionRecord)
	}
	err = at.decisionLogger.LogDecision(closeRecord)
	if err != nil {
		log.Printf("Failed to save close phase decision record: %v", err)
	}

	results["close_phase"] = map[string]interface{}{
		"count":    len(closeDecisions),
		"executed": closeRecord.Decisions,
		"success":  closeRecord.Success,
	}

	// 2) 再次构建上下文并获取AI决策（开仓阶段）
	ctx2, err := at.buildTradingContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build trading context for open phase: %w", err)
	}
	fullDecision2, err := decision.GetFullDecisionWithClient(at.aiClient, ctx2)
	if err != nil {
		return nil, fmt.Errorf("failed to get AI decisions for open phase: %w", err)
	}

	// 过滤仅 open_* 的决策
	var openDecisions []decision.Decision
	for _, d := range fullDecision2.Decisions {
		if d.Action == "open_long" || d.Action == "open_short" {
			openDecisions = append(openDecisions, d)
		}
	}
	openRecord := &logger.DecisionRecord{ExecutionLog: []string{}, Success: true}
	// 补齐提示与思维链，确保前端步骤1可视化
	openRecord.SystemPrompt = fullDecision2.SystemPrompt
	openRecord.InputPrompt = fullDecision2.UserPrompt
	openRecord.CoTTrace = fullDecision2.CoTTrace
	// 补齐JSON决策数组，确保前端步骤2在无决策时也显示为 []
	if len(openDecisions) > 0 {
		if b, err := json.MarshalIndent(openDecisions, "", "  "); err == nil {
			openRecord.DecisionJSON = string(b)
		} else {
			openRecord.DecisionJSON = "[]"
		}
	} else {
		openRecord.DecisionJSON = "[]"
	}
	for _, d := range openDecisions {
		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}
		err = at.executeDecisionWithRecord(&d, &actionRecord)
		if err != nil {
			actionRecord.Error = err.Error()
			openRecord.ExecutionLog = append(openRecord.ExecutionLog, fmt.Sprintf("%s %s failed: %v", d.Symbol, d.Action, err))
			openRecord.Success = false
		} else {
			actionRecord.Success = true
			openRecord.ExecutionLog = append(openRecord.ExecutionLog, fmt.Sprintf("%s %s succeeded", d.Symbol, d.Action))
		}
		openRecord.Decisions = append(openRecord.Decisions, actionRecord)
	}
	err = at.decisionLogger.LogDecision(openRecord)
	if err != nil {
		log.Printf("Failed to save open phase decision record: %v", err)
	}

	results["open_phase"] = map[string]interface{}{
		"count":    len(openDecisions),
		"executed": openRecord.Decisions,
		"success":  openRecord.Success,
	}

	return results, nil
}

// GetAccountInfo 获取账户信息（用于API）
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	// 在获取账户前同步交易所入金/出金为投资调整（节流控制）
	at.syncInvestmentsFromExchange()
	// DryRun 时不再直接返回0值，而是尽量读取真实数据，失败则回退到初始余额

	balance, err := at.trader.GetBalance()
	if err != nil {
		// 容错：返回占位数据，避免前端接口报错
		log.Printf("⚠️  获取余额失败，返回占位数据: %v", err)
		// DryRun 回退：如配置了初始余额，则以其作为钱包与可用余额
		wallet := 0.0
		avail := 0.0
		if at.initialBalance > 0 {
			wallet = at.initialBalance
			avail = at.initialBalance
		}
		balance = map[string]interface{}{
			"totalWalletBalance":    wallet,
			"totalUnrealizedProfit": 0.0,
			"availableBalance":      avail,
		}
	}

	// 获取账户字段
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

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 获取持仓计算总保证金
	positions, err := at.trader.GetPositions()
	if err != nil {
		log.Printf("⚠️  获取持仓失败，返回空持仓: %v", err)
		positions = []map[string]interface{}{}
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

	// 自动基线对齐（谨慎）：检测到差额仅记录日志，避免将盈亏误判为入金/出金
	if at.autoCalibrateBaseline && len(positions) == 0 && at.calibrationThreshold > 0 {
		base := at.investedBaseline()
		delta := totalWalletBalance - base
		if math.Abs(delta) >= at.calibrationThreshold {
			log.Printf("ℹ️ [%s] 检测到账户余额与投入基线存在差额 Δ=%.2f (wallet %.2f vs baseline %.2f)。为避免误判，未自动记录资金调整。", at.GetName(), delta, totalWalletBalance, base)
		}
	}

	// 计算总盈亏（相对真实投入基线：初始余额 + 额外投入）
	invested := at.investedBaseline()
	totalPnL := totalEquity - invested
	totalPnLPct := 0.0
	if invested > 0 {
		totalPnLPct = (totalPnL / invested) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 计算当日盈亏：以“当天首次可用的净值”为基线
	today := time.Now().Format("2006-01-02")
	if at.dailyBaselineDate != today || at.dailyBaseline <= 0 {
		at.dailyBaselineDate = today
		at.dailyBaseline = totalEquity
		// 尝试持久化当前日基线（可选）
		_ = at.saveDailyBaselineToFile()
		log.Printf("📅 [%s] 设置当日基线: date=%s baseline=%.2f", at.GetName(), today, at.dailyBaseline)
	}
	dailyPnL := totalEquity - at.dailyBaseline

	return map[string]interface{}{
		// 核心字段
		"total_equity":      totalEquity,           // 账户净值 = wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // 钱包余额（不含未实现盈亏）
		"unrealized_profit": totalUnrealizedProfit, // 未实现盈亏（从API）
		"available_balance": availableBalance,      // 可用余额

		// 盈亏统计
		"total_pnl":            totalPnL,           // 总盈亏 = equity - initial
		"total_pnl_pct":        totalPnLPct,        // 总盈亏百分比
		"total_unrealized_pnl": totalUnrealizedPnL, // 未实现盈亏（从持仓计算）
		"initial_balance":      at.initialBalance,  // 初始余额
		"invested_amount":      invested,           // 真实投入 = 初始余额 + 额外投入 + 调整
		"daily_pnl":            dailyPnL,           // 日盈亏 = 当前净值 - 当日基线

		// 持仓信息
		"position_count":  len(positions),  // 持仓数量
		"margin_used":     totalMarginUsed, // 保证金占用
		"margin_used_pct": marginUsedPct,   // 保证金使用率
	}, nil
}

// SetInitialBalance 动态设置初始资金基线（用于存取款后的基线校准）
func (at *AutoTrader) SetInitialBalance(v float64) {
	if v > 0 {
		at.initialBalance = v
		_ = at.saveInitialBalanceToFile()
	}
}

// GetPositions 获取持仓列表（用于API）
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		// 容错：返回空列表，避免前端报错
		log.Printf("⚠️  获取持仓失败，返回空列表: %v", err)
		return []map[string]interface{}{}, nil
	}

	// 保证空列表返回 [] 而非 null
	result := make([]map[string]interface{}, 0)
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

		// 按保证金计算盈亏百分比
		marginUsed := (quantity * markPrice) / float64(leverage)
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}

		result = append(result, map[string]interface{}{
			"symbol":                    symbol,
			"side":                      side,
			"entry_price":               entryPrice,
			"mark_price":                markPrice,
			"quantity":                  quantity,
			"leverage":                  leverage,
			"unrealized_pnl":            unrealizedPnl,
			"unrealized_pnl_pct":        pnlPct,
			"unrealized_pnl_pct_margin": pnlPct,
			"liquidation_price":         liquidationPrice,
			"margin_used":               marginUsed,
		})
	}

	return result, nil
}

// sortDecisionsByPriority 对决策排序：先平仓，再开仓，最后hold/wait
// 这样可以避免换仓时仓位叠加超限
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// 定义优先级
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // 最高优先级：先平仓
		case "open_long", "open_short":
			return 2 // 次优先级：后开仓
		case "hold", "wait":
			return 3 // 最低优先级：观望
		default:
			return 999 // 未知动作放最后
		}
	}

	// 复制决策列表
	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	// 按优先级排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// 持久化：保存初始余额到文件（可选）
func (at *AutoTrader) saveInitialBalanceToFile() error {
	if at.baselineStatePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(at.baselineStatePath), 0o755); err != nil {
		return err
	}
	data := map[string]interface{}{
		"initial_balance": at.initialBalance,
		"updated_at":      time.Now().Unix(),
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(at.baselineStatePath, b, 0o644)
}

// 持久化：读取初始余额文件
func (at *AutoTrader) loadInitialBalanceFromFile() (float64, error) {
	if at.baselineStatePath == "" {
		return 0, fmt.Errorf("no state path")
	}
	b, err := os.ReadFile(at.baselineStatePath)
	if err != nil {
		return 0, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return 0, err
	}
	if v, ok := m["initial_balance"].(float64); ok {
		return v, nil
	}
	if vInt, ok := m["initial_balance"].(int); ok {
		return float64(vInt), nil
	}
	if vStr, ok := m["initial_balance"].(string); ok {
		if f, err := strconv.ParseFloat(vStr, 64); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("invalid state file")
}

// 持久化：保存当日基线（可选）
func (at *AutoTrader) saveDailyBaselineToFile() error {
	if at.baselineStatePath == "" {
		return nil
	}
	// 使用相邻文件 daily_baseline_<id>.json
	safeID := strings.ReplaceAll(at.id, " ", "_")
	fileName := fmt.Sprintf("daily_baseline_%s.json", safeID)
	p := filepath.Join(filepath.Dir(at.baselineStatePath), fileName)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data := map[string]interface{}{
		"date":       at.dailyBaselineDate,
		"baseline":   at.dailyBaseline,
		"updated_at": time.Now().Unix(),
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// 持久化：读取当日基线（可选）
func (at *AutoTrader) loadDailyBaselineFromFile() (float64, string, error) {
	if at.baselineStatePath == "" {
		return 0, "", fmt.Errorf("no state path")
	}
	safeID := strings.ReplaceAll(at.id, " ", "_")
	fileName := fmt.Sprintf("daily_baseline_%s.json", safeID)
	p := filepath.Join(filepath.Dir(at.baselineStatePath), fileName)
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return 0, "", err
	}
	date := ""
	if ds, ok := m["date"].(string); ok {
		date = ds
	}
	baseline := 0.0
	if v, ok := m["baseline"].(float64); ok {
		baseline = v
	} else if vInt, ok := m["baseline"].(int); ok {
		baseline = float64(vInt)
	} else if vStr, ok := m["baseline"].(string); ok {
		if f, err := strconv.ParseFloat(vStr, 64); err == nil {
			baseline = f
		}
	}
	if baseline <= 0 || date == "" {
		return 0, "", fmt.Errorf("invalid daily baseline file")
	}
	return baseline, date, nil
}

// InvestmentAdjustment 资金调整事件（正数为追加入金，负数为取出/划转）
type InvestmentAdjustment struct {
	Amount    float64   `json:"amount"`
	Timestamp time.Time `json:"timestamp"`
	Note      string    `json:"note,omitempty"`
}

// AddInvestmentDelta 追加一条资金调整记录（正加负减），并持久化
func (at *AutoTrader) AddInvestmentDelta(amount float64, note string) error {
	if amount == 0 {
		return nil
	}
	adj := InvestmentAdjustment{Amount: amount, Timestamp: time.Now(), Note: note}
	at.investmentAdjustments = append(at.investmentAdjustments, adj)
	return at.saveInvestmentAdjustmentsToFile()
}

// GetInvestedAmount 返回当前累计真实投入金额（初始+额外+所有调整）
func (at *AutoTrader) GetInvestedAmount() float64 {
	return at.investedBaseline()
}

// GetInvestmentAdjustments 返回资金调整事件列表（只读副本）
func (at *AutoTrader) GetInvestmentAdjustments() []InvestmentAdjustment {
	// 返回副本，避免外部修改内部切片
	out := make([]InvestmentAdjustment, len(at.investmentAdjustments))
	copy(out, at.investmentAdjustments)
	return out
}

// GetInvestedAmountAt 返回指定时间点累计真实投入金额（初始+额外+截止该时间的调整）
func (at *AutoTrader) GetInvestedAmountAt(t time.Time) float64 {
	base := at.initialBalance
	if at.config.ExtraInvestment > 0 {
		base += at.config.ExtraInvestment
	}
	for _, adj := range at.investmentAdjustments {
		if !adj.Timestamp.After(t) {
			base += adj.Amount
		}
	}
	return base
}

// saveInvestmentAdjustmentsToFile 保存资金调整记录到本地文件
func (at *AutoTrader) saveInvestmentAdjustmentsToFile() error {
	if at.investmentStatePath == "" {
		return nil
	}
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(at.investmentStatePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(at.investmentAdjustments, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(at.investmentStatePath, data, 0o644)
}

// loadInvestmentAdjustmentsFromFile 读取本地资金调整记录
func (at *AutoTrader) loadInvestmentAdjustmentsFromFile() ([]InvestmentAdjustment, error) {
	if at.investmentStatePath == "" {
		return nil, nil
	}
	b, err := os.ReadFile(at.investmentStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []InvestmentAdjustment{}, nil
		}
		return nil, err
	}
	var list []InvestmentAdjustment
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}
