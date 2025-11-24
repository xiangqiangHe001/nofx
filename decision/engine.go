package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"nofx/prompt"
	"os"
	"strings"
	"time"
)

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"` // 持仓更新时间戳（毫秒）
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime        string                  `json:"current_time"`
	RuntimeMinutes     int                     `json:"runtime_minutes"`
	CallCount          int                     `json:"call_count"`
	Account            AccountInfo             `json:"account"`
	Positions          []PositionInfo          `json:"positions"`
	CandidateCoins     []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap      map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap       map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance        interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage     int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage    int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
	MinRiskRewardRatio float64                 `json:"-"` // 最小风险回报比（从配置读取）
	MaxMarginUsagePct  float64                 `json:"-"`
	CycleWeight4h      float64                 `json:"-"`
	CycleWeight1h      float64                 `json:"-"`
	CycleWeight15m     float64                 `json:"-"`
	CycleWeight3m      float64                 `json:"-"`
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	Confidence      float64 `json:"confidence,omitempty"` // 信心度（建议按0–1输出；解析兼容0–100）
	RiskUSD         float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning       string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 系统提示词（发送给AI的系统prompt）
	UserPrompt   string     `json:"user_prompt"`   // 发送给AI的输入prompt
	CoTTrace     string     `json:"cot_trace"`     // 思维链分析（AI输出）
	Decisions    []Decision `json:"decisions"`     // 具体决策列表
	Timestamp    time.Time  `json:"timestamp"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
// 保留原接口：继续使用包级默认客户端（兼容旧调用）
func GetFullDecision(ctx *Context) (*FullDecision, error) {
	if _, err := market.Get("BTCUSDT"); err != nil {
		return nil, fmt.Errorf("network precheck failed: %v", err)
	}
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch market data: %w", err)
	}

	// 打印当前启用的提示词变体，便于运行时确认
	log.Printf("[Prompt] Active variant: %s", activePromptVariant())

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MinRiskRewardRatio, ctx.MaxMarginUsagePct)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcp.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to call AI API: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MinRiskRewardRatio)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt // 保存系统prompt
	decision.UserPrompt = userPrompt     // 保存输入prompt
	return decision, nil
}

// GetFullDecisionWithClient 使用指定的AI客户端获取完整交易决策（推荐，避免全局冲突）
func GetFullDecisionWithClient(client *mcp.Client, ctx *Context) (*FullDecision, error) {
	if _, err := market.Get("BTCUSDT"); err != nil {
		return nil, fmt.Errorf("network precheck failed: %v", err)
	}
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch market data: %w", err)
	}

	// 打印当前启用的提示词变体，便于运行时确认
	log.Printf("[Prompt] Active variant: %s", activePromptVariant())

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MinRiskRewardRatio, ctx.MaxMarginUsagePct)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）——使用传入client避免defaultClient被其他trader覆盖
	aiResponse, err := client.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to call AI API: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MinRiskRewardRatio)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt // 保存系统prompt
	decision.UserPrompt = userPrompt     // 保存输入prompt
	return decision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 并发获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		data, err := market.Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体，只记录错误
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于15M USD的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < 15 {
				log.Printf("%s OI value too low (%.2fM USD < 15M), skipping [OI:%.0f × Price:%.4f]",
					symbol, oiValueInMillions, data.OpenInterest.Latest, data.CurrentPrice)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// 直接返回候选池的全部币种数量
	// 因为候选池已经在 auto_trader.go 中筛选过了
	// 固定分析前20个评分最高的币种（来自AI500）
	return len(ctx.CandidateCoins)
}

// buildSystemPrompt 构建 System Prompt（固定规则，可缓存）
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, minRR float64, maxMarginUsagePct float64) string {
	return prompt.RenderSystemPrompt(activePromptVariant(), accountEquity, btcEthLeverage, altcoinLeverage, minRR, maxMarginUsagePct)
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("**时间**: %s | **周期**: #%d | **运行**: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC/ETH/SOL 多周期技术概览（MACD 与柱状图并列）
	if btcData, ok := ctx.MarketDataMap["BTCUSDT"]; ok {
		sb.WriteString(fmt.Sprintf("**BTC 3m**: %.2f (MACD: %.4f | Hist: %+.4f) | **15m**: (MACD: %.4f | Hist: %+.4f) | **1h**: (MACD: %.4f | Hist: %+.4f) | **4h**: (MACD: %.4f | Hist: %+.4f) | RSI(3m): %.2f\n\n",
			btcData.CurrentPrice,
			btcData.CurrentMACD3m, btcData.CurrentMACDHistogram3m,
			btcData.CurrentMACD15m, btcData.CurrentMACDHistogram15m,
			btcData.CurrentMACD1h, btcData.CurrentMACDHistogram1h,
			btcData.CurrentMACD4h, btcData.CurrentMACDHistogram4h,
			btcData.CurrentRSI7))
	}
	if ethData, ok := ctx.MarketDataMap["ETHUSDT"]; ok {
		sb.WriteString(fmt.Sprintf("**ETH 3m**: %.2f (MACD: %.4f | Hist: %+.4f) | **15m**: (MACD: %.4f | Hist: %+.4f) | **1h**: (MACD: %.4f | Hist: %+.4f) | **4h**: (MACD: %.4f | Hist: %+.4f) | RSI(3m): %.2f\n\n",
			ethData.CurrentPrice,
			ethData.CurrentMACD3m, ethData.CurrentMACDHistogram3m,
			ethData.CurrentMACD15m, ethData.CurrentMACDHistogram15m,
			ethData.CurrentMACD1h, ethData.CurrentMACDHistogram1h,
			ethData.CurrentMACD4h, ethData.CurrentMACDHistogram4h,
			ethData.CurrentRSI7))
	}
	if solData, ok := ctx.MarketDataMap["SOLUSDT"]; ok {
		sb.WriteString(fmt.Sprintf("**SOL 3m**: %.2f (MACD: %.4f | Hist: %+.4f) | **15m**: (MACD: %.4f | Hist: %+.4f) | **1h**: (MACD: %.4f | Hist: %+.4f) | **4h**: (MACD: %.4f | Hist: %+.4f) | RSI(3m): %.2f\n\n",
			solData.CurrentPrice,
			solData.CurrentMACD3m, solData.CurrentMACDHistogram3m,
			solData.CurrentMACD15m, solData.CurrentMACDHistogram15m,
			solData.CurrentMACD1h, solData.CurrentMACDHistogram1h,
			solData.CurrentMACD4h, solData.CurrentMACDHistogram4h,
			solData.CurrentRSI7))
	}

	if btcData, ok := ctx.MarketDataMap["BTCUSDT"]; ok {
		s4 := 0.0
		if btcData.CurrentMACD4h > 0 {
			s4 += 0.6
		} else if btcData.CurrentMACD4h < 0 {
			s4 -= 0.6
		}
		if btcData.CurrentMACDHistogram4h > 0 {
			s4 += 0.4
		} else if btcData.CurrentMACDHistogram4h < 0 {
			s4 -= 0.4
		}
		s1 := 0.0
		if btcData.CurrentMACD1h > 0 {
			s1 += 0.6
		} else if btcData.CurrentMACD1h < 0 {
			s1 -= 0.6
		}
		if btcData.CurrentMACDHistogram1h > 0 {
			s1 += 0.4
		} else if btcData.CurrentMACDHistogram1h < 0 {
			s1 -= 0.4
		}
		s15 := 0.0
		if btcData.CurrentMACD15m > 0 {
			s15 += 0.6
		} else if btcData.CurrentMACD15m < 0 {
			s15 -= 0.6
		}
		if btcData.CurrentMACDHistogram15m > 0 {
			s15 += 0.4
		} else if btcData.CurrentMACDHistogram15m < 0 {
			s15 -= 0.4
		}
		s3 := 0.0
		if btcData.CurrentMACD3m > 0 {
			s3 += 0.6
		} else if btcData.CurrentMACD3m < 0 {
			s3 -= 0.6
		}
		if btcData.CurrentMACDHistogram3m > 0 {
			s3 += 0.4
		} else if btcData.CurrentMACDHistogram3m < 0 {
			s3 -= 0.4
		}
		wc := (s4*ctx.CycleWeight4h + s1*ctx.CycleWeight1h + s15*ctx.CycleWeight15m + s3*ctx.CycleWeight3m) / 100.0
		conf := (wc + 1.0) / 2.0 * 100.0
		sb.WriteString(fmt.Sprintf("**周期权重**: 4h:%.0f%% 1h:%.0f%% 15m:%.0f%% 3m:%.0f%% | BTC综合信心: %.0f/100\n\n",
			ctx.CycleWeight4h, ctx.CycleWeight1h, ctx.CycleWeight15m, ctx.CycleWeight3m, conf))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("**账户**: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 聚合当前持仓保证金盈亏百分比（sum(PnL)/sum(MarginUsed)）
	if len(ctx.Positions) > 0 {
		sumPnl := 0.0
		sumMargin := 0.0
		for _, p := range ctx.Positions {
			sumPnl += p.UnrealizedPnL
			sumMargin += p.MarginUsed
		}
		aggPct := 0.0
		if sumMargin > 0 {
			aggPct = (sumPnl / sumMargin) * 100
		}
		sb.WriteString(fmt.Sprintf("**持仓盈亏（保证金）**: %+.2f%%\n\n", aggPct))
	} else {
		sb.WriteString("\n")
	}

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%%(保证金) | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("**当前持仓**: 无\n\n")
	}

	// 候选币种（完整市场数据）
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.CandidateCoins)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用FormatMarketData输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 夏普比率（直接传值，不要复杂格式化）
	if ctx.Performance != nil {
		// 直接从interface{}中提取SharpeRatio
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	sb.WriteString("请严格按照以下结构先输出思维链分段，然后仅输出 JSON 决策数组（置于代码块围栏内）：\r\n\r\n")
	sb.WriteString("【观今日市势】\r\n")
	sb.WriteString("【维科夫周期判断】\r\n")
	sb.WriteString("【斐波那契分析】\r\n")
	sb.WriteString("【多周期一致性（3m/15m/1h/4h）】\r\n")
	sb.WriteString("【风险控制检查】\r\n")
	sb.WriteString("【持仓检查】\r\n")
	sb.WriteString("【决策】\r\n\r\n")
	sb.WriteString("多周期方向枚举已提供（3m/15m/1h/4h：up/down/flat），并含一致性计数；仅在至少三个周期方向一致时考虑开仓，并在【多周期一致性（3m/15m/1h/4h）】段落中明确列出各周期方向与一致性计数。\r\n\r\n")
	sb.WriteString("输出要求：\r\n- 仅允许动作：\"open_long\", \"open_short\", \"close_long\", \"close_short\", \"hold\", \"wait\"。\r\n- 当动作为 \"open_long\" 或 \"open_short\" 时必须给出：\r\n  - \"position_size_usd\"（正数）\r\n  - \"leverage\"（正数）\r\n  - \"stop_loss\"（同一计量单位）\r\n  - \"take_profit\"（同一计量单位）\r\n- 所有动作均需：\r\n  - \"confidence\"（0–1）\r\n  - \"risk_usd\"（美元）\r\n\r\n仅在上述思维链分段之后，输出纯 JSON 数组，不得混入任何额外文本。")

	sb.WriteString(prompt.UserPromptFooter(activePromptVariant()))

	return sb.String()
}

// activePromptVariant 返回当前启用的提示词变体
// 通过环境变量 NOFX_PROMPT_VARIANT 覆盖，默认使用 "default"
// 如果你希望在代码中强制指定某一变体，可直接修改默认值。
func activePromptVariant() string {
	if v := os.Getenv("NOFX_PROMPT_VARIANT"); v != "" {
		return v
	}
	// 默认使用项目中定义的提示词变体
	return prompt.DefaultVariant
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, minRiskRewardRatio float64) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	// 3. CoT 合规性检查（更宽松且可解释）：缺少关键段落时回退为观望
	// 允许不同写法与中英混用；仅当命中段落数低于阈值时判定为不合规
	norm := func(s string) string {
		u := strings.ToLower(strings.TrimSpace(s))
		// 移除常见括号与全角符号
		replacers := []string{"【", "】", "[", "]", "（", "）", ":", "：", "-", "—"}
		for _, r := range replacers {
			u = strings.ReplaceAll(u, r, " ")
		}
		// 压缩多余空格
		u = strings.Join(strings.Fields(u), " ")
		return u
	}
	cotN := norm(cotTrace)
	// 关键段组（任意命中其一即视为该段存在）
	groups := [][]string{
		{"观今日市势", "市势", "market overview"},
		{"维科夫", "wyckoff"},
		{"斐波那契", "fibonacci"},
		{"多周期一致性", "多周期", "multi timeframe", "multi-timeframe"},
		{"风险控制", "风控", "risk control"},
		{"持仓检查", "持仓", "position check", "positions"},
		{"决策", "decision"},
	}
	hit := 0
	missing := make([]string, 0)
	for _, g := range groups {
		found := false
		for _, key := range g {
			if strings.Contains(cotN, strings.ToLower(key)) {
				found = true
				break
			}
		}
		if found {
			hit++
		} else {
			// 使用组的第一个词作为缺失名
			missing = append(missing, g[0])
		}
	}
	if hit < 3 {
		reason := "CoT不合规，缺少: " + strings.Join(missing, ", ") + "；回退为观望"
		decisions = []Decision{{Symbol: "BTCUSDT", Action: "wait", Confidence: 0.5, Reasoning: reason}}
	}

	// 4. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage, minRiskRewardRatio); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

// ParseDecisionsForTest 对外暴露的解析函数，仅用于本地解析测试小工具
// 目的：允许在不调用外部API的情况下，直接验证AI响应字符串的解析与校验逻辑
func ParseDecisionsForTest(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, minRiskRewardRatio float64) (*FullDecision, error) {
	return parseFullDecisionResponse(aiResponse, accountEquity, btcEthLeverage, altcoinLeverage, minRiskRewardRatio)
}

// extractCoTTrace 提取思维链分析
func extractCoTTrace(response string) string {
	// 查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")

	if jsonStart > 0 {
		// 思维链是JSON数组之前的内容；若仅为代码块标记（```json等）则视为无思维链
		pre := strings.TrimSpace(response[:jsonStart])
		if pre == "" || strings.HasPrefix(pre, "```") {
			return ""
		}
		return pre
	}

	// 如果找不到JSON，整个响应都是思维链
	return strings.TrimSpace(response)
}

