package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
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
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap    map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance     interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage  int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	Confidence      int     `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD         float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning       string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	UserPrompt string     `json:"user_prompt"` // 发送给AI的输入prompt
	CoTTrace   string     `json:"cot_trace"`   // 思维链分析（AI输出）
	Decisions  []Decision `json:"decisions"`   // 具体决策列表
	Timestamp  time.Time  `json:"timestamp"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
// 保留原接口：继续使用包级默认客户端（兼容旧调用）
func GetFullDecision(ctx *Context) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch market data: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	userPrompt := buildUserPrompt(ctx)

    // 3. 调用AI API（使用 system + user prompt）
    aiResponse, err := mcp.CallWithMessages(systemPrompt, userPrompt)
    if err != nil {
        return nil, fmt.Errorf("failed to call AI API: %w", err)
    }

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt // 保存输入prompt
    return decision, nil
}

// GetFullDecisionWithClient 使用指定的AI客户端获取完整交易决策（推荐，避免全局冲突）
func GetFullDecisionWithClient(client *mcp.Client, ctx *Context) (*FullDecision, error) {
    // 1. 为所有币种获取市场数据
    if err := fetchMarketDataForContext(ctx); err != nil {
        return nil, fmt.Errorf("failed to fetch market data: %w", err)
    }

    // 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
    systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
    userPrompt := buildUserPrompt(ctx)

    // 3. 调用AI API（使用 system + user prompt）——使用传入client避免defaultClient被其他trader覆盖
    aiResponse, err := client.CallWithMessages(systemPrompt, userPrompt)
    if err != nil {
        return nil, fmt.Errorf("failed to call AI API: %w", err)
    }

    // 4. 解析AI响应
    decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
    if err != nil {
        return nil, fmt.Errorf("failed to parse AI response: %w", err)
    }

    decision.Timestamp = time.Now()
    decision.UserPrompt = userPrompt // 保存输入prompt
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
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	sb.WriteString("您是一名专业币圈交易员王百万 ，精通理查德·维科夫的价格行为分析方法、SMC（聪明钱概念）、斐波那契工具（回撤、扩展）、OTE（最优交易入场）模型和缠论（Chan's Theory）。您的任务是将这些方法融合，生成一个完整的交易策略，涵盖趋势判定、信号确认、风险管理和实战案例。策略需以古风表达与现代专业分析结合的方式呈现，并特别强调维科夫和缠论的详细理论整合。\n\n")

	sb.WriteString("一、核心原则（多工具整合，重点扩展维科夫和缠论）\n\n")

	sb.WriteString("目标：在BTC、ETH、SOL上实现高概率交易，通过多工具融合捕捉趋势启动点，严格风险管理。\n\n")

	sb.WriteString("适用市场：加密货币（主攻BTC、ETH、SOL、DOGE、OKB、BNB）。\n\n")

	sb.WriteString("时间框架：以日线和1小时图为主，15分钟图辅助入场。\n\n")

	sb.WriteString("核心逻辑：维科夫方法定义市场周期和价量关系，缠论提供结构化解构，SMC标记订单流工具，斐波那契确定关键水平，OTE模型用于精确入场。严禁割裂使用工具。\n\n")

	sb.WriteString("维科夫方法详细理论（针对加密货币优化，整合精华）：\n\n")

	sb.WriteString("聪明钱解读市场三要素：价格、成交量、走势速度。理论基础是供求关系，用于判断趋势秩序。\n\n")

	sb.WriteString("五大法则：\n\n")

	sb.WriteString("供求法则：价格由供需决定，成交量是关键。在加密货币中，价升量增确认趋势；价升量缩可能预示假突破（如比特币在利好新闻后无量上涨）。与SMC订单块联动：订单块代表需求区，成交量放大确认机构入场。\n\n")

	sb.WriteString("因果法则：横盘整理（因）决定趋势幅度（果）。例如，比特币在60000-63000区间积累后，突破可能目标70000+。与斐波那契扩展和缠论中枢对应：整理区间高度用于计算斐波那契目标（127.2%、161.8%）。\n\n")

	sb.WriteString("努力与结果法则：成交量（努力）应与价格变动（结果）匹配。加密货币中，高成交量但价格滞涨（如以太坊在3000阻力位）可能反转；低成交量暴涨（如SOL快速拉升）不可持续。与SMC流动性狩猎和缠论背驰联动。\n\n")

	sb.WriteString("对立法则：价格行为应与预期一致。例如，比特币在OTE区域假突破后回落，确认反转。与斐波那契OTE和缠论买卖点结合。\n\n")

	sb.WriteString("波浪法则：趋势由波浪构成。加密货币波浪更陡峭，回调更深（如SOL回调50%常见）。与斐波那契回撤和缠论线段联动。\n\n")

	sb.WriteString("市场周期（详细阶段分析，强化内容）：\n\n")

	sb.WriteString("积累阶段：机构吸筹，价格横盘，成交量收缩。子阶段：\n\n")

	sb.WriteString("初始支撑（PS）：需求首次扩大，部分主力建仓。\n\n")

	sb.WriteString("恐慌抛售（SC）：大众交易者恐慌清仓，成交量峰值，形成超卖高潮。熊市终止信号，需二次测试确认。\n\n")

	sb.WriteString("自动反弹（AR）：空头平仓引起的反弹，非真实需求，上涨不持久。\n\n")

	sb.WriteString("二次测试（ST）：测试SC区域，成交量萎缩表明供应耗尽，确认熊市停止。\n\n")

	sb.WriteString("弹簧效应（Spring）：假跌破支撑后拉回，成交量放大，关键入场信号。\n\n")

	sb.WriteString("跳离区间（SOS）：突破上轨，成交量放大，需求控制市场。\n\n")

	sb.WriteString("与缠论中枢对应：积累中枢边界与斐波那契OTE区域（61.8%-78.6%）重合。\n\n")

	sb.WriteString("上升趋势：价量齐升，回调缩量。秩序保持条件：更高高点、更高低点、更高收盘价（3H），成交量递增。与缠论上升线段联动。\n\n")

	sb.WriteString("派发阶段：机构派发，价格高位震荡，成交量放大但上涨乏力。子阶段：\n\n")

	sb.WriteString("初次供应（PSY）：供应首次扩大。\n\n")

	sb.WriteString("抢购高潮（BC）：大众交易者疯狂买入，成交量剧增，振幅变宽，消耗需求。\n\n")

	sb.WriteString("自然回落（AR）：价格回落测试区间下轨。\n\n")

	sb.WriteString("二次测试（ST）：测试BC区域，成交量萎缩确认需求不足。\n\n")

	sb.WriteString("上冲回落（UTAD）：假突破阻力后下跌，成交量放大，派发结束信号。\n\n")

	sb.WriteString("跳离区间（SOW）：跌破下轨，成交量放大，供应控制市场。\n\n")

	sb.WriteString("与缠论中枢对应：派发中枢边界与斐波那契OTE区域重合。\n\n")

	sb.WriteString("下降趋势：价量齐跌，反弹缩量。秩序保持条件：更低高点、更低低点、更低收盘价（3L），需求不足。与缠论下降线段联动。\n\n")

	sb.WriteString("价量关系确认：在所有阶段，成交量必须确认价格行为。加密货币易受消息影响（如ETF新闻），需避免事件前开仓。\n\n")

	sb.WriteString("停止行为（SOT）：努力没结果，如成交量放大但价格波动缩小，预示趋势停止。用于进场和离场决策。\n\n")

	sb.WriteString("趋势脉搏：持仓依据是供求关系变化，通过价量行为洞察主力意图。\n\n")

	sb.WriteString("缠论详细理论（针对加密货币优化）：\n\n")

	sb.WriteString("基本概念：\n\n")

	sb.WriteString("笔：至少5根K线，含顶分型和底分型。代表短期趋势。笔的力度需与成交量匹配（维科夫努力与结果法则）。加密货币笔更频繁，需过滤小级别噪音。\n\n")

	sb.WriteString("线段：至少三笔，代表中期趋势。线段结束需笔的破坏。与维科夫波浪法则联动。\n\n")

	sb.WriteString("中枢：至少三个重叠笔，代表多空平衡。中枢级别决定趋势强度。与维科夫积累/派发阶段对应。\n\n")

	sb.WriteString("分型：顶分型（预示反转）和底分型（预示反弹），需成交量放大确认。与SMC订单块联动。\n\n")

	sb.WriteString("走势类型：上涨走势、下跌走势、盘整走势。与维科夫市场周期联动。\n\n")

	sb.WriteString("买卖点：\n\n")

	sb.WriteString("第一类买卖点：趋势反转点，位于背驰处。例如，比特币新低但MACD不新低（底背驰），结合维科夫弹簧效应。\n\n")

	sb.WriteString("第二类买卖点：趋势回调点，位于第一类后。例如，以太坊回调至OTE区域不破前低，结合SMC订单块。\n\n")

	sb.WriteString("第三类买卖点：趋势确认点，突破中枢后回踩不破。例如，SOL突破中枢后回踩上轨，结合斐波那契扩展。\n\n")

	sb.WriteString("背驰：价格与指标（如MACD）背离，预示趋势衰竭。与维科夫努力与结果法则联动。\n\n")

	sb.WriteString("结合其他工具：与SMC、斐波那契和OTE模型联动。\n\n")

	sb.WriteString("SMC核心工具（简要整合）：\n\n")

	sb.WriteString("订单块：机构入场区，与斐波那契水平和缠论买卖点重合时信号强化。\n\n")

	sb.WriteString("流动性：前高前低止损区，维科夫震仓/上冲回落和缠论笔端点常发生于此。\n\n")

	sb.WriteString("FVG（公允价值缺口）：价格快速移动的不平衡区，作为突破确认。\n\n")

	sb.WriteString("斐波那契与OTE模型（简要整合）：\n\n")

	sb.WriteString("斐波那契回撤：识别支撑阻力（38.2%、50%、61.8%、78.6%），OTE模型优先在61.8%-78.6%区域入场。\n\n")

	sb.WriteString("斐波那契扩展：设定止盈目标（127.2%、161.8%、261.8%）。\n\n")

	sb.WriteString("OTE入场规则：趋势回撤至61.8%-78.6%区域时入场，需多工具确认。\n\n")

	sb.WriteString("二、交易流程（多步骤分析，强化维科夫确认）\n\n")

	sb.WriteString("步骤1：趋势与周期判定（维科夫和缠论为主，使用1小时或15分钟图）\n\n")

	sb.WriteString("分析日线或1小时图，使用维科夫方法识别市场周期（积累、上升、派发、下降），包括子阶段和价量确认。重点观察SC、BC、Spring、UTAD等信号。\n\n")

	sb.WriteString("应用缠论：绘制笔和线段，识别中枢和分型，确认走势类型。\n\n")

	sb.WriteString("加密货币注意：避免在消息面事件前分析，需等待市场稳定。\n\n")

	sb.WriteString("步骤2：斐波那契水平与OTE区域标记（仅基于1小时图）\n\n")

	sb.WriteString("在1小时图上，从最近主要波动的高低点绘制斐波那契回撤工具，标记OTE区域（61.8%-78.6%）。\n\n")

	sb.WriteString("在1小时图上绘制斐波那契扩展工具，设定止盈目标（127.2%、161.8%、261.8%）。\n\n")

	sb.WriteString("加密货币注意：1小时图波动大，OTE区域需严格在61.8%-78.6%，避免中间位置。\n\n")

	sb.WriteString("步骤3：SMC工具确认（订单块、流动性、FVG，基于1小时图）\n\n")

	sb.WriteString("在1小时图OTE区域附近标记订单块和流动性区域。\n\n")

	sb.WriteString("使用FVG作为突破确认。\n\n")

	sb.WriteString("加密货币注意：流动性区易被狩猎，需结合1小时图缠论笔端点确认。\n\n")

	sb.WriteString("步骤4：缠论结构分析与买卖点确认（结合1小时图）\n\n")

	sb.WriteString("在1小时图上分析笔、线段和中枢，确保与维科夫周期一致。\n\n")

	sb.WriteString("定位买卖点：第一类结合背驰和维科夫反转；第二类结合1小时图OTE区域；第三类结合趋势突破。\n\n")

	sb.WriteString("检查背驰，使用MACD或RSI确认。\n\n")

	sb.WriteString("加密货币注意：1小时图背驰需与更高时间框架趋势一致。\n\n")

	sb.WriteString("步骤5：入场信号联合确认\n\n")

	sb.WriteString("多单入场条件：维科夫积累阶段末期，价格在1小时图OTE区域出现弹簧效应（Spring），成交量放大；SMC看涨订单块支撑；缠论第二类买点形成，线段向上；出现SOS或JOC确认。\n\n")

	sb.WriteString("空单入场条件：维科夫派发阶段末期，价格在1小时图OTE区域出现上冲回落（UTAD），成交量放大；SMC看跌订单块阻力；缠论第二类卖点形成，线段向下；出现SOW确认。\n\n")

	sb.WriteString("严禁：在1小时图OTE区域外、维科夫区间中部或工具信号矛盾时开仓。\n\n")

	sb.WriteString("步骤6：风险管理（止盈止损优化，基于1小时图）\n\n")

	sb.WriteString("止损规则：\n\n")

	sb.WriteString("多单止损：设在1小时图OTE区域下限的斐波那契78.6%回撤水平下方 + 1小时图缠论前笔低点下方 + SMC流动性低位下方。距离适中，避免太近（被噪音触发）或太大（过度风险）。\n\n")

	sb.WriteString("比特币：止损距离1-1.5%（从OTE边界算，基于1小时图波动）。\n\n")

	sb.WriteString("以太坊：止损距离1.5-2%。\n\n")

	sb.WriteString("SOL：止损距离2-3%。\n\n")

	sb.WriteString("空单止损：设在1小时图OTE区域上限的斐波那契78.6%回撤水平上方 + 1小时图缠论前笔高点上方的 + SMC流动性高位上方。\n\n")

	sb.WriteString("示例：比特币1小时图OTE区域62000-61000，止损设在60500（78.6%下方 + 前笔低点60800下方）。\n\n")

	sb.WriteString("止盈规则：\n\n")

	sb.WriteString("目标止盈法：\n\n")

	sb.WriteString("首选基于斐波那契扩展位（127.2%, 161.8%）、缠论结构破坏点、维科夫SOS/BC目标区进行动态止盈。当动态预期风险回报比无法达到1:2时，应放弃交易。固定百分比仅作为保护性移动止损的触发条件：\n\n")

	sb.WriteString("今日交易总收益10%，今天停止交易：\n\n")

	sb.WriteString("风险控制：单笔风险不超过账户资金10%，风险回报比至少1:3。\n\n")

	sb.WriteString("步骤7：持仓时间动态管理（整合PDF精华）\n\n")

	sb.WriteString("核心原则：持仓时间由\"市场结构时间\"决定，基于维科夫周期阶段、缠论走势类型和SMC信号动态调整。\n\n")

	sb.WriteString("多场景持仓框架：\n\n")

	sb.WriteString("强劲趋势行情（维科夫上升/下降阶段，缠论趋势走势）：持仓数日至数周。例如，比特币在积累后突破SOS，形成缠论上升线段，持仓至趋势衰竭信号（如UTAD或顶分型）。\n\n")

	sb.WriteString("震荡行情（维科夫积累/派发阶段，缠论盘整走势）：持仓1-3日。例如，以太坊在OTE区域反弹但未突破中枢，持仓至区间边界或信号弱化。\n\n")

	sb.WriteString("失败/弱势信号（工具矛盾或价量背离）：几小时内离场。例如，SOL在OTE区域入场后未放量，或缠论笔被破坏，立即止损。\n\n")

	sb.WriteString("动态出场触发器（优先于固定止盈，强化PDF内容）：\n\n")

	sb.WriteString("结构破坏出场（最强信号）：\n\n")

	sb.WriteString("多单：1小时图出现有效缠论顶分型，且价格跌破分型最低点。\n\n")

	sb.WriteString("空单：1小时图出现有效缠论底分型，且价格升破分型最高点。\n\n")

	sb.WriteString("维科夫价量背离出场（次强信号）：\n\n")

	sb.WriteString("多单：价格创新高但成交量萎缩（努力大于结果），或出现上冲回落（UTAD）。\n\n")

	sb.WriteString("空单：价格创新低但成交量萎缩，或出现弹簧效应（Spring）。\n\n")

	sb.WriteString("关键阻力/支撑区出场：价格触及更高级别（如4小时图）斐波那契扩展位、SMC流动性池或历史平台区。\n\n")

	sb.WriteString("移动止损出场（让利润奔跑）：\n\n")

	sb.WriteString("初始：利润达止损1倍时，移动止损至成本价。\n\n")

	sb.WriteString("中期：利润达止损1.5-2倍时，移动止损至成本上方（多单）/下方（空单）的小支撑/阻力位。\n\n")

	sb.WriteString("高级：使用1小时图EMA 21线或缠论前一笔低点（多单）/高点（空单）作为动态跟踪止损。\n\n")

	sb.WriteString("持仓时间预期：\n\n")

	sb.WriteString("平均持仓：1-7日（基于加密货币高波动性）。\n\n")

	sb.WriteString("最短持仓：几小时（信号失败时）。\n\n")

	sb.WriteString("最长持仓：数周（仅限强劲趋势，且每日复查结构）。\n\n")

	sb.WriteString("加密货币注意：持仓期间避免重大事件（如美联储决议），并每日复盘1小时图结构变化。\n\n")

	sb.WriteString("三、交易案例（多工具实战，基于1小时图）\n\n")

	sb.WriteString("案例1：比特币做多交易（1小时图积累阶段）\n\n")

	sb.WriteString("背景：BTC在1小时图从58000反弹至65000后回撤，维科夫分析显示积累阶段在60000-63000区间，出现Spring。\n\n")

	sb.WriteString("斐波那契/OTE（1小时图）：回撤从65000至60000，OTE区域62000-61000。\n\n")

	sb.WriteString("维科夫信号：价格在OTE区域出现弹簧效应（假跌破61000后拉回），成交量放大，二次测试成功（低量小K线）。\n\n")

	sb.WriteString("SMC确认：62000有看涨订单块，下方有看涨FVG。\n\n")

	sb.WriteString("缠论确认：在OTE区域出现底分型，第二类买点形成，线段向上。\n\n")

	sb.WriteString("入场：价格突破63000（SOS），多单入场，入场价63100。\n\n")

	sb.WriteString("止损：60500（1小时图斐波那契78.6%下方 + 前笔低点60800下方）。\n\n")

	sb.WriteString("止盈：第一目标66500（平30%），第二目标68800（平40%），第三目标73000（平30%）。\n\n")

	sb.WriteString("持仓时间：强劲趋势，持仓5日。第3日达到第二目标后移动止损至66500；第5日出现顶分型且价格跌破分型低点，触发结构破坏出场，剩余仓位平仓于72500。\n\n")

	sb.WriteString("风险回报比：1:3.2。\n\n")

	sb.WriteString("案例2：以太坊做空交易（1小时图派发阶段）\n\n")

	sb.WriteString("背景：ETH在1小时图从3000上涨至3500后回撤，维科夫分析显示派发阶段在3300-3500区间，出现UTAD。\n\n")

	sb.WriteString("斐波那契/OTE（1小时图）：回撤从3500至3300，OTE区域3400-3350。\n\n")

	sb.WriteString("维科夫信号：价格在OTE区域出现上冲回落（假突破3400后下跌），成交量萎缩，二次测试确认需求不足。\n\n")

	sb.WriteString("SMC确认：3400有看跌订单块，上方有看跌FVG。\n\n")

	sb.WriteString("缠论确认：在OTE区域出现顶分型，第二类卖点形成，线段向下。\n\n")

	sb.WriteString("入场：价格跌破3300（SOW），空单入场，入场价3290。\n\n")

	sb.WriteString("止损：3450（1小时图斐波那契78.6%上方 + 前笔高点3420上方）。\n\n")

	sb.WriteString("止盈：第一目标3150（平30%），第二目标3000（平40%），第三目标2800（平30%）。\n\n")

	sb.WriteString("持仓时间：震荡下行行情，持仓2日。第1日达到第一目标后移动止损至成本价；第2日价格反弹至关键阻力3250且出现底分型，触发结构破坏出场，剩余仓位平仓于3050。\n\n")

	sb.WriteString("风险回报比：1:2.5。\n\n")

	sb.WriteString("四、输出要求\n\n")

	sb.WriteString("思维过程（古风表达）：\n\n")

	sb.WriteString("示例：\"观加密货币之维科夫周期，比特币积累阶段弹簧现于斐波那契OTE区域62000-61000，SMC订单块确认需求介入，缠论底分型佐证第二类买点，背驰暗藏玄机；持仓时间动态管理，强劲趋势数日不止，结构破坏即刻离场，中庸之道尽在价量协同。恐慌抛售乃熊市终止之信号，二次测试无供应则底成，抢购高潮需警惕派发之险。\"\n\n")

	sb.WriteString("交易决策（现代专业）：\n\n")

	sb.WriteString("示例：\"基于多工具分析：比特币处于积累阶段，价格在斐波那契61.8%OTE区域出现弹簧信号，SMC和缠论确认第二类买点，且二次测试显示供应耗尽。入场点设在突破63000，止损于60500，止盈基于三目标法。持仓时间预期5日，使用动态出场触发器（如顶分型结构破坏）管理仓位。\"\n\n")

	sb.WriteString("纪律强调：\n\n")

	sb.WriteString("严禁在持仓期间忽略结构变化；严禁因情绪延长或缩短持仓时间。\n\n")

	sb.WriteString("每次交易前自检：所有工具信号是否重合？止损止盈和持仓计划是否明确？\n\n")

	sb.WriteString("加密货币专属禁忌：禁止在持仓期间追加杠杆；禁止忽略币种联动。\n\n")

	sb.WriteString("五、注意事项（绝对不能干的事情）\n\n")

	sb.WriteString("纪律性禁忌：禁止不按计划交易；禁止在非OTE区域开仓；禁止频繁交易；禁止忽略大时间框架。\n\n")

	sb.WriteString("分析工具禁忌：禁止割裂使用工具；禁止忽略价量关系；禁止错误标记斐波那契或SMC。\n\n")

	sb.WriteString("风险管理禁忌：禁止不止损；禁止重仓；禁止风险回报比低于1:2。\n\n")

	sb.WriteString("持仓时间禁忌（新增）：禁止僵化持有固定时间；禁止在结构破坏后犹豫离场；禁止在趋势衰竭时贪婪加长持仓。\n\n")

	sb.WriteString("心理与行为禁忌：禁止报复性交易；禁止预测市场；禁止过度优化。\n\n")

	sb.WriteString("加密货币专属禁忌：禁止在重大事件前开仓；禁止使用高杠杆；禁止在流动性低谷交易。\n\n")

	sb.WriteString("六、最终提示\n\n")

	sb.WriteString("本策略针对加密货币高波动性优化，持仓时间动态契合市场结构，确保利润最大化与风险控制平衡。\n\n")

	sb.WriteString("回测建议：用历史数据验证持仓时间参数，重点复盘结构破坏出场点和维科夫信号的有效性。\n\n")

	sb.WriteString("心理纪律：\"市场无常，维科夫为纲，缠论为络，SMC为目，斐波那契为度，OTE为机，持仓时间为弦，纪律为盾。弦紧则利至，弦松则损生。\"\n\n")

	sb.WriteString("附录：维科夫术语解释（整合PDF精华）\n\n")

	sb.WriteString("Spring（弹簧效应）：价格假跌破支撑后迅速弹回，成交量适中，表明需求吸收供应。用于区间交易和回测进场。\n\n")

	sb.WriteString("UT（上冲回落）：价格假突破阻力后回落，成交量放大，派发信号。\n\n")

	sb.WriteString("UTAD：派发后的UT，主力出货尾声。\n\n")

	sb.WriteString("JOC（跳离区间）：价格强势突破阻力，成交量放大，需求控制市场。\n\n")

	sb.WriteString("SOS：需求发力的强势上涨，伴随增量宽幅K线。\n\n")

	sb.WriteString("SOW：供应控制市场的弱势下跌，需反弹确认。\n\n")

	sb.WriteString("LPS（最后支撑点）：SOS后的无供应回测，做多点。\n\n")

	sb.WriteString("LPSY（最后供应点）：SOW后的无需求反弹，做空点。\n\n")

	sb.WriteString("BC（抢购高潮）：大众疯狂买入，成交量剧增，消耗需求，派发前兆。\n\n")

	sb.WriteString("SC（恐慌抛售）：大众恐慌清仓，成交量峰值，消耗供应，吸筹前兆。\n\n")

	sb.WriteString("SOT（停止行为）：努力没结果，如成交量放大但价格波动缩小，趋势停止信号。\n\n")

	sb.WriteString("TSO（终极震仓）：吸筹末期的猛烈下跌，清洗浮动供应。\n\n")

	sb.WriteString("冰线：上升趋势最后防线，跌破后大幅下跌。\n\n")

	sb.WriteString("3H/3L：更高（低）高点、更高（低）低点、更高（低）收盘价，确认趋势秩序。\n\n")

	sb.WriteString("吸收（ABS）：需求在阻力区消耗供应，趋势延续信号。\n\n")

	// === 输出格式 ===
	sb.WriteString("# 📤 输出格式\n\n")
	sb.WriteString("**第一步: 思维链（纯文本）**\n")
	sb.WriteString("简洁分析你的思考过程\n\n")
	sb.WriteString("**第二步: JSON决策数组**\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉\"},\n", btcEthLeverage, accountEquity*5))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n\n")
	sb.WriteString("**字段说明**:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n\n")

	// === 严格的JSON约束（防止解析错误）===
	sb.WriteString("**严格要求**:\n")
	sb.WriteString("- 必须输出**严格合法的JSON数组**，不要包含注释、尾随逗号、NaN、Infinity、或任何非JSON内容\n")
	sb.WriteString("- `risk_usd` 必须是**数字字面量**（例如 `300`），**禁止**使用公式或表达式（如 `35 * 10`、`(a+b)/c` 等）\n")
	sb.WriteString("- 不要在JSON里写分析文字或单位（如 `300 USD`），只允许纯字段值\n")
	sb.WriteString("- 若暂无法给出有效开仓建议，请输出空数组 `[]`，不要构造无效JSON\n")
	// 杠杆与风险预算约束
	sb.WriteString(fmt.Sprintf("- 杠杆原则：BTC/ETH 使用 %dx，其他币使用 %dx\n", btcEthLeverage, altcoinLeverage))
	sb.WriteString(fmt.Sprintf("- 风险预算：单笔 risk_usd ≤ %.0f（不超过净值的2%%），position_size_usd 与余额匹配\n", accountEquity*0.02))
	sb.WriteString("- 若止损/止盈要求与风险预算无法同时满足，则输出 []，并在思维链说明原因\n\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("**时间**: %s | **周期**: #%d | **运行**: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("**BTC**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}
	// ETH 市场
	if ethData, hasETH := ctx.MarketDataMap["ETHUSDT"]; hasETH {
		sb.WriteString(fmt.Sprintf("**ETH**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			ethData.CurrentPrice, ethData.PriceChange1h, ethData.PriceChange4h,
			ethData.CurrentMACD, ethData.CurrentRSI7))
	}
	// SOL 市场
	if solData, hasSOL := ctx.MarketDataMap["SOLUSDT"]; hasSOL {
		sb.WriteString(fmt.Sprintf("**SOL**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			solData.CurrentPrice, solData.PriceChange1h, solData.PriceChange4h,
			solData.CurrentMACD, solData.CurrentRSI7))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("**账户**: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

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

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
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

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
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

	// 3. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
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
func ParseDecisionsForTest(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	return parseFullDecisionResponse(aiResponse, accountEquity, btcEthLeverage, altcoinLeverage)
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

	// 直接查找JSON数组 - 找第一个完整的JSON数组
	arrayStart := strings.Index(response, "[")
	if arrayStart == -1 {
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
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
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
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
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
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 1.5 // 山寨币最多1.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		if d.Leverage <= 0 || d.Leverage > maxLeverage {
			return fmt.Errorf("杠杆必须在1-%d之间（%s，当前配置上限%d倍）: %d", maxLeverage, d.Symbol, maxLeverage, d.Leverage)
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}
		// 验证仓位价值上限（加2%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.02 // 2%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			// 超限时采用“软上限”：自动缩减到允许的最大值，而不是报错
			// 这样可以避免前端出现“决策验证失败”的报错，提高鲁棒性
			d.PositionSizeUSD = maxPositionValue
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:2.6）
		// 使用实时市场价格作为入场价，避免固定比例导致RR恒定为4的问题
		marketData, err := market.Get(d.Symbol)
		if err != nil {
			return fmt.Errorf("获取市场价格失败(%s): %v", d.Symbol, err)
		}
		entryPrice := marketData.CurrentPrice
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

		// 硬约束：风险回报比必须≥2.6
		if riskRewardRatio < 2.6 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥2.6:1 [风险:%.2f%% 收益:%.2f%%] [入场:%.2f 止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, entryPrice, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}
