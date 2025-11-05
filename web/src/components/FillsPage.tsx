import useSWR from 'swr';
import { api } from '../lib/api';
import { useLanguage } from '../contexts/LanguageContext';
import { t } from '../i18n/translations';

interface FillsPageProps {
  traderId?: string;
}

export default function FillsPage({ traderId }: FillsPageProps) {
  const { language } = useLanguage();
  const [limit, setLimit] = useStateSafe(200);
  // 筛选与分页状态
  const [symbol, setSymbol] = useStateSafe<string>('');
  const [side, setSide] = useStateSafe<string>(''); // buy/sell
  const [posSide, setPosSide] = useStateSafe<string>(''); // long/short
  const [start, setStart] = useStateSafe<string>(''); // datetime-local
  const [end, setEnd] = useStateSafe<string>('');
  const [pageSize, setPageSize] = useStateSafe<number>(50);
  const [page, setPage] = useStateSafe<number>(1);

  const { data: fills, error, isLoading, mutate } = useSWR(
    traderId ? `okx-fills-${traderId}-${limit}` : null,
    () => api.getOkxFills(traderId, limit),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      dedupingInterval: 8000,
    }
  );

  // 当 OKX 成交拉取失败或为空时，回退读取决策日志构造成交视图
  const shouldFetchFallback = (Boolean(error) || (!isLoading && (!fills || fills.length === 0))) && Boolean(traderId);
  const { data: decisionsFallback } = useSWR(
    shouldFetchFallback ? `decisions-fallback-${traderId}` : null,
    () => api.getDecisions(traderId),
    {
      refreshInterval: 30000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  // 基础数据源：优先使用 OKX 原始成交；否则由决策动作映射生成
  const baseRows: any[] = (fills && fills.length > 0)
    ? (fills as any[])
    : mapDecisionsToFills(decisionsFallback || []);
  const sourceTag: 'okx' | 'decisions' | undefined = (fills && fills.length > 0)
    ? 'okx'
    : ((decisionsFallback && decisionsFallback.length > 0) ? 'decisions' : undefined);

  // 选项与过滤
  const symbols = Array.from(
    new Set((baseRows || []).map((f: any) => f.symbol || f.inst_id))
  ).sort();

  const startMs = start ? new Date(start).getTime() : undefined;
  const endMs = end ? new Date(end).getTime() : undefined;

  const filtered = (baseRows || []).filter((f: any) => {
    const sym = f.symbol || f.inst_id;
    const ts = typeof f.timestamp === 'string' ? Number(f.timestamp) : f.timestamp;
    if (symbol && sym !== symbol) return false;
    if (side && String(f.side).toLowerCase() !== side) return false;
    if (posSide && String(f.pos_side).toLowerCase() !== posSide) return false;
    if (startMs && ts < startMs) return false;
    if (endMs && ts > endMs) return false;
    return true;
  }).sort((a: any, b: any) => {
    const ta = typeof a.timestamp === 'string' ? Number(a.timestamp) : a.timestamp;
    const tb = typeof b.timestamp === 'string' ? Number(b.timestamp) : b.timestamp;
    return tb - ta; // 时间倒序
  });

  // 分页
  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const clampedPage = Math.min(Math.max(1, page), totalPages);
  const startIdx = (clampedPage - 1) * pageSize;
  const paged = filtered.slice(startIdx, startIdx + pageSize);

  return (
    <div className="binance-card p-6 animate-slide-in">
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-2">
          <h2 className="text-xl font-bold" style={{ color: '#EAECEF' }}>{t('tradeHistory', language)}</h2>
          {baseRows && (
            <span className="text-xs px-2 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              {baseRows.length}{sourceTag === 'decisions' ? '（回退：决策日志）' : ''}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <select
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value))}
            className="rounded px-2 py-1.5 text-xs cursor-pointer"
            style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
          >
            {[50, 100, 200, 500].map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
          <button
            onClick={() => mutate()}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid #2B3139' }}
          >
            刷新
          </button>
          <button
            onClick={() => exportCSV(filtered)}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(14, 203, 129, 0.12)', color: '#0ECB81', border: '1px solid #2B3139' }}
          >
            导出CSV
          </button>
          <button
            onClick={() => exportJSON(filtered)}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(132, 142, 156, 0.12)', color: '#EAECEF', border: '1px solid #2B3139' }}
          >
            导出JSON
          </button>
        </div>
      </div>

      {/* Filters */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3 mb-4">
        <select
          value={symbol}
          onChange={(e) => { setSymbol(e.target.value); setPage(1); }}
          className="rounded px-2 py-1.5 text-xs cursor-pointer"
          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">全部交易对</option>
          {symbols.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
        <select
          value={side}
          onChange={(e) => { setSide(e.target.value); setPage(1); }}
          className="rounded px-2 py-1.5 text-xs cursor-pointer"
          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">全部方向</option>
          <option value="buy">买入</option>
          <option value="sell">卖出</option>
        </select>
        <select
          value={posSide}
          onChange={(e) => { setPosSide(e.target.value); setPage(1); }}
          className="rounded px-2 py-1.5 text-xs cursor-pointer"
          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">全部多空</option>
          <option value="long">多仓</option>
          <option value="short">空仓</option>
        </select>
        <div className="flex gap-2">
          <input
            type="datetime-local"
            value={start}
            onChange={(e) => { setStart(e.target.value); setPage(1); }}
            className="rounded px-2 py-1.5 text-xs flex-1"
            style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
          />
          <input
            type="datetime-local"
            value={end}
            onChange={(e) => { setEnd(e.target.value); setPage(1); }}
            className="rounded px-2 py-1.5 text-xs flex-1"
            style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
          />
        </div>
      </div>
      <div className="flex items-center justify-between mb-3">
        <button
          onClick={() => { setSymbol(''); setSide(''); setPosSide(''); setStart(''); setEnd(''); setPage(1); }}
          className="px-3 py-1.5 rounded text-xs font-semibold"
          style={{ background: 'rgba(43, 49, 57, 0.6)', color: '#EAECEF', border: '1px solid #2B3139' }}
        >清空筛选</button>
        <div className="flex items-center gap-2">
          <span className="text-xs" style={{ color: '#848E9C' }}>每页</span>
          <select
            value={pageSize}
            onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}
            className="rounded px-2 py-1.5 text-xs"
            style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
          >
            {[20, 50, 100, 200].map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        </div>
      </div>

      {isLoading && (
        <div className="text-sm" style={{ color: '#848E9C' }}>{t('loading', language)}</div>
      )}
      {error && (
        <div className="text-sm" style={{ color: '#F6465D' }}>{t('loadingError', language)}</div>
      )}
      {sourceTag === 'decisions' && (
        <div className="text-xs mt-2" style={{ color: '#F0B90B' }}>
          已使用回退：OKX成交不可用或为空，按最近决策动作生成列表。
        </div>
      )}

      {paged && paged.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left border-b border-gray-800">
              <tr>
                <th className="pb-3 font-semibold text-gray-400">时间</th>
                <th className="pb-3 font-semibold text-gray-400">交易对</th>
                <th className="pb-3 font-semibold text-gray-400">方向</th>
                <th className="pb-3 font-semibold text-gray-400">多空</th>
                <th className="pb-3 font-semibold text-gray-400">价格</th>
                <th className="pb-3 font-semibold text-gray-400">数量</th>
                <th className="pb-3 font-semibold text-gray-400">合约张数</th>
                <th className="pb-3 font-semibold text-gray-400">订单ID</th>
              </tr>
            </thead>
            <tbody>
              {paged.map((f: any, i: number) => (
                <tr key={i} className="border-b border-gray-800 last:border-0">
                  <td className="py-2 text-xs" style={{ color: '#848E9C' }}>{formatTs(f.timestamp)}</td>
                  <td className="py-2 font-mono font-semibold">{f.symbol || f.inst_id}</td>
                  <td className="py-2">
                    <span className="px-2 py-1 rounded text-xs font-semibold" style={{ background: 'rgba(240, 185, 11, 0.06)', color: '#EAECEF', border: '1px solid #2B3139' }}>{f.side?.toUpperCase()}</span>
                  </td>
                  <td className="py-2">
                    <span className="px-2 py-1 rounded text-xs font-semibold" style={{ background: 'rgba(43, 49, 57, 0.6)', color: f.pos_side === 'long' ? '#0ECB81' : '#F6465D', border: '1px solid #2B3139' }}>{f.pos_side}</span>
                  </td>
                  <td className="py-2 font-mono">{number(f.price)}</td>
                  <td className="py-2 font-mono">{number(f.quantity)}</td>
                  <td className="py-2 font-mono text-xs" style={{ color: '#848E9C' }}>{number(f.contracts)}</td>
                  <td className="py-2 font-mono text-xs" style={{ color: '#94A3B8' }}>{f.trade_id}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {/* Pagination */}
          <div className="flex items-center justify-between mt-3">
            <div className="text-xs" style={{ color: '#848E9C' }}>
              共 {filtered.length} 条，页 {clampedPage}/{totalPages}
            </div>
            <div className="flex items-center gap-2">
              <button
                disabled={clampedPage <= 1}
                onClick={() => setPage(clampedPage - 1)}
                className="px-3 py-1.5 rounded text-xs font-semibold disabled:opacity-50"
                style={{ background: 'rgba(43, 49, 57, 0.6)', color: '#EAECEF', border: '1px solid #2B3139' }}
              >上一页</button>
              <button
                disabled={clampedPage >= totalPages}
                onClick={() => setPage(clampedPage + 1)}
                className="px-3 py-1.5 rounded text-xs font-semibold disabled:opacity-50"
                style={{ background: 'rgba(43, 49, 57, 0.6)', color: '#EAECEF', border: '1px solid #2B3139' }}
              >下一页</button>
            </div>
          </div>
        </div>
      ) : (
        <div className="py-16 text-center">
          <div className="text-6xl mb-4 opacity-30">📜</div>
          <div className="text-lg font-semibold" style={{ color: '#EAECEF' }}>暂无成交记录</div>
          <div className="text-sm" style={{ color: '#848E9C' }}>如果你正在使用OKX交易，将在此显示历史成交。</div>
        </div>
      )}
    </div>
  );
}

function useStateSafe<T>(initial: T): [T, (val: T) => void] {
  const [val, setVal] = useState(initial);
  return [val, (v: T) => {
    try { setVal(v); } catch { /* ignore */ }
  }];
}

function number(n: any) {
  if (typeof n === 'number') return n.toFixed(4);
  const v = Number(n);
  if (isNaN(v)) return '-';
  if (Math.abs(v) >= 1) return v.toFixed(4);
  return v.toFixed(6);
}

function formatTs(ts: any) {
  // OKX返回毫秒字符串
  const ms = typeof ts === 'string' ? Number(ts) : ts;
  if (!ms || isNaN(ms)) return '-';
  const d = new Date(ms);
  return d.toLocaleString();
}

import { useState } from 'react';

// 将决策动作映射为成交行（用于回退显示）
function mapDecisionsToFills(records: any[]): any[] {
  if (!records || records.length === 0) return [];
  const rows: any[] = [];
  for (const rec of records) {
    const actions = Array.isArray(rec?.decisions) ? rec.decisions : [];
    for (const a of actions) {
      const actionStr = String(a?.action || '').toLowerCase();
      const isOpen = actionStr.includes('open');
      const isLong = actionStr.includes('long');
      const side = actionStr
        ? (isOpen ? (isLong ? 'buy' : 'sell') : (isLong ? 'sell' : 'buy'))
        : '';
      const pos_side = isLong ? 'long' : (actionStr.includes('short') ? 'short' : '');
      const tsStr = a?.timestamp || rec?.timestamp;
      const ts = tsStr ? Date.parse(tsStr) : Date.now();
      rows.push({
        timestamp: ts,
        symbol: a?.symbol || '',
        inst_id: a?.symbol || '',
        side,
        pos_side,
        price: a?.price ?? undefined,
        quantity: a?.quantity ?? undefined,
        contracts: a?.quantity ?? undefined,
        trade_id: a?.order_id ?? `${rec?.cycle_number || ''}-${a?.symbol || ''}-${ts}`,
      });
    }
  }
  return rows;
}

// 导出工具
function exportJSON(rows: any[]) {
  const blob = new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `fills_${Date.now()}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function exportCSV(rows: any[]) {
  const headers = ['time', 'symbol', 'side', 'pos_side', 'price', 'quantity', 'contracts', 'trade_id'];
  const lines = [headers.join(',')];
  for (const f of rows) {
    const line = [
      new Date(typeof f.timestamp === 'string' ? Number(f.timestamp) : f.timestamp).toISOString(),
      (f.symbol || f.inst_id) ?? '',
      (f.side ?? '').toString(),
      (f.pos_side ?? '').toString(),
      String(f.price ?? ''),
      String(f.quantity ?? ''),
      String(f.contracts ?? ''),
      String(f.trade_id ?? ''),
    ].map(v => String(v).replace(/,/g, '')) // 简单移除逗号避免列错位
     .join(',');
    lines.push(line);
  }
  const blob = new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `fills_${Date.now()}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}