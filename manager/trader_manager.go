package manager

import (
	"fmt"
	"log"
	"nofx/config"
	"nofx/trader"
	"sync"
	"time"
)

// TraderManager 绠＄悊澶氫釜trader瀹炰緥
type TraderManager struct {
    traders map[string]*trader.AutoTrader // key: trader ID
    mu      sync.RWMutex
}

// NewTraderManager 鍒涘缓trader绠＄悊鍣?
func NewTraderManager() *TraderManager {
	return &TraderManager{
		traders: make(map[string]*trader.AutoTrader),
	}
}

// AddTrader 娣诲姞涓€涓猼rader
func (tm *TraderManager) AddTrader(cfg config.TraderConfig, coinPoolURL string, maxDailyLoss, maxDrawdown float64, stopTradingMinutes int, leverage config.LeverageConfig) error {
    tm.mu.Lock()
    defer tm.mu.Unlock()

    if _, exists := tm.traders[cfg.ID]; exists {
        return fmt.Errorf("trader ID '%s' 已存在", cfg.ID)
    }

	// 鏋勫缓AutoTraderConfig
	traderConfig := trader.AutoTraderConfig{
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		AIModel:               cfg.AIModel,
		Exchange:              cfg.Exchange,
		BinanceAPIKey:         cfg.BinanceAPIKey,
		BinanceSecretKey:      cfg.BinanceSecretKey,
		HyperliquidPrivateKey: cfg.HyperliquidPrivateKey,
		HyperliquidWalletAddr: cfg.HyperliquidWalletAddr,
		HyperliquidTestnet:    cfg.HyperliquidTestnet,
		AsterUser:             cfg.AsterUser,
		AsterSigner:           cfg.AsterSigner,
		AsterPrivateKey:       cfg.AsterPrivateKey,
		CoinPoolAPIURL:        coinPoolURL,
        OKXAPIKey:           cfg.OKXAPIKey,
        OKXSecretKey:        cfg.OKXSecretKey,
        OKXPassphrase:       cfg.OKXPassphrase,
		UseQwen:               cfg.AIModel == "qwen",
		DeepSeekKey:           cfg.DeepSeekKey,
		QwenKey:               cfg.QwenKey,
		CustomAPIURL:          cfg.CustomAPIURL,
		CustomAPIKey:          cfg.CustomAPIKey,
		CustomModelName:       cfg.CustomModelName,
		ScanInterval:          cfg.GetScanInterval(),
		InitialBalance:        cfg.InitialBalance,
        ExtraInvestment:        cfg.ExtraInvestment,
        AutoCalibrateInitialBalance: cfg.AutoCalibrateInitialBalance,
        CalibrationThreshold:        cfg.CalibrationThreshold,
        PersistInitialBalance:       cfg.PersistInitialBalance,
        InitialBalanceStateDir:      cfg.InitialBalanceStateDir,
		BTCETHLeverage:        leverage.BTCETHLeverage,  // 浣跨敤閰嶇疆鐨勬潬鏉嗗€嶆暟
		AltcoinLeverage:       leverage.AltcoinLeverage, // 浣跨敤閰嶇疆鐨勬潬鏉嗗€嶆暟
		MaxDailyLoss:          maxDailyLoss,
		MaxDrawdown:           maxDrawdown,
		StopTradingTime:       time.Duration(stopTradingMinutes) * time.Minute,
	}

    // Debug: 打印当前 trader 的扫描间隔配置与换算后的值
    log.Printf("[Manager] AddTrader '%s' (%s): scan_interval_minutes=%d -> interval=%s", cfg.ID, cfg.Name, cfg.ScanIntervalMinutes, cfg.GetScanInterval())

    // 鍒涘缓trader瀹炰緥
    at, err := trader.NewAutoTrader(traderConfig)
    if err != nil {
        return fmt.Errorf("鍒涘缓trader澶辫触: %w", err)
    }

    tm.traders[cfg.ID] = at
    log.Printf("✅ Trader '%s' (%s) 已添加", cfg.Name, cfg.AIModel)
    return nil
}

