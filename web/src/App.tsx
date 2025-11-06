import { useEffect, useState } from 'react';
import useSWR from 'swr';
import { api } from './lib/api';
import { EquityChart } from './components/EquityChart';
import { CompetitionPage } from './components/CompetitionPage';
import AILearning from './components/AILearning';
import FillsPage from './components/FillsPage';
import CloseLogsPage from './components/CloseLogsPage';
import { LanguageProvider, useLanguage } from './contexts/LanguageContext';
import { t, type Language } from './i18n/translations';
import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  Statistics,
  TraderInfo,
} from './types';

type Page = 'competition' | 'trader' | 'fills' | 'closeLogs';

function App() {
  const { language, setLanguage } = useLanguage();

  // 从URL hash读取初始页面状态（支持刷新保持页面）
  const getInitialPage = (): Page => {
    const hash = window.location.hash.slice(1); // 去掉 #
    if (hash === 'trader' || hash === 'details') return 'trader';
    if (hash === 'fills') return 'fills';
    if (hash === 'closeLogs') return 'closeLogs';
    return 'competition';
  };

  const [currentPage, setCurrentPage] = useState<Page>(getInitialPage());
  const [selectedTraderId, setSelectedTraderId] = useState<string | undefined>();
  const [lastUpdate, setLastUpdate] = useState<string>('--:--:--');

  // 监听URL hash变化，同步页面状态
  useEffect(() => {
    const handleHashChange = () => {
      const hash = window.location.hash.slice(1);
      if (hash === 'trader' || hash === 'details') {
        setCurrentPage('trader');
      } else if (hash === 'fills') {
        setCurrentPage('fills');
      } else if (hash === 'closeLogs') {
        setCurrentPage('closeLogs');
      } else if (hash === 'competition' || hash === '') {
        setCurrentPage('competition');
      }
    };

    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  // 切换页面时更新URL hash
  const navigateToPage = (page: Page) => {
    setCurrentPage(page);
    if (page === 'competition') {
      window.location.hash = '';
    } else {
      window.location.hash = page;
    }
  };

  // 获取trader列表
  const { data: traders } = useSWR<TraderInfo[]>('traders', api.getTraders, {
    refreshInterval: 10000,
  });

  // 当获取到traders后，设置默认选中第一个
  useEffect(() => {
    if (traders && traders.length > 0 && !selectedTraderId) {
      setSelectedTraderId(traders[0].trader_id);
    }
  }, [traders, selectedTraderId]);

  // 如果在trader页面，获取该trader的数据
  const { data: status, mutate: mutateStatus } = useSWR<SystemStatus>(
    currentPage === 'trader' && selectedTraderId
      ? `status-${selectedTraderId}`
      : null,
    () => api.getStatus(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  // 独立轮询执行开关状态（更快同步UI），避免受状态接口缓存影响
  const { data: execution, mutate: mutateExecution } = useSWR<{ execution_enabled: boolean }>(
    currentPage === 'trader' && selectedTraderId ? `execution-${selectedTraderId}` : null,
    () => api.getExecutionStatus(selectedTraderId),
    {
      refreshInterval: 5000,
      revalidateOnFocus: false,
      dedupingInterval: 3000,
    }
  );

  const { data: account } = useSWR<AccountInfo>(
    currentPage === 'trader' && selectedTraderId
      ? `account-${selectedTraderId}`
      : null,
    () => api.getAccount(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  const { data: positions } = useSWR<Position[]>(
    currentPage === 'trader' && selectedTraderId
      ? `positions-${selectedTraderId}`
      : null,
    () => api.getPositions(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  const { data: decisions } = useSWR<DecisionRecord[]>(
    currentPage === 'trader' && selectedTraderId
      ? `decisions/latest-${selectedTraderId}`
      : null,
    () => api.getLatestDecisions(selectedTraderId),
    {
      refreshInterval: 30000, // 30秒刷新（决策更新频率较低）
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  const { data: stats } = useSWR<Statistics>(
    currentPage === 'trader' && selectedTraderId
      ? `statistics-${selectedTraderId}`
      : null,
    () => api.getStatistics(selectedTraderId),
    {
      refreshInterval: 30000, // 30秒刷新（统计数据更新频率较低）
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  useEffect(() => {
    if (account) {
      const now = new Date().toLocaleTimeString();
      setLastUpdate(now);
    }
  }, [account]);

  const selectedTrader = traders?.find((t) => t.trader_id === selectedTraderId);

  return (
    <div className="min-h-screen" style={{ background: '#0B0E11', color: '#EAECEF' }}>
      {/* Header - Binance Style */}
      <header className="glass sticky top-0 z-50 backdrop-blur-xl">
        <div className="max-w-[1920px] mx-auto px-3 sm:px-6 py-3 sm:py-4">
          {/* Mobile: Two rows, Desktop: Single row */}
          <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            {/* Left: Logo and Title */}
            <div className="flex items-center gap-2 sm:gap-3 flex-shrink-0">
              <div className="w-7 h-7 sm:w-8 sm:h-8 rounded-full flex items-center justify-center text-lg sm:text-xl" style={{ background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)' }}>
                ⚡
              </div>
              <div>
                <h1 className="text-base sm:text-xl font-bold leading-tight" style={{ color: '#EAECEF' }}>
                  {t('appTitle', language)}
                </h1>
                <p className="text-xs mono hidden sm:block" style={{ color: '#848E9C' }}>
                  {t('subtitle', language)}
                </p>
              </div>
            </div>

            {/* Right: Controls - Wrap on mobile */}
            <div className="flex items-center gap-2 flex-wrap md:flex-nowrap">
              {/* Execution Toggle - show on trader page when status is available */}
              {currentPage === 'trader' && (execution || status) && (
                <button
                  onClick={async () => {
                    const current = execution?.execution_enabled ?? status?.execution_enabled ?? false;
                    const next = !current;
                    try {
                      await api.setExecution(next, selectedTraderId);
                      // 即时更新UI中的状态，不等待下一次轮询
                      mutateExecution({ execution_enabled: next }, false);
                      mutateStatus((prev) => prev ? { ...prev, execution_enabled: next } : prev, false);
                    } catch (e) {
                      console.error(e);
                    }
                  }}
                  className="flex items-center gap-2 px-2 md:px-3 py-1.5 md:py-2 rounded text-sm font-semibold transition-all hover:scale-105"
                  style={(execution?.execution_enabled ?? status?.execution_enabled)
                    ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81', border: '1px solid #2B3139' }
                    : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D', border: '1px solid #2B3139' }
                  }
                >
                  <span>⚙️ {t('autoExecute', language)}</span>
                  <span className="hidden md:inline">{t((execution?.execution_enabled ?? status?.execution_enabled) ? 'enabled' : 'disabled', language)}</span>
                </button>
              )}

              {/* Language Toggle */}
              <div className="flex gap-0.5 sm:gap-1 rounded p-0.5 sm:p-1" style={{ background: '#1E2329' }}>
                <button
                  onClick={() => setLanguage('zh')}
                  className="px-2 sm:px-3 py-1 sm:py-1.5 rounded text-xs font-semibold transition-all"
                  style={language === 'zh'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  中文
                </button>
                <button
                  onClick={() => setLanguage('en')}
                  className="px-2 sm:px-3 py-1 sm:py-1.5 rounded text-xs font-semibold transition-all"
                  style={language === 'en'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  EN
                </button>
              </div>

              {/* Page Toggle */}
              <div className="flex gap-0.5 sm:gap-1 rounded p-0.5 sm:p-1" style={{ background: '#1E2329' }}>
                <button
                  onClick={() => navigateToPage('competition')}
                  className="px-2 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-semibold transition-all"
                  style={currentPage === 'competition'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  {t('competition', language)}
                </button>
                <button
                  onClick={() => navigateToPage('trader')}
                  className="px-2 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-semibold transition-all"
                  style={currentPage === 'trader'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  {t('details', language)}
                </button>
              <button
                onClick={() => navigateToPage('fills')}
                className="px-2 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-semibold transition-all"
                style={currentPage === 'fills'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                {t('tradeHistory', language)}
              </button>
              <button
                onClick={() => navigateToPage('closeLogs')}
                className="px-2 sm:px-4 py-1.5 sm:py-2 rounded text-xs sm:text-sm font-semibold transition-all"
                style={currentPage === 'closeLogs'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                平仓明细
              </button>
              </div>

              {/* Trader Selector (only show on trader page) */}
              {(currentPage === 'trader' || currentPage === 'fills' || currentPage === 'closeLogs') && traders && traders.length > 0 && (
                <select
                  value={selectedTraderId}
                  onChange={(e) => setSelectedTraderId(e.target.value)}
                  className="rounded px-2 sm:px-3 py-1.5 sm:py-2 text-xs sm:text-sm font-medium cursor-pointer transition-colors flex-1 sm:flex-initial"
                  style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
                >
                  {traders.map((trader) => (
                    <option key={trader.trader_id} value={trader.trader_id}>
                      {trader.trader_name} ({trader.ai_model.toUpperCase()})
                    </option>
                  ))}
                </select>
              )}

              {/* Status Indicator (only show on trader page) */}
              {currentPage === 'trader' && status && (
                <div
                  className="flex items-center gap-1.5 sm:gap-2 px-2 sm:px-3 py-1.5 sm:py-2 rounded"
                  style={status.is_running
                    ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81', border: '1px solid rgba(14, 203, 129, 0.2)' }
                    : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D', border: '1px solid rgba(246, 70, 93, 0.2)' }
                  }
                >
                  <div
                    className={`w-2 h-2 rounded-full ${status.is_running ? 'pulse-glow' : ''}`}
                    style={{ background: status.is_running ? '#0ECB81' : '#F6465D' }}
                  />
                  <span className="font-semibold mono text-xs">
                    {t(status.is_running ? 'running' : 'stopped', language)}
                  </span>
                </div>
              )}
            </div>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="max-w-[1920px] mx-auto px-6 py-6">
        {currentPage === 'competition' ? (
          <CompetitionPage />
        ) : currentPage === 'fills' ? (
          <FillsPage traderId={selectedTraderId} />
        ) : currentPage === 'closeLogs' ? (
          <CloseLogsPage traderId={selectedTraderId} />
        ) : (
          <TraderDetailsPage
            selectedTrader={selectedTrader}
            status={status}
            account={account}
            positions={positions}
            decisions={decisions}
            stats={stats}
            lastUpdate={lastUpdate}
            language={language}
          />
        )}
      </main>

      {/* Footer */}
      <footer className="mt-16" style={{ borderTop: '1px solid #2B3139', background: '#181A20' }}>
        <div className="max-w-[1920px] mx-auto px-6 py-6 text-center text-sm" style={{ color: '#5E6673' }}>
          <p>{t('footerTitle', language)}</p>
          <p className="mt-1">{t('footerWarning', language)}</p>
        </div>
      </footer>
    </div>
  );
}

// Trader Details Page Component
function TraderDetailsPage({
  selectedTrader,
  status,
  account,
  positions,
  decisions,
  lastUpdate,
  language,
}: {
  selectedTrader?: TraderInfo;
  status?: SystemStatus;
  account?: AccountInfo;
  positions?: Position[];
  decisions?: DecisionRecord[];
  stats?: Statistics;
  lastUpdate: string;
  language: Language;
}) {
  const [analysisEnabled, setAnalysisEnabled] = useState<boolean>(() => {
    try {
      const saved = localStorage.getItem('analysisEnabled');
      return saved ? saved === 'true' : false;
    } catch {
      return false;
    }
  });

  const toggleAnalysis = () => {
    setAnalysisEnabled(prev => {
      const next = !prev;
      try { localStorage.setItem('analysisEnabled', String(next)); } catch {}
      return next;
    });
  };
  if (!selectedTrader) {
    return (
      <div className="space-y-6">
        {/* Loading Skeleton - Binance Style */}
        <div className="binance-card p-6 animate-pulse">
          <div className="skeleton h-8 w-48 mb-3"></div>
          <div className="flex gap-4">
            <div className="skeleton h-4 w-32"></div>
            <div className="skeleton h-4 w-24"></div>
            <div className="skeleton h-4 w-28"></div>
          </div>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="binance-card p-5 animate-pulse">
              <div className="skeleton h-4 w-24 mb-3"></div>
              <div className="skeleton h-8 w-32"></div>
            </div>
          ))}
        </div>
        <div className="binance-card p-6 animate-pulse">
          <div className="skeleton h-6 w-40 mb-4"></div>
          <div className="skeleton h-64 w-full"></div>
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Trader Header */}
      <div className="mb-6 rounded p-6 animate-scale-in" style={{ background: 'linear-gradient(135deg, rgba(240, 185, 11, 0.15) 0%, rgba(252, 213, 53, 0.05) 100%)', border: '1px solid rgba(240, 185, 11, 0.2)', boxShadow: '0 0 30px rgba(240, 185, 11, 0.15)' }}>
        <h2 className="text-2xl font-bold mb-3 flex items-center gap-2" style={{ color: '#EAECEF' }}>
          <span className="w-10 h-10 rounded-full flex items-center justify-center text-xl" style={{ background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)' }}>
            🤖
          </span>
          {selectedTrader.trader_name}
        </h2>
        <div className="flex items-center gap-4 text-sm" style={{ color: '#848E9C' }}>
          <span>AI Model: <span className="font-semibold" style={{ color: selectedTrader.ai_model === 'qwen' ? '#c084fc' : '#60a5fa' }}>{selectedTrader.ai_model.toUpperCase()}</span></span>
          {status && (
            <>
              <span>•</span>
              <span>Cycles: {status.call_count}</span>
              <span>•</span>
              <span>Runtime: {status.runtime_minutes} min</span>
            </>
          )}
        </div>
      </div>

      {/* Debug Info */}
      {account && (
        <div className="mb-4 p-3 rounded text-xs font-mono" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
          <div style={{ color: '#848E9C' }}>
            🔄 Last Update: {lastUpdate} | Total Equity: {account.total_equity?.toFixed(2) || '0.00'} |
            Available: {account.available_balance?.toFixed(2) || '0.00'} | P&L: {account.total_pnl?.toFixed(2) || '0.00'}{' '}
            ({account.total_pnl_pct?.toFixed(2) || '0.00'}%)
          </div>
        </div>
      )}

      {/* Account Overview */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-8">
        <StatCard
          title={t('totalEquity', language)}
          value={`${account?.total_equity?.toFixed(2) || '0.00'} USDT`}
          change={account?.total_pnl_pct || 0}
          positive={(account?.total_pnl ?? 0) > 0}
        />
        <StatCard
          title={t('availableBalance', language)}
          value={`${account?.available_balance?.toFixed(2) || '0.00'} USDT`}
          subtitle={`${(account?.available_balance && account?.total_equity ? ((account.available_balance / account.total_equity) * 100).toFixed(1) : '0.0')}% ${t('free', language)}`}
        />
        <StatCard
          title={t('totalPnL', language)}
          value={`${account?.total_pnl !== undefined && account.total_pnl >= 0 ? '+' : ''}${account?.total_pnl?.toFixed(2) || '0.00'} USDT`}
          change={account?.total_pnl_pct || 0}
          positive={(account?.total_pnl ?? 0) >= 0}
        />
        <StatCard
          title={t('positions', language)}
          value={`${account?.position_count || 0}`}
          subtitle={`${t('margin', language)}: ${account?.margin_used_pct?.toFixed(1) || '0.0'}%`}
        />
      </div>

      {/* 主要内容区：左右分屏 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
        {/* 左侧：图表 + 持仓 */}
        <div className="space-y-6">
          {/* Equity Chart */}
          <div className="animate-slide-in" style={{ animationDelay: '0.1s' }}>
            <EquityChart traderId={selectedTrader.trader_id} />
          </div>

          {/* Current Positions */}
          <div className="binance-card p-6 animate-slide-in" style={{ animationDelay: '0.15s' }}>
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            📈 {t('currentPositions', language)}
          </h2>
          {positions && positions.length > 0 && (
            <div className="text-xs px-3 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              {positions.length} {t('active', language)}
            </div>
          )}
        </div>
        {positions && positions.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left border-b border-gray-800">
                <tr>
                  <th className="pb-3 font-semibold text-gray-400">{t('symbol', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('side', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('entryPrice', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('markPrice', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('quantity', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('positionValue', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('leverage', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('unrealizedPnL', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('liqPrice', language)}</th>
                </tr>
              </thead>
              <tbody>
                {positions.map((pos, i) => (
                  <tr key={i} className="border-b border-gray-800 last:border-0">
                    <td className="py-3 font-mono font-semibold">{pos.symbol}</td>
                    <td className="py-3">
                      <span
                        className="px-2 py-1 rounded text-xs font-bold"
                        style={pos.side === 'long'
                          ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }
                          : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' }
                        }
                      >
                        {t(pos.side === 'long' ? 'long' : 'short', language)}
                      </span>
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{pos.entry_price.toFixed(4)}</td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{pos.mark_price.toFixed(4)}</td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{pos.quantity.toFixed(4)}</td>
                    <td className="py-3 font-mono font-bold" style={{ color: '#EAECEF' }}>
                      {(pos.quantity * pos.mark_price).toFixed(2)} USDT
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#F0B90B' }}>{pos.leverage}x</td>
                    <td className="py-3 font-mono">
                      <span
                        style={{ color: pos.unrealized_pnl >= 0 ? '#0ECB81' : '#F6465D', fontWeight: 'bold' }}
                      >
                        {pos.unrealized_pnl >= 0 ? '+' : ''}
                        {pos.unrealized_pnl.toFixed(2)} ({pos.unrealized_pnl_pct.toFixed(2)}%)
                      </span>
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#848E9C' }}>
                      {pos.liquidation_price.toFixed(4)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="text-center py-16" style={{ color: '#848E9C' }}>
            <div className="text-6xl mb-4 opacity-50">📊</div>
            <div className="text-lg font-semibold mb-2">{t('noPositions', language)}</div>
            <div className="text-sm">{t('noActivePositions', language)}</div>
          </div>
        )}
          </div>
        </div>
        {/* 左侧结束 */}

        {/* 右侧：Recent Decisions - 卡片容器 */}
        <div className="binance-card p-6 animate-slide-in h-fit lg:sticky lg:top-24 lg:max-h-[calc(100vh-120px)]" style={{ animationDelay: '0.2s' }}>
          {/* 标题 */}
          <div className="flex items-center justify-between mb-5 pb-4 border-b" style={{ borderColor: '#2B3139' }}>
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl flex items-center justify-center text-xl" style={{
                background: 'linear-gradient(135deg, #6366F1 0%, #8B5CF6 100%)',
                boxShadow: '0 4px 14px rgba(99, 102, 241, 0.4)'
              }}>
                🧠
              </div>
              <div>
                <h2 className="text-xl font-bold" style={{ color: '#EAECEF' }}>{t('recentDecisions', language)}</h2>
                {decisions && decisions.length > 0 && (
                  <div className="text-xs" style={{ color: '#848E9C' }}>
                    {t('lastCycles', language, { count: decisions.length })}
                  </div>
                )}
              </div>
            </div>
            {/* AI辅助分析开关 */}
            <button
              onClick={toggleAnalysis}
              className="flex items-center gap-2 px-3 py-2 rounded text-xs sm:text-sm font-semibold transition-all hover:scale-105"
              style={analysisEnabled
                ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81', border: '1px solid #2B3139' }
                : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D', border: '1px solid #2B3139' }
              }
            >
              <span>🧠 AI辅助分析</span>
              <span className="hidden md:inline">{analysisEnabled ? t('enabled', language) : t('disabled', language)}</span>
            </button>
          </div>

          {/* 决策列表 - 可滚动 */}
          <div className="space-y-4 overflow-y-auto pr-2" style={{ maxHeight: 'calc(100vh - 280px)' }}>
            {decisions && decisions.length > 0 ? (
              decisions.map((decision, i) => (
<DecisionCard key={i} decision={decision} language={language} />
              ))
            ) : (
              <div className="py-16 text-center">
                <div className="text-6xl mb-4 opacity-30">🧠</div>
                <div className="text-lg font-semibold mb-2" style={{ color: '#EAECEF' }}>{t('noDecisionsYet', language)}</div>
                <div className="text-sm" style={{ color: '#848E9C' }}>{t('aiDecisionsWillAppear', language)}</div>
              </div>
            )}
            {/* 移除：最近决策卡片容器底部的简版建议行 */}
          </div>
        </div>
        {/* 右侧结束 */}
      </div>

      {/* AI Learning & Performance Analysis */}
      <div className="mb-6 animate-slide-in" style={{ animationDelay: '0.3s' }}>
        <AILearning traderId={selectedTrader.trader_id} />
      </div>
    </div>
  );
}

// Stat Card Component - Binance Style Enhanced
function StatCard({
  title,
  value,
  change,
  positive,
  subtitle,
}: {
  title: string;
  value: string;
  change?: number;
  positive?: boolean;
  subtitle?: string;
}) {
  return (
    <div className="stat-card animate-fade-in">
      <div className="text-xs mb-2 mono uppercase tracking-wider" style={{ color: '#848E9C' }}>{title}</div>
      <div className="text-2xl font-bold mb-1 mono" style={{ color: '#EAECEF' }}>{value}</div>
      {change !== undefined && (
        <div className="flex items-center gap-1">
          <div
            className="text-sm mono font-bold"
            style={{ color: positive ? '#0ECB81' : '#F6465D' }}
          >
            {positive ? '▲' : '▼'} {positive ? '+' : ''}
            {change.toFixed(2)}%
          </div>
        </div>
      )}
      {subtitle && <div className="text-xs mt-2 mono" style={{ color: '#848E9C' }}>{subtitle}</div>}
    </div>
  );
}

// 纯文本底部建议：仅基于 decision_json 输出“SYMBOL ACTION”行
function PlainSuggestionsFooter({ latestRecord }: { latestRecord: any }) {
  const decisionJSON: string = latestRecord?.decision_json || latestRecord?.DecisionJSON || '';
  let suggestions: any[] = [];
  try {
    if (decisionJSON && typeof decisionJSON === 'string') {
      const parsed = JSON.parse(decisionJSON);
      if (Array.isArray(parsed)) suggestions = parsed;
    }
  } catch (e) {
    suggestions = [];
  }

  if (!suggestions || suggestions.length === 0) return null;

  const normalizeAction = (a: any) => {
    const v = String(a || '').toLowerCase();
    if (v === 'buy' || v === 'long' || v === 'open_long') return 'BUY';
    if (v === 'sell' || v === 'short' || v === 'open_short') return 'SELL';
    return 'HOLD';
  };

  const line = suggestions
    .map((s: any) => `${String(s?.symbol || '-').toUpperCase()} ${normalizeAction(s?.action)}`)
    .join(' · ');

  return (
    <div className="text-xs mono" style={{ color: '#848E9C' }}>{line}</div>
  );
}

// Decision Card Component with CoT Trace - Binance Style
function DecisionCard({ decision, language }: { decision: DecisionRecord; language: Language }) {
  const [showInputPrompt, setShowInputPrompt] = useState<boolean>(false);
  const [showCoT, setShowCoT] = useState<boolean>(false);

  // 统一计算动作列表：优先使用 decisions，其次解析 decision_json，再次回退 candidate_coins 为 wait
  const buildActions = (): Array<{ symbol: string; action: string; quantity?: number; leverage?: number; price?: number; success?: boolean; error?: string }> => {
    const actions = decision?.decisions || [];
    if (actions && actions.length > 0) {
      return actions.map((a: any) => ({
        symbol: String(a?.symbol || '-'),
        action: String(a?.action || '-'),
        quantity: Number(a?.quantity || 0),
        leverage: Number(a?.leverage || 0),
        price: Number(a?.price || 0),
        success: Boolean(a?.success ?? decision?.success),
        error: a?.error,
      }));
    }

    // 尝试从 decision_json 解析
    let suggestions: any[] = [];
    try {
      const raw = (decision as any)?.decision_json;
      if (raw && typeof raw === 'string') {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) suggestions = parsed;
      }
    } catch {
      suggestions = [];
    }
    if (suggestions.length > 0) {
      return suggestions.map((s: any) => ({
        symbol: String(s?.symbol || s?.Symbol || '-'),
        action: String(s?.action || s?.Action || 'wait'),
        leverage: Number(s?.leverage || s?.Leverage || 0),
        price: Number(s?.price || s?.Price || 0),
        success: Boolean(decision?.success),
      }));
    }

    // 最后回退：使用候选币种显示为 wait ✓
    const coins: string[] = (decision as any)?.candidate_coins || (decision as any)?.CandidateCoins || [];
    if (Array.isArray(coins) && coins.length > 0) {
      return coins.map((c: any) => ({ symbol: String(c), action: 'wait', success: Boolean(decision?.success) }));
    }

    return [];
  };

  const actions = buildActions();

  // 账户摘要（若有）
  const account = (decision as any)?.account_state || (decision as any)?.AccountState;

  // 提取余额不足与数值信息（required/available）
  const logs: string[] = (decision as any)?.execution_log || [];
  const hasInsufficient = (() => {
    const keywords = [/INSUFFICIENT_BALANCE/i, /insufficient/i, /余额不足/];
    const inError = keywords.some((re) => re.test(String((decision as any)?.error_message || '')));
    const inLogs = logs.some((l) => keywords.some((re) => re.test(String(l))));
    return inError || inLogs;
  })();

  const extractRequiredAvailable = (): { required?: number; available?: number } => {
    const required = (decision as any)?.required_margin ?? (decision as any)?.RequiredMargin;
    const available = (decision as any)?.available_balance ?? (decision as any)?.AvailableBalance;
    let r = typeof required === 'number' ? required : undefined;
    let a = typeof available === 'number' ? available : undefined;
    if (r !== undefined && a !== undefined) return { required: r, available: a };
    // 从日志解析
    for (const line of logs) {
      if (r === undefined) {
        const m = line.match(/required\s*[:=]?\s*([0-9]+(?:\.[0-9]+)?)/i) || line.match(/所需\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?)/);
        if (m) r = parseFloat(m[1]);
      }
      if (a === undefined) {
        const m2 = line.match(/available\s*[:=]?\s*([0-9]+(?:\.[0-9]+)?)/i) || line.match(/可用\s*[:：]?\s*([0-9]+(?:\.[0-9]+)?)/);
        if (m2) a = parseFloat(m2[1]);
      }
      if (r !== undefined && a !== undefined) break;
    }
    return { required: r, available: a };
  };

  const parsedNumbers = extractRequiredAvailable();

  // 计算所需保证金（基于首个 open_* 动作的数量、价格和杠杆）
  const computeRequiredMargin = (): number | undefined => {
    if (!actions || actions.length === 0) return undefined;
    const open = actions.find((a) => String(a.action).toLowerCase().includes('open')) || actions[0];
    const qty = Number((open as any)?.quantity || 0);
    const price = Number(open?.price || 0);
    const lev = Number(open?.leverage || 1);
    if (qty > 0 && price > 0 && lev > 0) return (qty * price) / lev;
    return undefined;
  };

  const requiredFinal = parsedNumbers.required ?? computeRequiredMargin();
  const availableFinal = parsedNumbers.available ?? (account?.available_balance ?? account?.AvailableBalance);

  const getLogColor = (log: string): string => {
    const s = String(log);
    if (s.includes('✓') || /成功/.test(s)) return '#0ECB81';
    if (/INSUFFICIENT_BALANCE|insufficient|余额不足/i.test(s)) return '#F0B90B'; // amber
    if (/MIN_NOTIONAL|最小名义|notional/i.test(s)) return '#fb7185'; // rose
    if (/步进|increment|tick/i.test(s)) return '#a78bfa'; // violet
    if (/network|接口|超时|timeout|连接|503|500/i.test(s)) return '#60a5fa'; // blue
    return '#F6465D'; // default red
  };

  return (
    <div className="rounded p-5 transition-all duration-300 hover:translate-y-[-2px]" style={{ border: '1px solid #2B3139', background: '#1E2329', boxShadow: '0 2px 8px rgba(0, 0, 0, 0.3)' }}>
      {/* Header */}
      <div className="flex items-start justify-between mb-3">
        <div>
          <div className="font-semibold" style={{ color: '#EAECEF' }}>{t('cycle', language)} #{decision.cycle_number}</div>
          <div className="text-xs" style={{ color: '#848E9C' }}>
            {new Date((decision as any).timestamp).toLocaleString()}
          </div>
        </div>
        <div
          className="px-3 py-1 rounded text-xs font-bold"
          style={decision.success
            ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }
            : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' }
          }
        >
          {t(decision.success ? 'success' : 'failed', language)}
        </div>
      </div>

      {/* Input Prompt - Collapsible (always visible) */}
      <div className="mb-3">
        <button
          onClick={() => setShowInputPrompt(!showInputPrompt)}
          className="flex items-center gap-2 text-sm transition-colors"
          style={{ color: '#60a5fa' }}
        >
          <span className="font-semibold">📥 {t('inputPrompt', language)}</span>
          <span className="text-xs">{showInputPrompt ? t('collapse', language) : t('expand', language)}</span>
        </button>
        {showInputPrompt && (
          <div className="mt-2 rounded p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
            {(decision as any)?.input_prompt || (decision as any)?.InputPrompt || '—'}
          </div>
        )}
      </div>

      {/* AI Chain of Thought - Collapsible (always visible) */}
      <div className="mb-3">
        <button
          onClick={() => setShowCoT(!showCoT)}
          className="flex items-center gap-2 text-sm transition-colors"
          style={{ color: '#F0B90B' }}
        >
          <span className="font-semibold">📤 {t('aiThinking', language)}</span>
          <span className="text-xs">{showCoT ? t('collapse', language) : t('expand', language)}</span>
        </button>
        {showCoT && (
          <>
            {/* 仅展示 reasoning 文本：优先从 decision_json 提取，其次回退到 cot_trace */}
            <div className="mt-2 rounded p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
              {(() => {
                const raw = (decision as any)?.decision_json || (decision as any)?.DecisionJSON;
                let reasoningText = '';
                if (raw && typeof raw === 'string') {
                  try {
                    const parsed = JSON.parse(raw);
                    if (Array.isArray(parsed)) {
                      const texts = parsed
                        .map((s: any) => s?.reasoning || s?.Reasoning)
                        .filter((v: any) => typeof v === 'string' && v.trim().length > 0);
                      if (texts.length > 0) reasoningText = texts.join('\n\n');
                    }
                  } catch {
                    // ignore parse error and fall back
                  }
                }
                return reasoningText || (decision as any)?.cot_trace || (decision as any)?.CoTTrace || '—';
              })()}
            </div>
          </>
        )}
      </div>

      {/* 余额不足提示条（始终出现在动作列表区域顶部；不依赖是否有动作） */}
      {hasInsufficient && (
        <div
          className="mb-2 px-3 py-2 rounded text-xs font-semibold"
          style={{ background: 'rgba(240, 185, 11, 0.12)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.35)' }}
        >
          余额不足，已跳过开仓；所需: {requiredFinal !== undefined ? Number(requiredFinal).toFixed(2) : '—'} USDT，
          可用: {availableFinal !== undefined ? Number(availableFinal).toFixed(2) : '—'} USDT
        </div>
      )}

      {/* Decisions / Suggestions - with fallback */}
      {actions.length > 0 && !hasInsufficient && (
        <div className="space-y-2 mb-3">
          {actions.map((a, j) => (
            <div key={j} className="flex items-center gap-2 text-sm rounded px-3 py-2" style={{ background: '#0B0E11' }}>
              <span className="font-mono font-bold" style={{ color: '#EAECEF' }}>{a.symbol}</span>
              <span
                className="px-2 py-0.5 rounded text-xs font-bold"
                style={String(a.action).includes('open')
                  ? { background: 'rgba(96, 165, 250, 0.1)', color: '#60a5fa' }
                  : { background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B' }
                }
              >
                {String(a.action).toLowerCase()}
              </span>
              {(a.leverage ?? 0) > 0 && <span style={{ color: '#F0B90B' }}>{a.leverage}x</span>}
              {(a.price ?? 0) > 0 && (
                <span className="font-mono text-xs" style={{ color: '#848E9C' }}>@{Number(a.price).toFixed(4)}</span>
              )}
              <span style={{ color: a.success ? '#0ECB81' : '#F6465D' }}>
                {a.success ? '✓' : '✗'}
              </span>
              {a.error && <span className="text-xs ml-2" style={{ color: '#F6465D' }}>{a.error}</span>}
            </div>
          ))}
        </div>
      )}

      {/* 移除：DecisionCard 内部账户摘要行 */}

      {/* Execution Logs */}
      {decision.execution_log && decision.execution_log.length > 0 && (
        <div className="space-y-1">
          {decision.execution_log.map((log, k) => (
            <div
              key={k}
              className="text-xs font-mono"
              style={{ color: getLogColor(String(log)) }}
            >
              {log}
            </div>
          ))}
        </div>
      )}

      {/* 移除：DecisionCard 底部的简版建议行 */}

      {/* Error Message */}
      {decision.error_message && (
        <div className="text-sm rounded px-3 py-2 mt-3" style={{ color: '#F6465D', background: 'rgba(246, 70, 93, 0.1)' }}>
          ❌ {decision.error_message}
        </div>
      )}
    </div>
  );
}

// Wrap App with LanguageProvider
export default function AppWithLanguage() {
  return (
    <LanguageProvider>
      <App />
    </LanguageProvider>
  );
}
