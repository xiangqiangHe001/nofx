package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// TraderConfig 鍗曚釜trader鐨勯厤缃?
type TraderConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"` // 鏄惁鍚敤璇rader
	AIModel string `json:"ai_model"` // "qwen" or "deepseek"

	// 浜ゆ槗骞冲彴閫夋嫨锛堜簩閫変竴锛?
	Exchange string `json:"exchange"` // "binance" or "hyperliquid"

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

	// AI閰嶇疆
	QwenKey     string `json:"qwen_key,omitempty"`
	DeepSeekKey string `json:"deepseek_key,omitempty"`

	// 鑷畾涔堿I API閰嶇疆锛堟敮鎸佷换浣昈penAI鏍煎紡鐨凙PI锛?
	CustomAPIURL    string `json:"custom_api_url,omitempty"`
	CustomAPIKey    string `json:"custom_api_key,omitempty"`
	CustomModelName string `json:"custom_model_name,omitempty"`

	InitialBalance      float64 `json:"initial_balance"`
	ScanIntervalMinutes int     `json:"scan_interval_minutes"`
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

	return &config, nil
}

// Validate 楠岃瘉閰嶇疆鏈夋晥鎬?
func (c *Config) Validate() error {
	if len(c.Traders) == 0 {
		return fmt.Errorf("鑷冲皯闇€瑕侀厤缃竴涓猼rader")
	}

	traderIDs := make(map[string]bool)
	for i, trader := range c.Traders {
		if trader.ID == "" {
			return fmt.Errorf("trader[%d]: ID涓嶈兘涓虹┖", i)
		}
		if traderIDs[trader.ID] {
			return fmt.Errorf("trader[%d]: ID '%s' 閲嶅", i, trader.ID)
		}
		traderIDs[trader.ID] = true

		if trader.Name == "" {
			return fmt.Errorf("trader[%d]: Name涓嶈兘涓虹┖", i)
		}
		if trader.AIModel != "qwen" && trader.AIModel != "deepseek" && trader.AIModel != "custom" {
			return fmt.Errorf("trader[%d]: ai_model蹇呴』鏄?'qwen', 'deepseek' 鎴?'custom'", i)
		}

		// 楠岃瘉浜ゆ槗骞冲彴閰嶇疆
		if trader.Exchange == "" {
			trader.Exchange = "binance" // 榛樿浣跨敤甯佸畨
		}
		if trader.Exchange != "binance" && trader.Exchange != "hyperliquid" && trader.Exchange != "aster" {
			return fmt.Errorf("trader[%d]: exchange蹇呴』鏄?'binance', 'hyperliquid' 鎴?'aster'", i)
		}

		// 鏍规嵁骞冲彴楠岃瘉瀵瑰簲鐨勫瘑閽?
		if trader.Exchange == "binance" {
			if trader.BinanceAPIKey == "" || trader.BinanceSecretKey == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤甯佸畨鏃跺繀椤婚厤缃産inance_api_key鍜宐inance_secret_key", i)
			}
		} else if trader.Exchange == "hyperliquid" {
			if trader.HyperliquidPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤Hyperliquid鏃跺繀椤婚厤缃甴yperliquid_private_key", i)
			}
		} else if trader.Exchange == "aster" {
			if trader.AsterUser == "" || trader.AsterSigner == "" || trader.AsterPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤Aster鏃跺繀椤婚厤缃產ster_user, aster_signer鍜宎ster_private_key", i)
			}
		}

		if trader.AIModel == "qwen" && trader.QwenKey == "" {
			return fmt.Errorf("trader[%d]: 浣跨敤Qwen鏃跺繀椤婚厤缃畄wen_key", i)
		}
		if trader.AIModel == "deepseek" && trader.DeepSeekKey == "" {
			return fmt.Errorf("trader[%d]: 浣跨敤DeepSeek鏃跺繀椤婚厤缃甦eepseek_key", i)
		}
		if trader.AIModel == "custom" {
			if trader.CustomAPIURL == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤鑷畾涔堿PI鏃跺繀椤婚厤缃甤ustom_api_url", i)
			}
			if trader.CustomAPIKey == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤鑷畾涔堿PI鏃跺繀椤婚厤缃甤ustom_api_key", i)
			}
			if trader.CustomModelName == "" {
				return fmt.Errorf("trader[%d]: 浣跨敤鑷畾涔堿PI鏃跺繀椤婚厤缃甤ustom_model_name", i)
			}
		}
		if trader.InitialBalance <= 0 {
			return fmt.Errorf("trader[%d]: initial_balance蹇呴』澶т簬0", i)
		}
		if trader.ScanIntervalMinutes <= 0 {
			trader.ScanIntervalMinutes = 3 // 榛樿3鍒嗛挓
		}
	}

	if c.APIServerPort <= 0 {
		c.APIServerPort = 8080 // 榛樿8080绔彛
	}

	// 璁剧疆鏉犳潌榛樿鍊硷紙閫傞厤甯佸畨瀛愯处鎴烽檺鍒讹紝鏈€澶?鍊嶏級
	if c.Leverage.BTCETHLeverage <= 0 {
		c.Leverage.BTCETHLeverage = 5 // 榛樿5鍊嶏紙瀹夊叏鍊硷紝閫傞厤瀛愯处鎴凤級
	}
	if c.Leverage.BTCETHLeverage > 5 {
		fmt.Printf("鈿狅笍  璀﹀憡: BTC/ETH鏉犳潌璁剧疆涓?dx锛屽鏋滀娇鐢ㄥ瓙璐︽埛鍙兘浼氬け璐ワ紙瀛愯处鎴烽檺鍒垛墹5x锛塡n", c.Leverage.BTCETHLeverage)
	}
	if c.Leverage.AltcoinLeverage <= 0 {
		c.Leverage.AltcoinLeverage = 5 // 榛樿5鍊嶏紙瀹夊叏鍊硷紝閫傞厤瀛愯处鎴凤級
	}
	if c.Leverage.AltcoinLeverage > 5 {
		fmt.Printf("鈿狅笍  璀﹀憡: 灞卞甯佹潬鏉嗚缃负%dx锛屽鏋滀娇鐢ㄥ瓙璐︽埛鍙兘浼氬け璐ワ紙瀛愯处鎴烽檺鍒垛墹5x锛塡n", c.Leverage.AltcoinLeverage)
	}

	return nil
}

// GetScanInterval 鑾峰彇鎵弿闂撮殧
func (tc *TraderConfig) GetScanInterval() time.Duration {
	return time.Duration(tc.ScanIntervalMinutes) * time.Minute
}

