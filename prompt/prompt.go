package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
    DefaultVariant = "zhugefan"
    systemPrefix   = "prompt/system_"
    userPrefix     = "prompt/user_"
)

// RenderSystemPrompt 加载指定变体的系统提示词，并填充必要的动态占位符
// 支持占位符：
// - {{POSITION_LIMITS}} 由账户净值与杠杆计算出的仓位限制行
// - {{LEVERAGE_BTC_ETH}} 用于示例 JSON 中 BTC/ETH 的杠杆数字
// - {{POSITION_SIZE_BTC_SAMPLE}} 用于示例 JSON 中 BTC 头寸大小示例
// - {{MIN_RISK_REWARD}} 最小风险回报比
// - {{MAX_MARGIN_USAGE_PCT}} 保证金总使用率上限百分比
func RenderSystemPrompt(variant string, accountEquity float64, btcEthLeverage, altcoinLeverage int, minRiskReward float64, maxMarginUsagePct float64) string {
    content := readFileSafe(systemFile(variant))
    if content == "" {
        content = defaultSystemStub(minRiskReward, maxMarginUsagePct)
    }

    // 计算动态占位符
    positionLimits := fmt.Sprintf("3. **单币仓位**: 山寨%.0f-%.0f U(%dx杠杆) | BTC/ETH %.0f-%.0f U(%dx杠杆)",
        accountEquity*0.8, accountEquity*1.5, altcoinLeverage, accountEquity*5, accountEquity*10, btcEthLeverage)

    content = strings.ReplaceAll(content, "{{POSITION_LIMITS}}", positionLimits)
    content = strings.ReplaceAll(content, "{{LEVERAGE_BTC_ETH}}", strconv.Itoa(btcEthLeverage))
    content = strings.ReplaceAll(content, "{{LEVERAGE_ALTCOIN}}", strconv.Itoa(altcoinLeverage))
    content = strings.ReplaceAll(content, "{{POSITION_SIZE_BTC_SAMPLE}}", fmt.Sprintf("%.0f", accountEquity*5))
    // 支持动态最小风险回报比占位
    content = strings.ReplaceAll(content, "{{MIN_RISK_REWARD}}", fmt.Sprintf("%.2f", minRiskReward))
    // 支持保证金使用率上限占位
    if maxMarginUsagePct <= 0 {
        maxMarginUsagePct = 60.0
    }
    content = strings.ReplaceAll(content, "{{MAX_MARGIN_USAGE_PCT}}", fmt.Sprintf("%.0f", maxMarginUsagePct))

    return content
}

// UserPromptFooter 加载用户提示词尾部文案（例如下达输出格式的指令）
func UserPromptFooter(variant string) string {
	content := readFileSafe(userFile(variant))
	if content == "" {
		return defaultUserFooter()
	}
	return content
}

func systemFile(variant string) string {
	if variant == "" {
		variant = DefaultVariant
	}
	return filepath.FromSlash(systemPrefix + variant + ".txt")
}

func userFile(variant string) string {
	if variant == "" {
		variant = DefaultVariant
	}
	return filepath.FromSlash(userPrefix + variant + ".txt")
}

func readFileSafe(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// 当找不到变体文件时的最小系统提示词占位
func defaultSystemStub(minRiskReward float64, maxMarginUsagePct float64) string {
    if maxMarginUsagePct <= 0 {
        maxMarginUsagePct = 60.0
    }
    return fmt.Sprintf("你是专业的加密货币交易AI，目标是最大化夏普比率。\n"+
        "# ⚖️ 硬约束（风险控制）\n"+
        "1. 风险回报比 ≥ 1:%.2f\n2. 最多持仓 3 个币种\n"+
        "{{POSITION_LIMITS}}\n4. 保证金总使用率 ≤ %.0f%%\n\n"+
        "# 📤 输出格式\n先给出你的思维链分析，再输出 JSON 决策数组。\n", minRiskReward)
}

func defaultUserFooter() string {
	return "---\n现在请分析并输出决策（思维链 + JSON）\n"
}