// GetTrader 鑾峰彇鎸囧畾ID鐨則rader
func (tm *TraderManager) GetTrader(id string) (*trader.AutoTrader, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

    t, exists := tm.traders[id]
    if !exists {
        return nil, fmt.Errorf("trader ID '%s' 不存在", id)
    }
    return t, nil
}

// GetAllTraders 鑾峰彇鎵€鏈塼rader
func (tm *TraderManager) GetAllTraders() map[string]*trader.AutoTrader {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make(map[string]*trader.AutoTrader)
	for id, t := range tm.traders {
		result[id] = t
	}
	return result
}

// GetTraderIDs 鑾峰彇鎵€鏈塼rader ID鍒楄〃
func (tm *TraderManager) GetTraderIDs() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	ids := make([]string, 0, len(tm.traders))
	for id := range tm.traders {
		ids = append(ids, id)
	}
	return ids
}

// StartAll 鍚姩鎵€鏈塼rader
func (tm *TraderManager) StartAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	log.Println("馃殌 鍚姩鎵€鏈塗rader...")
	for id, t := range tm.traders {
		go func(traderID string, at *trader.AutoTrader) {
			log.Printf("鈻讹笍  鍚姩 %s...", at.GetName())
			if err := at.Run(); err != nil {
				log.Printf("鉂?%s 杩愯閿欒: %v", at.GetName(), err)
			}
		}(id, t)
	}
}

// StopAll 鍋滄鎵€鏈塼rader
func (tm *TraderManager) StopAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	log.Println("鈴? 鍋滄鎵€鏈塗rader...")
	for _, t := range tm.traders {
		t.Stop()
	}
}

// GetComparisonData 鑾峰彇瀵规瘮鏁版嵁
func (tm *TraderManager) GetComparisonData() (map[string]interface{}, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	comparison := make(map[string]interface{})
	traders := make([]map[string]interface{}, 0, len(tm.traders))

	for _, t := range tm.traders {
		account, err := t.GetAccountInfo()
		if err != nil {
			continue
		}

		status := t.GetStatus()

		traders = append(traders, map[string]interface{}{
			"trader_id":       t.GetID(),
			"trader_name":     t.GetName(),
			"ai_model":        t.GetAIModel(),
			"total_equity":    account["total_equity"],
			"total_pnl":       account["total_pnl"],
			"total_pnl_pct":   account["total_pnl_pct"],
			"position_count":  account["position_count"],
			"margin_used_pct": account["margin_used_pct"],
			"call_count":      status["call_count"],
			"is_running":      status["is_running"],
		})
	}

	comparison["traders"] = traders
	comparison["count"] = len(traders)

	return comparison, nil
}

// CloseAllPositions 清空所有Trader的所有持仓
// 返回每个trader的平仓数量与错误信息
func (tm *TraderManager) CloseAllPositions() map[string]interface{} {
    tm.mu.RLock()
    defer tm.mu.RUnlock()

    result := make(map[string]interface{})
    for id, t := range tm.traders {
        count, err := t.CloseAllPositions()
        entry := map[string]interface{}{
            "closed_count": count,
        }
        if err != nil {
            entry["error"] = err.Error()
        } else {
            entry["success"] = true
        }
        result[id] = entry
    }
    return result
}

// RunOnceAll 为所有Trader执行一次AI决策周期
func (tm *TraderManager) RunOnceAll() map[string]interface{} {
    tm.mu.RLock()
    defer tm.mu.RUnlock()

    result := make(map[string]interface{})
    for id, t := range tm.traders {
        err := t.RunOnce()
        entry := map[string]interface{}{}
        if err != nil {
            entry["error"] = err.Error()
        } else {
            entry["success"] = true
        }
        result[id] = entry
    }
    return result
}

// RunAiCloseThenOpenAll 让所有Trader执行：AI平仓阶段 -> AI开仓阶段
func (tm *TraderManager) RunAiCloseThenOpenAll() map[string]interface{} {
    tm.mu.RLock()
    defer tm.mu.RUnlock()

    result := make(map[string]interface{})
    for id, t := range tm.traders {
        res, err := t.RunAiCloseThenOpen()
        entry := map[string]interface{}{
            "result": res,
        }
        if err != nil {
            entry["error"] = err.Error()
        } else {
            entry["success"] = true
        }
        result[id] = entry
    }
    return result
}
