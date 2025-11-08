package trader

import (
    "strconv"
    "strings"
    "time"
)

// syncInvestmentsFromExchange 检测并同步交易所的入金/出金到投资调整
func (at *AutoTrader) syncInvestmentsFromExchange() {
    // 前置条件：非DryRun、是OKX、节流（5分钟）
    if at.config.DryRun { return }
    // 仅在开启“自动基线对齐/入金校准”时才进行资金同步
    if !at.config.AutoCalibrateInitialBalance { return }
    if strings.ToLower(at.exchange) != "okx" { return }
    if time.Since(at.lastInvestmentSync) < 5*time.Minute { return }

    okx, ok := at.trader.(*OKXTrader)
    if !ok { return }

    deposits, errD := okx.GetAssetDepositHistory(100)
    withdrawals, errW := okx.GetAssetWithdrawalHistory(100)
    if errD != nil && errW != nil {
        return
    }

    // 仅同步“启动/重置之后”的资金记录，避免历史记录影响基线
    baselineAfter := at.startTime
    if at.lastResetTime.After(baselineAfter) {
        baselineAfter = at.lastResetTime
    }

    // 现有记录去重（基于 Note）
    existing := make(map[string]bool)
    for _, adj := range at.investmentAdjustments {
        if strings.HasPrefix(adj.Note, "okx_deposit:") || strings.HasPrefix(adj.Note, "okx_withdrawal:") {
            existing[adj.Note] = true
        }
    }

    // 入金 -> 正调整
    if errD == nil {
        for _, d := range deposits {
            txid, _ := d["tx_id"].(string)
            state, _ := d["state"].(string)
            if state != "2" && !strings.EqualFold(state, "success") { continue }
            note := "okx_deposit:" + txid
            var tsVal string
            if ts, _ := d["ts"].(string); ts != "" { tsVal = ts }
            if txid == "" && tsVal != "" { note = "okx_deposit:" + tsVal }
            // 时间过滤（以毫秒为单位）
            if tsVal != "" {
                if ms, err := strconv.ParseInt(tsVal, 10, 64); err == nil {
                    t := time.UnixMilli(ms)
                    if t.Before(baselineAfter) { continue }
                }
            }
            if existing[note] { continue }
            amt, _ := d["amount"].(float64)
            if amt <= 0 { continue }
            at.investmentAdjustments = append(at.investmentAdjustments, InvestmentAdjustment{
                Amount:    amt,
                Timestamp: time.Now(),
                Note:      note,
            })
        }
    }

    // 出金 -> 负调整
    if errW == nil {
        for _, w := range withdrawals {
            txid, _ := w["tx_id"].(string)
            state, _ := w["state"].(string)
            if state != "2" && !strings.EqualFold(state, "success") { continue }
            note := "okx_withdrawal:" + txid
            var tsVal string
            if ts, _ := w["ts"].(string); ts != "" { tsVal = ts }
            if txid == "" && tsVal != "" { note = "okx_withdrawal:" + tsVal }
            // 时间过滤（以毫秒为单位）
            if tsVal != "" {
                if ms, err := strconv.ParseInt(tsVal, 10, 64); err == nil {
                    t := time.UnixMilli(ms)
                    if t.Before(baselineAfter) { continue }
                }
            }
            if existing[note] { continue }
            amt, _ := w["amount"].(float64)
            if amt <= 0 { continue }
            at.investmentAdjustments = append(at.investmentAdjustments, InvestmentAdjustment{
                Amount:    -amt,
                Timestamp: time.Now(),
                Note:      note,
            })
        }
    }

    // 持久化与节流更新时间
    _ = at.saveInvestmentAdjustmentsToFile()
    at.lastInvestmentSync = time.Now()
}