// extractJSONFromCodeBlock 优先提取 ``` 代码块中的内容（支持 ```json）并返回其中的JSON文本
func extractJSONFromCodeBlock(response string) (string, bool) {
	// 找到第一个代码块起始围栏
	start := strings.Index(response, "```")
	if start == -1 {
		return "", false
	}
	// 跳过语言标签行（如 ```json）到下一行
	after := response[start+3:]
	newline := strings.Index(after, "\n")
	if newline == -1 {
		return "", false
	}
	contentStart := start + 3 + newline + 1
	// 查找结束围栏
	endFence := strings.Index(response[contentStart:], "```")
	if endFence == -1 {
		return "", false
	}
	block := strings.TrimSpace(response[contentStart : contentStart+endFence])
	// 如果代码块中包含JSON数组，提取最外层的完整数组
	arrayStart := strings.Index(block, "[")
	if arrayStart != -1 {
		if arrayEnd := findMatchingBracket(block, arrayStart); arrayEnd != -1 {
			return strings.TrimSpace(block[arrayStart : arrayEnd+1]), true
		}
	}
	return block, true
}

// extractDecisions 提取JSON决策列表
func extractDecisions(response string) ([]Decision, error) {
	// 若AI响应包含代码块围栏，优先使用围栏内的内容以提升稳定性
	if block, ok := extractJSONFromCodeBlock(response); ok {
		response = block
	}

	// 直接查找JSON数组 - 找第一个完整的JSON数组（允许位于对象内部）
	arrayStart := strings.Index(response, "[")
	if arrayStart == -1 {
		// 回退：尝试解析单个JSON对象（AI可能只输出一个决策对象）
		// 优先基于包含 "action" 关键字的对象进行提取
		actionIdx := strings.Index(response, "\"action\"")
		objStart := -1
		if actionIdx != -1 {
			for i := actionIdx; i >= 0; i-- {
				if response[i] == '{' {
					objStart = i
					break
				}
			}
		}
		if objStart == -1 {
			objStart = strings.Index(response, "{")
		}
		if objStart != -1 {
			objEnd := findMatchingBrace(response, objStart)
			if objEnd != -1 {
				objContent := strings.TrimSpace(response[objStart : objEnd+1])
				objContent = fixMissingQuotes(objContent)
				objContent = normalizeChinesePunctuation(objContent)
				objContent = fixTrailingCommas(objContent)
				objContent = fixRiskUsdExpressions(objContent)
				var one Decision
				if err := json.Unmarshal([]byte(objContent), &one); err == nil {
					return []Decision{one}, nil
				}
			}
		}
		return nil, fmt.Errorf("无法找到JSON数组起始")
	}

	// 从 [ 开始，匹配括号找到对应的 ]
	arrayEnd := findMatchingBracket(response, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束")
	}

	jsonContent := strings.TrimSpace(response[arrayStart : arrayEnd+1])

	// 🔧 修复常见的JSON格式错误：缺少引号的字段值
	// 匹配: "reasoning": 内容"}  或  "reasoning": 内容}  (没有引号)
	// 修复为: "reasoning": "内容"}
	// 使用简单的字符串扫描而不是正则表达式
	jsonContent = fixMissingQuotes(jsonContent)
	jsonContent = normalizeChinesePunctuation(jsonContent)
	jsonContent = fixTrailingCommas(jsonContent)

	// 🔧 清理非法 risk_usd 表达式：若出现加减乘除或括号，替换为数字0
	jsonContent = fixRiskUsdExpressions(jsonContent)

	// 解析JSON
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		// 尝试回退解析：AI可能输出的是字符串数组而不是结构化决策
		var items []string
		if err2 := json.Unmarshal([]byte(jsonContent), &items); err2 == nil {
			// 将字符串条目转换为 "wait/观望" 类型的决策，避免前端报错
			fallback := make([]Decision, 0, len(items))
			for _, s := range items {
				symbol := inferSymbolFromText(s)
				// 使用 "wait" 行为，以便通过验证逻辑且不触发开仓
				fallback = append(fallback, Decision{
					Symbol:    symbol,
					Action:    "wait",
					Reasoning: strings.TrimSpace(s),
				})
			}
			return fallback, nil
		}
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
}

