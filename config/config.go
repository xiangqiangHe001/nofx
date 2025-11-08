package config

import (
    "encoding/json"
    "fmt"
    "log"
    "os"
    "time"
    "strings"
)

// TraderConfig 鍗曚釜trader鐨勯厤缃?
type TraderConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"` // 鏄惁鍚敤璇rader
	AIModel string `json:"ai_model"` // "qwen" or "deepseek"
    Exchange string `json:"exchange"`

	// 浜ゆ槗骞冲彴閫夋嫨锛堜簩閫変竴锛?


	// 甯佸畨閰嶇疆
	BinanceAPIKey    string `json:"binance_api_key,omitempty"`
	BinanceSecretKey string `json:"binance_secret_key,omitempty"`

	// Hyperliquid閰嶇疆
	HyperliquidPrivateKey string `json:"hyperliquid_private_key,omitempty"`
	HyperliquidWalletAddr string `json:"hyperliquid_wallet_addr,omitempty"`
	HyperliquidTestnet    bool   `json:"hyperliquid_testnet,omitempty"`

	// Aster閰嶇疆
	AsterUser       string `json:"aster_user,omitempty"`        // Aster涓婚挶鍖呭湴鍧€
	AsterSigner     string `json:"aster_signer,omitempty"`      // Aster API閽卞寘鍦板潃
	AsterPrivateKey string `json:"aster_private_key,omitempty"` // Aster API閽卞寘绉侀挜


    // OKX配置
    OKXAPIKey     string `json:"okx_api_key,omitempty"`
    OKXSecretKey  string `json:"okx_secret_key,omitempty"`
    OKXPassphrase string `json:"okx_passphrase,omitempty"`
	// AI閰嶇疆
	QwenKey     string `json:"qwen_key,omitempty"`
	DeepSeekKey string `json:"deepseek_key,omitempty"`

	// 鑷畾涔堿I API閰嶇疆锛堟敮鎸佷换浣昈penAI鏍煎紡鐨凙PI锛?
	CustomAPIURL    string `json:"custom_api_url,omitempty"`
	CustomAPIKey    string `json:"custom_api_key,omitempty"`
	CustomModelName string `json:"custom_model_name,omitempty"`

	InitialBalance      float64 `json:"initial_balance"`
	ScanIntervalMinutes int     `json:"scan_interval_minutes"`

    // 初始资金基线自动对齐与持久化（可选）
    AutoCalibrateInitialBalance bool    `json:"auto_calibrate_initial_balance,omitempty"`
    CalibrationThreshold        float64 `json:"calibration_threshold,omitempty"`
    PersistInitialBalance       bool    `json:"persist_initial_balance,omitempty"`
    InitialBalanceStateDir      string  `json:"initial_balance_state_dir,omitempty"`
}

// LeverageConfig 鏉犳潌閰嶇疆
type LeverageConfig struct {
	BTCETHLeverage  int `json:"btc_eth_leverage"` // BTC鍜孍TH鐨勬潬鏉嗗€嶆暟锛堜富璐︽埛寤鸿5-50锛屽瓙璐︽埛鈮?锛?
	AltcoinLeverage int `json:"altcoin_leverage"` // 灞卞甯佺殑鏉犳潌鍊嶆暟锛堜富璐︽埛寤鸿5-20锛屽瓙璐︽埛鈮?锛?
}

// Config 鎬婚厤缃?
type Config struct {
	Traders            []TraderConfig `json:"traders"`
	UseDefaultCoins    bool           `json:"use_default_coins"` // 鏄惁浣跨敤榛樿涓绘祦甯佺鍒楄〃
	DefaultCoins       []string       `json:"default_coins"`     // 榛樿涓绘祦甯佺姹?
	CoinPoolAPIURL     string         `json:"coin_pool_api_url"`
	OITopAPIURL        string         `json:"oi_top_api_url"`
	APIServerPort      int            `json:"api_server_port"`
	MaxDailyLoss       float64        `json:"max_daily_loss"`
	MaxDrawdown        float64        `json:"max_drawdown"`
	StopTradingMinutes int            `json:"stop_trading_minutes"`
	Leverage           LeverageConfig `json:"leverage"` // 鏉犳潌閰嶇疆
}

