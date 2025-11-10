package api

import (
    "fmt"
    "log"
    "net/http"
    "nofx/config"
    "nofx/manager"
    "nofx/trader"
    "sort"
    "strconv"
    "time"
    "strings"

    "github.com/gin-gonic/gin"
)

// Server HTTP API服务器
type Server struct {
    router        *gin.Engine
    traderManager *manager.TraderManager
    port          int
    cfg           *config.Config
}

// NewServer 创建API服务器
func NewServer(traderManager *manager.TraderManager, port int, cfg *config.Config) *Server {
    // 设置为Release模式（减少日志输出）
    gin.SetMode(gin.ReleaseMode)

    router := gin.Default()

    // 启用CORS
    router.Use(corsMiddleware())
    // 请求ID与结构化日志
    router.Use(requestIDMiddleware())
    router.Use(requestLogger())

    s := &Server{
        router:        router,
        traderManager: traderManager,
        port:          port,
        cfg:           cfg,
    }

    // 设置路由
    s.setupRoutes()
    // 仅在外部兼容开关开启时注册可选路由（默认不开启，不影响现有行为）
    s.setupExternalCompatRoutes()

    // 静态站点：仅挂载 assets 目录，避免与 /api 路由冲突
    router.Static("/assets", "./web/dist/assets")
    // 根路径返回 index.html
    router.GET("/", func(c *gin.Context) {
        c.File("./web/dist/index.html")
    })
    router.NoRoute(func(c *gin.Context) {
        p := c.Request.URL.Path
        if strings.HasPrefix(p, "/api/") {
            c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
            return
        }
        c.File("./web/dist/index.html")
    })

    return s
}

// setupExternalCompatRoutes 外部兼容路由扩展点（默认不开启）
func (s *Server) setupExternalCompatRoutes() {
    if s.cfg == nil {
        return
    }
    if !s.cfg.ExternalCompat.Enable || !s.cfg.ExternalCompat.API {
        return
    }
    // 在开关开启时，可按需注册附加的兼容路由。
    // 示例（保持注释，避免默认行为变化）：
    // api := s.router.Group("/api")
    // api.GET("/compat/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
        c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

        c.Next()
    }
}

// requestIDMiddleware 为每个请求生成并附加一个请求ID（X-Request-ID）
func requestIDMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        rid := c.GetHeader("X-Request-ID")
        if rid == "" {
            // 简单的时间戳+随机数生成（避免引入外部依赖）
            rid = fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix()%1000)
            c.Request.Header.Set("X-Request-ID", rid)
        }
        c.Writer.Header().Set("X-Request-ID", rid)
        c.Set("request_id", rid)
        c.Next()
    }
}

// requestLogger 结构化记录每个HTTP请求的关键字段
func requestLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        method := c.Request.Method
        path := c.Request.URL.Path
        query := c.Request.URL.RawQuery
        rid, _ := c.Get("request_id")

        // 预先抓取常用上下文（如 trader_id）
        traderID := c.Query("trader_id")

        c.Next()

        latency := time.Since(start)
        status := c.Writer.Status()

        log.Printf("http | rid=%v method=%s path=%s status=%d latency=%s trader_id=%s query=%s",
            rid, method, path, status, latency, traderID, query)
    }
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
    // 健康检查
    s.router.Any("/health", s.handleHealth)

    // API路由组
    api := s.router.Group("/api")
    {
        // 将健康检查也暴露在 /api/health，保持前端对所有 API 走 /api 前缀的一致性
        api.GET("/health", s.handleHealth)
        // 竞赛总览
        api.GET("/competition", s.handleCompetition)

        // 调试：当前加载配置与trader列表来源
        api.GET("/debug/config", s.handleDebugConfig)

        // Trader列表
        api.GET("/traders", s.handleTraderList)

		// 指定trader的数据（使用query参数 ?trader_id=xxx）
		api.GET("/status", s.handleStatus)
        api.GET("/account", s.handleAccount)
        api.GET("/positions", s.handlePositions)
        api.GET("/decisions", s.handleDecisions)
        api.GET("/decisions/latest", s.handleLatestDecisions)
        // 动态设置初始资金基线（用于存取款后的基线校准）
        api.POST("/initial-balance", s.handleSetInitialBalance)
        // 投资信息与动态调整
        api.GET("/investment", s.handleInvestment)
        api.POST("/investment/adjust", s.handleInvestmentAdjust)
        // 平仓明细日志（过滤 close_* 动作）
        api.GET("/close-logs", s.handleCloseLogs)
		api.GET("/statistics", s.handleStatistics)
		api.GET("/equity-history", s.handleEquityHistory)
        api.GET("/performance", s.handlePerformance)
        // OKX专用原始成交记录接口
        api.GET("/okx/fills", s.handleOkxFills)

        // 执行开关与状态
        api.GET("/execution", s.handleExecutionStatus)
        api.POST("/execution", s.handleExecutionToggle)

        // 清空所有仓位（所有Trader）
        api.POST("/close-all-positions", s.handleCloseAllPositions)

        // 一键完整开平仓流程：先清仓 -> 运行一次AI决策 -> 等待 -> 再清仓
        api.POST("/run-full-cycle", s.handleRunFullCycle)

        // AI先决策平仓，再决策开仓
        api.POST("/ai-close-then-open", s.handleAiCloseThenOpen)

        // 手动测试路由：开/平仓
        api.POST("/manual/open", s.handleManualOpen)
        api.POST("/manual/close", s.handleManualClose)
    }
}

