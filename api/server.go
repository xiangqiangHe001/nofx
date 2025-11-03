package api

import (
    "fmt"
    "log"
    "net/http"
    "nofx/manager"
    "nofx/logger"
    "sort"
    "strconv"
    "time"

    "github.com/gin-gonic/gin"
)

// Server HTTP API服务器
type Server struct {
	router        *gin.Engine
	traderManager *manager.TraderManager
	port          int
}

// NewServer 创建API服务器
func NewServer(traderManager *manager.TraderManager, port int) *Server {
	// 设置为Release模式（减少日志输出）
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// 启用CORS
	router.Use(corsMiddleware())

	s := &Server{
		router:        router,
		traderManager: traderManager,
		port:          port,
	}

	// 设置路由
	s.setupRoutes()

	return s
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

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	// 健康检查
	s.router.Any("/health", s.handleHealth)

	// API路由组
	api := s.router.Group("/api")
    {
		// 竞赛总览
		api.GET("/competition", s.handleCompetition)

		// Trader列表
		api.GET("/traders", s.handleTraderList)

		// 指定trader的数据（使用query参数 ?trader_id=xxx）
		api.GET("/status", s.handleStatus)
		api.GET("/account", s.handleAccount)
		api.GET("/positions", s.handlePositions)
		api.GET("/decisions", s.handleDecisions)
		api.GET("/decisions/latest", s.handleLatestDecisions)
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

	// 从AutoTrader获取初始余额（用于计算盈亏百分比）
	initialBalance := 0.0
	if status := trader.GetStatus(); status != nil {
		if ib, ok := status["initial_balance"].(float64); ok && ib > 0 {
			initialBalance = ib
		}
	}

	// 如果无法从status获取，且有历史记录，则从第一条记录获取
	if initialBalance == 0 && len(records) > 0 {
		// 第一条记录的equity作为初始余额
		initialBalance = records[0].AccountState.TotalBalance
	}

	// 如果还是无法获取，返回错误
	if initialBalance == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "无法获取初始余额",
		})
		return
	}

	var history []EquityPoint
	for _, record := range records {
		// TotalBalance字段实际存储的是TotalEquity
		totalEquity := record.AccountState.TotalBalance
		// TotalUnrealizedProfit字段实际存储的是TotalPnL（相对初始余额）
		totalPnL := record.AccountState.TotalUnrealizedProfit

		// 计算盈亏百分比
		totalPnLPct := 0.0
		if initialBalance > 0 {
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

    // 分析最近20个周期的交易表现
    performance, err := trader.GetDecisionLogger().AnalyzePerformance(20)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "error": fmt.Sprintf("分析历史表现失败: %v", err),
        })
        return
    }

    // 基于成交记录的回退：
    // 1) 决策日志无法形成有效交易（TotalTrades==0）
    // 2) 或仅有盈利、没有任何亏损（可能窗口内样本不完整导致 ProfitFactor 显示为 999）
    //    这类情形下尝试用 OKX 成交记录进行补全估算
    if performance != nil && (performance.TotalTrades == 0 || (performance.TotalTrades > 0 && performance.LosingTrades == 0 && performance.WinningTrades > 0)) {
        // 仅针对 OKX 交易器提供回退统计
        // 使用更大的样本来提高覆盖度
        fills, ferr := trader.GetOKXFills(500)
        if ferr == nil && len(fills) > 0 {
            // 将成交记录按时间升序排序
            sort.SliceStable(fills, func(i, j int) bool {
                ti, _ := strconv.ParseInt(fmt.Sprintf("%v", fills[i]["timestamp"]), 10, 64)
                tj, _ := strconv.ParseInt(fmt.Sprintf("%v", fills[j]["timestamp"]), 10, 64)
                return ti < tj
            })

            // 每个 inst_id + pos_side 的持仓状态
            type posState struct {
                openQty      float64
                avgOpenPrice float64
                openTimeMs   int64
            }
            states := make(map[string]*posState)

            totalWin := 0.0
            totalLoss := 0.0
            symbolStats := make(map[string]*logger.SymbolPerformance)

            // 构造最近交易（最多10条）
            var recent []logger.TradeOutcome

            for _, f := range fills {
                symbol := fmt.Sprintf("%v", f["symbol"]) // 例如 BTCUSDT
                instID := fmt.Sprintf("%v", f["inst_id"]) // 例如 BTC-USDT-SWAP
                side := fmt.Sprintf("%v", f["side"])      // buy/sell
                posSide := fmt.Sprintf("%v", f["pos_side"]) // long/short
                price, _ := f["price"].(float64)
                qty, _ := f["quantity"].(float64)
                tsStr := fmt.Sprintf("%v", f["timestamp"]) // 毫秒时间戳字符串
                tsMs, _ := strconv.ParseInt(tsStr, 10, 64)

                key := instID + "/" + posSide
                st := states[key]
                if st == nil {
                    st = &posState{}
                    states[key] = st
                }

                // long: buy 增加持仓，sell 减少持仓（产生收益）
                // short: sell 增加持仓，buy 减少持仓（产生收益）
                isOpen := (posSide == "long" && side == "buy") || (posSide == "short" && side == "sell")
                isClose := (posSide == "long" && side == "sell") || (posSide == "short" && side == "buy")

                if isOpen && qty > 0 && price > 0 {
                    // 加权更新开仓均价
                    newQty := st.openQty + qty
                    if newQty > 0 {
                        st.avgOpenPrice = (st.avgOpenPrice*st.openQty + price*qty) / newQty
                    } else {
                        st.avgOpenPrice = price
                    }
                    st.openQty = newQty
                    if st.openTimeMs == 0 { st.openTimeMs = tsMs }
                } else if isClose && qty > 0 && st.openQty > 0 && st.avgOpenPrice > 0 {
                    closed := qty
                    if closed > st.openQty { closed = st.openQty }

                    var pnl float64
                    if posSide == "long" {
                        pnl = closed * (price - st.avgOpenPrice)
                    } else { // short
                        pnl = closed * (st.avgOpenPrice - price)
                    }

                    // 更新持仓剩余
                    st.openQty -= closed
                    if st.openQty <= 0 {
                        st.openQty = 0
                        st.avgOpenPrice = 0
                        st.openTimeMs = 0
                    }

                    // 记录交易结果（用于最近交易展示和统计）
                    outcome := logger.TradeOutcome{
                        Symbol:        symbol,
                        Side:          posSide,
                        Quantity:      closed,
                        Leverage:      1,
                        OpenPrice:     st.avgOpenPrice,
                        ClosePrice:    price,
                        PositionValue: closed * st.avgOpenPrice,
                        MarginUsed:    closed * st.avgOpenPrice, // 以杠杆1估算
                        PnL:           pnl,
                        PnLPct:        0, // 无法精准估算，设为0
                        Duration:      "",
                        OpenTime:      time.UnixMilli(st.openTimeMs),
                        CloseTime:     time.UnixMilli(tsMs),
                        WasStopLoss:   false,
                    }
                    recent = append(recent, outcome)

                    // 汇总总交易与赢亏
                    performance.TotalTrades++
                    if pnl > 0 {
                        performance.WinningTrades++
                        totalWin += pnl
                    } else if pnl < 0 {
                        performance.LosingTrades++
                        totalLoss += pnl // 负数
                    }

                    // 更新币种统计
                    if _, ok := symbolStats[symbol]; !ok {
                        symbolStats[symbol] = &logger.SymbolPerformance{Symbol: symbol}
                    }
                    ss := symbolStats[symbol]
                    ss.TotalTrades++
                    ss.TotalPnL += pnl
                    if pnl > 0 { ss.WinningTrades++ } else if pnl < 0 { ss.LosingTrades++ }
                }
            }

            // 计算总体指标
            performance.RecentTrades = recent
            performance.SymbolStats = symbolStats
            if performance.TotalTrades > 0 {
                performance.WinRate = (float64(performance.WinningTrades) / float64(performance.TotalTrades)) * 100
                if performance.WinningTrades > 0 {
                    performance.AvgWin = totalWin / float64(performance.WinningTrades)
                }
                if performance.LosingTrades > 0 {
                    performance.AvgLoss = totalLoss / float64(performance.LosingTrades)
                }
                if totalLoss != 0 {
                    performance.ProfitFactor = totalWin / (-totalLoss)
                } else if totalWin > 0 {
                    performance.ProfitFactor = 999.0
                }

                // 币种胜率/平均值与最佳/最差
                bestPnL := -1e9
                worstPnL := 1e9
                for sym, ss := range performance.SymbolStats {
                    if ss.TotalTrades > 0 {
                        ss.WinRate = (float64(ss.WinningTrades) / float64(ss.TotalTrades)) * 100
                        ss.AvgPnL = ss.TotalPnL / float64(ss.TotalTrades)
                        if ss.TotalPnL > bestPnL { bestPnL = ss.TotalPnL; performance.BestSymbol = sym }
                        if ss.TotalPnL < worstPnL { worstPnL = ss.TotalPnL; performance.WorstSymbol = sym }
                    }
                }
            }
        }
    }

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

    fills, err := trader.GetOKXFills(limit)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