// LoadConfig 浠庢枃浠跺姞杞介厤缃?
func LoadConfig(filename string) (*Config, error) {
    data, err := os.ReadFile(filename)
    if err != nil {
        return nil, fmt.Errorf("璇诲彇閰嶇疆鏂囦欢澶辫触: %w", err)
    }

    var config Config
    if err := json.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("瑙ｆ瀽閰嶇疆鏂囦欢澶辫触: %w", err)
    }

    // Debug: 打印未校验前的每个 Trader 的扫描间隔
    log.Printf("[Config] Loaded file: %s, traders=%d", filename, len(config.Traders))
    // 仅打印包含关键字段的原始文本行，便于比对实际读取的配置
    for _, line := range strings.Split(string(data), "\n") {
        if strings.Contains(line, "scan_interval_minutes") || strings.Contains(line, "default_coins") {
            log.Printf("[Config] Raw line: %s", strings.TrimSpace(line))
        }
    }
    for _, t := range config.Traders {
        log.Printf("[Config] Pre-validate trader '%s' scan_interval_minutes=%d ai_model=%s exchange=%s", t.ID, t.ScanIntervalMinutes, t.AIModel, t.Exchange)
    }

	// 璁剧疆榛樿鍊硷細濡傛灉use_default_coins鏈缃紙涓篺alse锛変笖娌℃湁閰嶇疆coin_pool_api_url锛屽垯榛樿浣跨敤榛樿甯佺鍒楄〃
	if !config.UseDefaultCoins && config.CoinPoolAPIURL == "" {
		config.UseDefaultCoins = true
	}

	// 璁剧疆榛樿甯佺姹?
	if len(config.DefaultCoins) == 0 {
		config.DefaultCoins = []string{
			"BTCUSDT",
			"ETHUSDT",
			"SOLUSDT",
			"BNBUSDT",
			"XRPUSDT",
			"DOGEUSDT",
			"ADAUSDT",
			"HYPEUSDT",
		}
	}

    // 楠岃瘉閰嶇疆
    if err := config.Validate(); err != nil {
        return nil, fmt.Errorf("閰嶇疆楠岃瘉澶辫触: %w", err)
    }

    // Debug: 打印校验后的每个 Trader 的扫描间隔（若为0，会被设置为3）
    for _, t := range config.Traders {
        log.Printf("[Config] Post-validate trader '%s' scan_interval_minutes=%d (interval=%s)", t.ID, t.ScanIntervalMinutes, t.GetScanInterval())
    }

    return &config, nil
}

// Validate 楠岃瘉閰嶇疆鏈夋晥鎬?
func (c *Config) Validate() error {
    if len(c.Traders) == 0 {
        return fmt.Errorf("至少需要配置一个trader")
    }

    traderIDs := make(map[string]bool)
    for i, trader := range c.Traders {
        if trader.ID == "" {
            return fmt.Errorf("trader[%d]: ID不能为空", i)
        }
        if traderIDs[trader.ID] {
            return fmt.Errorf("trader[%d]: ID '%s' 重复", i, trader.ID)
        }
        traderIDs[trader.ID] = true

        if trader.Name == "" {
            return fmt.Errorf("trader[%d]: Name不能为空", i)
        }
        if trader.AIModel != "qwen" && trader.AIModel != "deepseek" && trader.AIModel != "custom" {
            return fmt.Errorf("trader[%d]: ai_model必须是 'qwen', 'deepseek' 或 'custom'", i)
        }

        // 仅允许 OKX 交易所
        if trader.Exchange == "" {
            trader.Exchange = "okx"
        }
        if trader.Exchange != "okx" {
            return fmt.Errorf("trader[%d]: 仅支持 OKX 交易所，请将 exchange 设置为 'okx'", i)
        }
        if trader.OKXAPIKey == "" || trader.OKXSecretKey == "" || trader.OKXPassphrase == "" {
            return fmt.Errorf("trader[%d]: 使用OKX时必须配置okx_api_key, okx_secret_key和okx_passphrase", i)
        }

        if trader.AIModel == "qwen" && trader.QwenKey == "" {
            return fmt.Errorf("trader[%d]: 使用Qwen时必须配置qwen_key", i)
        }
        if trader.AIModel == "deepseek" && trader.DeepSeekKey == "" {
            return fmt.Errorf("trader[%d]: 使用DeepSeek时必须配置deepseek_key", i)
        }
        if trader.AIModel == "custom" {
            if trader.CustomAPIURL == "" {
                return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_url", i)
            }
            if trader.CustomAPIKey == "" {
                return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_key", i)
            }
            if trader.CustomModelName == "" {
                return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_model_name", i)
            }
        }

        if trader.InitialBalance <= 0 {
            return fmt.Errorf("trader[%d]: initial_balance必须大于0", i)
        }
        if trader.ScanIntervalMinutes <= 0 {
            trader.ScanIntervalMinutes = 3
        }

        // 设置自动校准阈值默认值
        if trader.AutoCalibrateInitialBalance && trader.CalibrationThreshold <= 0 {
            trader.CalibrationThreshold = 1.0
        }
    }

    if c.APIServerPort <= 0 {
        c.APIServerPort = 8080
    }

    if c.Leverage.BTCETHLeverage <= 0 {
        c.Leverage.BTCETHLeverage = 5
    }
    if c.Leverage.AltcoinLeverage <= 0 {
        c.Leverage.AltcoinLeverage = 5
    }

    return nil
}

func (tc *TraderConfig) GetScanInterval() time.Duration {
	return time.Duration(tc.ScanIntervalMinutes) * time.Minute
}