// handleHealth 健康检查
func (s *Server) handleHealth(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "status": "ok",
        "time":   fmt.Sprintf("%s", time.Now().Format(time.RFC3339)),
    })
}

// getTraderFromQuery 从query参数获取trader
func (s *Server) getTraderFromQuery(c *gin.Context) (*manager.TraderManager, string, error) {
    traderID := c.Query("trader_id")
    if traderID == "" {
        // 如果没有指定trader_id，返回第一个trader
        ids := s.traderManager.GetTraderIDs()
        if len(ids) == 0 {
            return nil, "", fmt.Errorf("no available trader")
        }
        traderID = ids[0]
    }
    return s.traderManager, traderID, nil
}

// handleCompetition 竞赛总览（对比所有trader）
func (s *Server) handleCompetition(c *gin.Context) {
    comparison, err := s.traderManager.GetComparisonData()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "error": fmt.Sprintf("failed to get comparison data: %v", err),
        })
        return
    }
    c.JSON(http.StatusOK, comparison)
}

// handleTraderList trader列表
func (s *Server) handleTraderList(c *gin.Context) {
    traders := s.traderManager.GetAllTraders()
    result := make([]map[string]interface{}, 0, len(traders))

	for _, t := range traders {
		result = append(result, map[string]interface{}{
			"trader_id":   t.GetID(),
			"trader_name": t.GetName(),
			"ai_model":    t.GetAIModel(),
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleDebugConfig 返回后端当前加载的配置路径与trader来源统计
func (s *Server) handleDebugConfig(c *gin.Context) {
    // 从配置对象统计（原始配置文件）
    cfgCount := 0
    cfgIDs := make([]string, 0)
    if s.cfg != nil {
        cfgCount = len(s.cfg.Traders)
        for _, t := range s.cfg.Traders {
            cfgIDs = append(cfgIDs, t.ID)
        }
    }

    // 从运行中的管理器统计（启用并成功添加的）
    mgrIDs := s.traderManager.GetTraderIDs()
    mgrCount := len(mgrIDs)

    c.JSON(http.StatusOK, gin.H{
        "config_loaded_file":   func() string { if s.cfg != nil { return s.cfg.LoadedFile } else { return "" } }(),
        "trader_count_in_config": cfgCount,
        "trader_ids_in_config":   cfgIDs,
        "trader_count_in_manager": mgrCount,
        "trader_ids_in_manager":   mgrIDs,
    })
}

// handleCloseAllPositions 清空所有Trader的所有持仓
func (s *Server) handleCloseAllPositions(c *gin.Context) {
    result := s.traderManager.CloseAllPositions()
    c.JSON(http.StatusOK, result)
}

// handleRunFullCycle 执行完整开平仓流程
// 请求体可选字段：{"delay_seconds": 3}
func (s *Server) handleRunFullCycle(c *gin.Context) {
    var req struct {
        DelaySeconds int `json:"delay_seconds"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        // 如果没有JSON，使用默认值
        req.DelaySeconds = 3
    }
    if req.DelaySeconds <= 0 {
        req.DelaySeconds = 3
    }

    // 1) 先清空所有持仓
    closedBefore := s.traderManager.CloseAllPositions()

    // 2) 为所有Trader执行一次AI决策周期
    runOnce := s.traderManager.RunOnceAll()

    // 3) 等待指定秒数
    time.Sleep(time.Duration(req.DelaySeconds) * time.Second)

    // 4) 再次清仓，完成完整流程演示
    closedAfter := s.traderManager.CloseAllPositions()

    c.JSON(http.StatusOK, map[string]interface{}{
        "closed_before": closedBefore,
        "run_once":      runOnce,
        "closed_after":  closedAfter,
        "delay_seconds": req.DelaySeconds,
    })
}

// handleAiCloseThenOpen 让AI先决策并执行平仓，再决策并执行开仓
// 可选：通过query参数 ?trader_id=xxx 仅对指定Trader执行；默认对所有Trader
func (s *Server) handleAiCloseThenOpen(c *gin.Context) {
    traderID := c.Query("trader_id")

    if traderID == "" {
        // 对所有trader执行
        result := s.traderManager.RunAiCloseThenOpenAll()
        c.JSON(http.StatusOK, result)
        return
    }

    // 指定trader执行
    t, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    res, err := t.RunAiCloseThenOpen()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "result": res})
        return
    }
    c.JSON(http.StatusOK, gin.H{"result": res})
}

// handleManualOpen 手动开仓（用于测试）
// JSON Body: {"trader_id":"...","trader_idx":0,"action":"long|short","symbol":"BTCUSDT","usd":100.0,"leverage":10}
// 说明：trader_id 与 trader_idx 可二选一；若同时提供，优先使用 trader_id。trader_idx 为 0-based 索引。
func (s *Server) handleManualOpen(c *gin.Context) {
    type Req struct {
        TraderID string  `json:"trader_id"`
        TraderIdx *int   `json:"trader_idx"`
        Action   string  `json:"action"`
        Symbol   string  `json:"symbol"`
        USD      float64 `json:"usd"`
        Leverage int     `json:"leverage"`
    }
    var req Req
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
        return
    }
    // 解析 Trader 标识：优先使用 trader_id；如果为空且提供了 trader_idx，则按索引选择
    var traderID string
    if req.TraderID != "" {
        traderID = req.TraderID
    } else if req.TraderIdx != nil {
        ids := s.traderManager.GetTraderIDs()
        if *req.TraderIdx < 0 || *req.TraderIdx >= len(ids) {
            c.JSON(http.StatusBadRequest, gin.H{"error": "trader_idx 越界或无效"})
            return
        }
        traderID = ids[*req.TraderIdx]
    }

    if traderID == "" || req.Symbol == "" || req.Action == "" || req.USD <= 0 || req.Leverage <= 0 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "缺少必要参数或参数不合法"})
        return
    }

    t, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    var result map[string]interface{}
    switch req.Action {
    case "long":
        result, err = t.ManualOpenLong(req.Symbol, req.USD, req.Leverage)
    case "short":
        result, err = t.ManualOpenShort(req.Symbol, req.USD, req.Leverage)
    default:
        c.JSON(http.StatusBadRequest, gin.H{"error": "action 必须为 long 或 short"})
        return
    }
    if err != nil {
        // 若为结构化订单错误，返回详细信息以便前端渲染
        if oe, ok := err.(*trader.OrderError); ok {
            c.JSON(http.StatusBadRequest, gin.H{
                "error":        oe.Message,
                "order_error":  oe,
                "success":      false,
            })
            return
        }
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "order": result})
}

// handleManualClose 手动平仓（用于测试）
// JSON Body: {"trader_id":"...","trader_idx":0,"side":"long|short","symbol":"BTCUSDT"}
// 说明：trader_id 与 trader_idx 可二选一；若同时提供，优先使用 trader_id。trader_idx 为 0-based 索引。
func (s *Server) handleManualClose(c *gin.Context) {
    type Req struct {
        TraderID string `json:"trader_id"`
        TraderIdx *int  `json:"trader_idx"`
        Side     string `json:"side"`
        Symbol   string `json:"symbol"`
    }
    var req Req
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
        return
    }
    // 解析 Trader 标识：优先使用 trader_id；如果为空且提供了 trader_idx，则按索引选择
    var traderID string
    if req.TraderID != "" {
        traderID = req.TraderID
    } else if req.TraderIdx != nil {
        ids := s.traderManager.GetTraderIDs()
        if *req.TraderIdx < 0 || *req.TraderIdx >= len(ids) {
            c.JSON(http.StatusBadRequest, gin.H{"error": "trader_idx 越界或无效"})
            return
        }
        traderID = ids[*req.TraderIdx]
    }

    if traderID == "" || req.Symbol == "" || req.Side == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "缺少必要参数或参数不合法"})
        return
    }

    t, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    var result map[string]interface{}
    switch req.Side {
    case "long":
        result, err = t.ManualCloseLong(req.Symbol)
    case "short":
        result, err = t.ManualCloseShort(req.Symbol)
    default:
        c.JSON(http.StatusBadRequest, gin.H{"error": "side 必须为 long 或 short"})
        return
    }
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"success": true, "order": result})
}

// handleStatus 系统状态
func (s *Server) handleStatus(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := trader.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAccount 账户信息
func (s *Server) handleAccount(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.Printf("📊 收到账户信息请求 [%s]", trader.GetName())
	account, err := trader.GetAccountInfo()
	if err != nil {
		log.Printf("❌ 获取账户信息失败 [%s]: %v", trader.GetName(), err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取账户信息失败: %v", err),
		})
		return
	}

	log.Printf("✓ 返回账户信息 [%s]: 净值=%.2f, 可用=%.2f, 盈亏=%.2f (%.2f%%)",
		trader.GetName(),
		account["total_equity"],
		account["available_balance"],
		account["total_pnl"],
		account["total_pnl_pct"])
	c.JSON(http.StatusOK, account)
}

// handleSetInitialBalance 设置指定 Trader 的初始资金基线
// JSON Body: {"trader_id":"何百万 okx_deepseek","value":50}
func (s *Server) handleSetInitialBalance(c *gin.Context) {
    type Req struct {
        TraderID string  `json:"trader_id"`
        Value    float64 `json:"value"`
    }
    var req Req
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
        return
    }
    if req.TraderID == "" || req.Value <= 0 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 不能为空且 value 必须大于 0"})
        return
    }

    t, err := s.traderManager.GetTrader(req.TraderID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    t.SetInitialBalance(req.Value)
    log.Printf("⚙️ 已设置初始资金基线 [%s] = %.2f", t.GetName(), req.Value)
    c.JSON(http.StatusOK, gin.H{"success": true, "trader_id": req.TraderID, "initial_balance": req.Value})
}

// handlePositions 持仓列表
func (s *Server) handlePositions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	positions, err := trader.GetPositions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取持仓列表失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, positions)
}

// handleDecisions 决策日志列表
func (s *Server) handleDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取所有历史决策记录（无限制）
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, records)
}

// handleLatestDecisions 最新决策日志（最近5条，最新的在前）
func (s *Server) handleLatestDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	records, err := trader.GetDecisionLogger().GetLatestRecords(5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	// 反转数组，让最新的在前面（用于列表显示）
	// GetLatestRecords返回的是从旧到新（用于图表），这里需要从新到旧
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	c.JSON(http.StatusOK, records)
}

// handleStatistics 统计信息
func (s *Server) handleStatistics(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	stats, err := trader.GetDecisionLogger().GetStatistics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取统计信息失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleEquityHistory 收益率历史数据
func (s *Server) handleEquityHistory(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取尽可能多的历史数据（几天的数据）
	// 每3分钟一个周期：10000条 = 约20天的数据
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取历史数据失败: %v", err),
		})
		return
	}

	// 构建收益率历史数据点
	type EquityPoint struct {
		Timestamp        string  `json:"timestamp"`
		TotalEquity      float64 `json:"total_equity"`      // 账户净值（wallet + unrealized）
		AvailableBalance float64 `json:"available_balance"` // 可用余额
		TotalPnL         float64 `json:"total_pnl"`         // 总盈亏（相对初始余额）
		TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
		PositionCount    int     `json:"position_count"`    // 持仓数量
		MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
		CycleNumber      int     `json:"cycle_number"`
	}

    // 读取初始余额用于展示；百分比采用“动态累计投入”基线
    initialBalance := 0.0
    if status := trader.GetStatus(); status != nil {
        if ib, ok := status["initial_balance"].(float64); ok && ib > 0 {
            initialBalance = ib
        }
    }

	var history []EquityPoint
	for _, record := range records {
		// TotalBalance字段实际存储的是TotalEquity
		totalEquity := record.AccountState.TotalBalance
		// TotalUnrealizedProfit字段实际存储的是TotalPnL（相对初始余额）
		totalPnL := record.AccountState.TotalUnrealizedProfit

        // 计算盈亏百分比：使用“截止该记录时间的累计投入金额”作为基线
        totalPnLPct := 0.0
        investedAt := trader.GetInvestedAmountAt(record.Timestamp)
        if investedAt > 0 {
            totalPnLPct = (totalPnL / investedAt) * 100
        } else if initialBalance > 0 {
            // 兜底：累计投入不可用时，退回初始余额
            totalPnLPct = (totalPnL / initialBalance) * 100
        }

		history = append(history, EquityPoint{
			Timestamp:        record.Timestamp.Format("2006-01-02 15:04:05"),
			TotalEquity:      totalEquity,
			AvailableBalance: record.AccountState.AvailableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			PositionCount:    record.AccountState.PositionCount,
			MarginUsedPct:    record.AccountState.MarginUsedPct,
			CycleNumber:      record.CycleNumber,
		})
	}

	c.JSON(http.StatusOK, history)
}

// handleInvestment 获取投资信息（总投入金额 + 调整事件）
func (s *Server) handleInvestment(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    t, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    invested := t.GetInvestedAmount()
    adjs := t.GetInvestmentAdjustments()

    // 规范化输出（时间戳转字符串）
    type Adj struct {
        Amount    float64 `json:"amount"`
        Timestamp string  `json:"timestamp"`
        Note      string  `json:"note,omitempty"`
    }
    out := make([]Adj, 0, len(adjs))
    for _, a := range adjs {
        out = append(out, Adj{Amount: a.Amount, Timestamp: a.Timestamp.Format("2006-01-02 15:04:05"), Note: a.Note})
    }

    c.JSON(http.StatusOK, gin.H{
        "trader_id":       traderID,
        "invested_amount": invested,
        "adjustments":     out,
    })
}

// handleInvestmentAdjust 追加投资调整（正数入金，负数出金）
func (s *Server) handleInvestmentAdjust(c *gin.Context) {
    var req struct {
        TraderID string  `json:"trader_id"`
        Amount   float64 `json:"amount"`
        Note     string  `json:"note"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
        return
    }
    if req.TraderID == "" || req.Amount == 0 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 不能为空且 amount 不能为 0"})
        return
    }

    t, err := s.traderManager.GetTrader(req.TraderID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    if err := t.AddInvestmentDelta(req.Amount, req.Note); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "success":         true,
        "trader_id":       req.TraderID,
        "invested_amount": t.GetInvestedAmount(),
    })
}

// handlePerformance AI历史表现分析（用于展示AI学习和反思）
func (s *Server) handlePerformance(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    trader, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    // 支持通过查询参数 cycles 指定分析周期上限，默认100，最大5000
    cycles := 100
    if cs := c.Query("cycles"); cs != "" {
        if v, e := strconv.Atoi(cs); e == nil && v > 0 {
            cycles = v
        }
    }
    if cycles > 5000 {
        cycles = 5000
    }
    // 使用持久化决策日志进行分析，确保支持大窗口（最多5000周期）
    performance, err := trader.GetDecisionLogger().AnalyzePerformance(cycles)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "error": fmt.Sprintf("分析历史表现失败: %v", err),
        })
        return
    }

    // 回退逻辑与模拟平仓已移除，保留原始 AnalyzePerformance 输出

    c.JSON(http.StatusOK, performance)
}