// fixMissingQuotes 替换中文引号为英文引号（避免输入法自动转换）
func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '
	return jsonStr
}

// normalizeChinesePunctuation 将常见中文标点替换为英文标点，提升JSON解析兼容性
func normalizeChinesePunctuation(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",")
	return jsonStr
}

// fixTrailingCommas 移除对象或数组结尾的多余逗号（例如 {"a":1,} 或 [1,2,]）
func fixTrailingCommas(jsonStr string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(jsonStr); i++ {
		c := jsonStr[i]
		if inString {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else {
				if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(jsonStr) && (jsonStr[j] == ' ' || jsonStr[j] == '\n' || jsonStr[j] == '\t' || jsonStr[j] == '\r') {
				j++
			}
			if j < len(jsonStr) && (jsonStr[j] == '}' || jsonStr[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// fixRiskUsdExpressions 检测并清理 risk_usd 的非法表达式，替换为合法数字（0）
// 说明：AI有时会输出诸如 35 * 10 或 (108000-107328)/107328 之类的表达式，这不是合法JSON数值。
// 为了保证解析稳定性，这里将任何包含运算符的 risk_usd 值统一替换为 0。
func fixRiskUsdExpressions(jsonStr string) string {
	// 逐次定位 "risk_usd" 键并检查其值是否包含表达式字符
	const key = "\"risk_usd\""
	i := 0
	for {
		idx := strings.Index(jsonStr[i:], key)
		if idx == -1 {
			break
		}
		// 绝对位置
		pos := i + idx
		// 从 key 之后查找冒号
		colon := strings.Index(jsonStr[pos+len(key):], ":")
		if colon == -1 {
			i = pos + len(key)
			continue
		}
		// 值起始位置（跳过 ":" 和可能的空白）
		valStart := pos + len(key) + colon + 1
		// 跳过空白
		for valStart < len(jsonStr) && (jsonStr[valStart] == ' ' || jsonStr[valStart] == '\n' || jsonStr[valStart] == '\t') {
			valStart++
		}
		// 值结束位置：直到下一个逗号或右花括号
		valEnd := valStart
		for valEnd < len(jsonStr) && jsonStr[valEnd] != ',' && jsonStr[valEnd] != '}' {
			valEnd++
		}
		// 提取值并检查是否包含表达式字符
		value := strings.TrimSpace(jsonStr[valStart:valEnd])
		if containsExpressionChars(value) {
			// 用 0 替换表达式值
			jsonStr = jsonStr[:valStart] + "0" + jsonStr[valEnd:]
			// 移动游标到替换后的末尾，避免死循环
			i = valStart + 1
			continue
		}
		// 正常情况，继续向后搜索
		i = valEnd
	}
	return jsonStr
}

// findMatchingBrace 查找匹配的右花括号
func findMatchingBrace(s string, start int) int {
	if start >= len(s) || s[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else {
				if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// containsExpressionChars 判断字符串是否包含常见的算术表达式字符
func containsExpressionChars(s string) bool {
	if s == "" {
		return false
	}
	// 如果是以引号开头，说明是字符串（非法类型，但交给json解析报错），不在此处理
	if s[0] == '"' {
		return false
	}
	// 只要包含以下任一字符，即认为不是纯数字字面量
	exprChars := "*/+-()"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.ContainsRune(exprChars, rune(c)) {
			return true
		}
		// 非数字、非小数点、非科学计数法的 e/E 也视作可疑表达式（排除合法数字之外的情况）
		if !(c >= '0' && c <= '9') && c != '.' && c != 'e' && c != 'E' {
			return true
		}
	}
	return false
}

// inferSymbolFromText 从一段文本中推断币种符号（尽量匹配主流USDT交易对）
func inferSymbolFromText(s string) string {
	if s == "" {
		return ""
	}
	// 简单按空格切分，取第一个词尝试匹配常见符号
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	t := strings.ToUpper(fields[0])
	switch t {
	case "BTC", "BTCUSDT":
		return "BTCUSDT"
	case "ETH", "ETHUSDT":
		return "ETHUSDT"
	case "SOL", "SOLUSDT":
		return "SOLUSDT"
	case "BNB", "BNBUSDT":
		return "BNBUSDT"
	case "XRP", "XRPUSDT":
		return "XRPUSDT"
	case "DOGE", "DOGEUSDT":
		return "DOGEUSDT"
	default:
		// 未识别则返回空字符串，让前端仅展示 reasoning
		return ""
	}
}

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, minRiskRewardRatio float64) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage, minRiskRewardRatio); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// findMatchingBracket 查找匹配的右括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if inString {
			if escaped {
				// 当前字符被转义，跳过并清除转义状态
				escaped = false
			} else {
				if c == '\\' {
					escaped = true
				} else if c == '"' {
					inString = false
				}
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, minRiskRewardRatio float64) error {
	// 验证action
	validActions := map[string]bool{
		"open_long":   true,
		"open_short":  true,
		"close_long":  true,
		"close_short": true,
		"hold":        true,
		"wait":        true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		rawSym := strings.TrimSpace(d.Symbol)
		normSym := market.Normalize(rawSym)
		up := strings.ToUpper(normSym)
		if rawSym == "" || up == "USDT" || up == "OKXUSDT" {
			return fmt.Errorf("无效币种: '%s' (需形如 BTCUSDT)", d.Symbol)
		}
		d.Symbol = normSym

		md, err := market.Get(normSym)
		if err != nil {
			d.Action = "wait"
			d.Reasoning = strings.TrimSpace(d.Reasoning + " | 市场数据不可用，观望")
			return nil
		}

		upCount := 0
		downCount := 0
		if md.Direction3m == "up" {
			upCount++
		} else if md.Direction3m == "down" {
			downCount++
		}
		if md.Direction15m == "up" {
			upCount++
		} else if md.Direction15m == "down" {
			downCount++
		}
		if md.Direction1h == "up" {
			upCount++
		} else if md.Direction1h == "down" {
			downCount++
		}
		if md.Direction4h == "up" {
			upCount++
		} else if md.Direction4h == "down" {
			downCount++
		}

		if d.Action == "open_long" && upCount < 3 {
			d.Action = "wait"
			d.Reasoning = strings.TrimSpace(d.Reasoning + " | 多周期一致性不足，观望")
			return nil
		}
		if d.Action == "open_short" && downCount < 3 {
			d.Action = "wait"
			d.Reasoning = strings.TrimSpace(d.Reasoning + " | 多周期一致性不足，观望")
			return nil
		}

		if md.LongerTermContext != nil && md.CurrentPrice > 0 {
			atrPct := md.LongerTermContext.ATR14 / md.CurrentPrice
			if atrPct < 0.008 || atrPct > 0.06 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 波动率不在合理区间，观望")
				return nil
			}
		}

		slopeOK := true
		if md.IntradaySeries != nil && len(md.IntradaySeries.EMA20Values) >= 6 {
			ema := md.IntradaySeries.EMA20Values
			base := ema[len(ema)-6]
			last := ema[len(ema)-1]
			if base > 0 {
				avgSlope := (last - base) / base / 5.0
				if d.Action == "open_long" && avgSlope < 0.001 {
					slopeOK = false
				}
				if d.Action == "open_short" && avgSlope > -0.001 {
					slopeOK = false
				}
			}
		}
		if !slopeOK {
			d.Action = "wait"
			d.Reasoning = strings.TrimSpace(d.Reasoning + " | EMA斜率不足，观望")
			return nil
		}

		if d.Action == "open_long" {
			if !(md.CurrentMACDHistogram15m > 0 || md.CurrentMACDHistogram1h > 0) {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 动能不足，观望")
				return nil
			}
		} else {
			if !(md.CurrentMACDHistogram15m < 0 || md.CurrentMACDHistogram1h < 0) {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 动能不足，观望")
				return nil
			}
		}

		if md.Direction4h != "" && md.Direction1h != "" && md.Direction4h != md.Direction1h {
			d.Action = "wait"
			d.Reasoning = strings.TrimSpace(d.Reasoning + " | 中长周期不一致，观望")
			return nil
		}

		if normSym != "BTCUSDT" && normSym != "ETHUSDT" {
			btc, e := market.Get("BTCUSDT")
			if e == nil {
				up := 0
				down := 0
				for _, dir := range []string{btc.Direction15m, btc.Direction1h, btc.Direction4h} {
					if dir == "up" {
						up++
					} else if dir == "down" {
						down++
					}
				}
				if d.Action == "open_long" && up < 2 {
					d.Action = "wait"
					d.Reasoning = strings.TrimSpace(d.Reasoning + " | BTC门槛未满足，观望")
					return nil
				}
				if d.Action == "open_short" && down < 2 {
					d.Action = "wait"
					d.Reasoning = strings.TrimSpace(d.Reasoning + " | BTC门槛未满足，观望")
					return nil
				}
			}
		}

		if md.BuySellPressureRatio > 0 {
			if d.Action == "open_long" && md.BuySellPressureRatio < 0.55 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 买卖压力不足，观望")
				return nil
			}
			if d.Action == "open_short" && md.BuySellPressureRatio > 0.45 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 买卖压力不符，观望")
				return nil
			}
		}

		if md.FundingRate != 0 {
			if md.FundingRate > 0.002 || md.FundingRate < -0.002 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 资金费率异常，观望")
				return nil
			}
		}

		if md.OpenInterest != nil && md.OpenInterest.Average > 0 {
			ratio := md.OpenInterest.Latest / md.OpenInterest.Average
			if d.Action == "open_long" && ratio < 1.05 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | OI增量不足，观望")
				return nil
			}
			if d.Action == "open_short" && ratio < 1.05 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | OI增量不足，观望")
				return nil
			}
		}

		if d.Action == "open_long" {
			if md.CurrentRSI15m > 70 && md.CurrentRSI1h < 60 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | RSI跨周期不利，观望")
				return nil
			}
			if md.Last15mUpperShadowToBody > 2.0 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 长上影显著，观望")
				return nil
			}
		}
		if d.Action == "open_short" {
			if md.CurrentRSI15m < 30 && md.CurrentRSI1h > 40 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | RSI跨周期不利，观望")
				return nil
			}
			if md.Last15mLowerShadowToBody > 2.0 {
				d.Action = "wait"
				d.Reasoning = strings.TrimSpace(d.Reasoning + " | 长下影显著，观望")
				return nil
			}
		}

		maxLeverage := altcoinLeverage
		maxPositionValue := accountEquity * 1.5
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage
			maxPositionValue = accountEquity * 10
		}
		if d.Leverage <= 0 {
			d.Leverage = 1
		}
		if d.Leverage > maxLeverage {
			d.Leverage = maxLeverage
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}
		tolerance := maxPositionValue * 0.02
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			d.PositionSizeUSD = maxPositionValue
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		entryPrice := md.CurrentPrice
		if entryPrice <= 0 {
			return fmt.Errorf("无效入场价(%.6f)，无法计算风险回报比", entryPrice)
		}
		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}
		if riskRewardRatio+1e-9 < minRiskRewardRatio {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥%.2f:1 [风险:%.2f%% 收益:%.2f%%] [入场:%.2f 止损:%.2f 止盈:%.2f]",
				riskRewardRatio, minRiskRewardRatio, riskPercent, rewardPercent, entryPrice, d.StopLoss, d.TakeProfit)
		}
	}

	// 平仓动作允许未指定或格式不正确的币种：在执行阶段回退根据当前持仓推断
	// 这样可以容忍AI在平仓时省略符号（例如“close_long”表示平掉当前所有多仓或唯一多仓）
	if d.Action == "close_long" || d.Action == "close_short" {
		d.Symbol = strings.TrimSpace(d.Symbol)
		// 保留容错：若为空或不合法，不在解析阶段报错，交由执行阶段推断具体币种
	}

	return nil
}