// handleOkxFills 获取OKX成交记录（需要指定 trader_id）
func (s *Server) handleOkxFills(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    trader, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    limit := 50
    if ls := c.Query("limit"); ls != "" {
        if v, e := strconv.Atoi(ls); e == nil && v > 0 {
            limit = v
        }
    }
    // 防御：OKX单次最大返回条数通常不超过100，避免过大limit导致接口报错
    if limit > 100 {
        limit = 100
    }

    fills, err := trader.GetOKXFills(limit)
    if err != nil {
        // 前端期望数组类型，避免500导致前端中断；改为返回空数组并记录错误
        log.Printf("⚠️ 获取OKX成交记录失败(trader=%s, limit=%d): %v", traderID, limit, err)
        c.JSON(http.StatusOK, []map[string]interface{}{})
        return
    }
    c.JSON(http.StatusOK, fills)
}

// handleExecutionStatus 获取自动执行开关状态
func (s *Server) handleExecutionStatus(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    trader, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "trader_id": trader.GetID(),
        "execution_enabled": trader.IsExecutionEnabled(),
    })
}

// handleExecutionToggle 设置自动执行开关
func (s *Server) handleExecutionToggle(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    trader, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    var req struct {
        Enabled bool `json:"enabled"`
    }
    if err := c.BindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求体"})
        return
    }

    trader.SetExecutionEnabled(req.Enabled)
    c.JSON(http.StatusOK, gin.H{
        "trader_id": trader.GetID(),
        "execution_enabled": trader.IsExecutionEnabled(),
    })
}

// Start 启动服务器
func (s *Server) Start() error {
    addr := fmt.Sprintf(":%d", s.port)
    log.Printf("🌐 API服务器启动在 http://localhost%s", addr)
    log.Printf("📊 API文档:")
	log.Printf("  • GET  /api/competition      - 竞赛总览（对比所有trader）")
	log.Printf("  • GET  /api/traders          - Trader列表")
	log.Printf("  • GET  /api/status?trader_id=xxx     - 指定trader的系统状态")
	log.Printf("  • GET  /api/account?trader_id=xxx    - 指定trader的账户信息")
	log.Printf("  • GET  /api/positions?trader_id=xxx  - 指定trader的持仓列表")
	log.Printf("  • GET  /api/decisions?trader_id=xxx  - 指定trader的决策日志")
	log.Printf("  • GET  /api/decisions/latest?trader_id=xxx - 指定trader的最新决策")
	log.Printf("  • GET  /api/statistics?trader_id=xxx - 指定trader的统计信息")
    log.Printf("  • GET  /api/equity-history?trader_id=xxx - 指定trader的收益率历史数据")
    log.Printf("  • GET  /api/performance?trader_id=xxx - 指定trader的AI学习表现分析")
    log.Printf("  • POST /api/close-all-positions      - 平掉所有Trader的全部持仓")
    log.Printf("  • POST /api/run-full-cycle           - 一键完整开平仓流程（先清仓→AI决策→再清仓）")
    log.Printf("  • POST /api/ai-close-then-open       - AI先决策平仓，再决策开仓（可选trader_id）")
    log.Printf("  • GET  /health               - 健康检查")
    log.Println()

    return s.router.Run(addr)
}
// handleCloseLogs 返回平仓明细日志（按时间倒序）
func (s *Server) handleCloseLogs(c *gin.Context) {
    _, traderID, err := s.getTraderFromQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    trader, err := s.traderManager.GetTrader(traderID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
        return
    }

    // 获取最近的决策记录，足够多以覆盖平仓动作
    records, err := trader.GetDecisionLogger().GetLatestRecords(1000)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取决策日志失败: %v", err)})
        return
    }

    type CloseLog struct {
        Action    string    `json:"action"`
        Symbol    string    `json:"symbol"`
        Quantity  float64   `json:"quantity"`
        Price     float64   `json:"price"`
        OrderID   int64     `json:"order_id"`
        Timestamp time.Time `json:"timestamp"`
        Success   bool      `json:"success"`
        Error     string    `json:"error"`
    }

    logs := make([]CloseLog, 0)
    for _, r := range records {
        for _, a := range r.Decisions {
            if a.Action == "close_long" || a.Action == "close_short" {
                logs = append(logs, CloseLog{
                    Action:    a.Action,
                    Symbol:    a.Symbol,
                    Quantity:  a.Quantity,
                    Price:     a.Price,
                    OrderID:   a.OrderID,
                    Timestamp: a.Timestamp,
                    Success:   a.Success,
                    Error:     a.Error,
                })
            }
        }
    }

    // 时间倒序
    sort.Slice(logs, func(i, j int) bool { return logs[i].Timestamp.After(logs[j].Timestamp) })

    // 支持 limit 参数
    limit := len(logs)
    if ls := c.Query("limit"); ls != "" {
        if v, e := strconv.Atoi(ls); e == nil && v > 0 && v < limit {
            limit = v
        }
    }

    c.JSON(http.StatusOK, logs[:limit])
}
