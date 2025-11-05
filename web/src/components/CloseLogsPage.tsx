import useSWR from 'swr';
import { api } from '../lib/api';
import { useLanguage } from '../contexts/LanguageContext';
import { t } from '../i18n/translations';
import { useState } from 'react';

interface CloseLogsPageProps {
  traderId?: string;
}

export default function CloseLogsPage({ traderId }: CloseLogsPageProps) {
  const { language } = useLanguage();
  const [limit, setLimit] = useStateSafe(500);
  const [symbol, setSymbol] = useStateSafe<string>('');
  const [action, setAction] = useStateSafe<string>(''); // close_long/close_short
  const [success, setSuccess] = useStateSafe<string>(''); // '', 'true', 'false'
  const [start, setStart] = useStateSafe<string>('');
  const [end, setEnd] = useStateSafe<string>('');
  const [pageSize, setPageSize] = useStateSafe<number>(50);
  const [page, setPage] = useStateSafe<number>(1);

  const { data: logs, error, isLoading, mutate } = useSWR(
    traderId ? `close-logs-${traderId}-${limit}` : null,
    () => api.getCloseLogs(traderId, limit),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      dedupingInterval: 8000,
    }
  );

  // 当后端平仓明细接口失败或为空时，回退读取完整决策日志并提取 close_* 动作
  const shouldFetchFallback = (Boolean(error) || (!isLoading && (!logs || logs.length === 0))) && Boolean(traderId);
  const { data: decisionsFallback } = useSWR(
    shouldFetchFallback ? `close-logs-decisions-fallback-${traderId}` : null,
    () => api.getDecisions(traderId),
    {
      refreshInterval: 30000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  const baseLogs: any[] = (logs && logs.length > 0)
    ? (logs as any[])
    : mapCloseActionsFromDecisions(decisionsFallback || []);
  const sourceTag: 'api' | 'decisions' | undefined = (logs && logs.length > 0)
    ? 'api'
    : ((decisionsFallback && decisionsFallback.length > 0) ? 'decisions' : undefined);

  const symbols = Array.from(new Set((baseLogs || []).map((r: any) => r.symbol))).sort();
  const startMs = start ? new Date(start).getTime() : undefined;
  const endMs = end ? new Date(end).getTime() : undefined;

  const filtered = (baseLogs || []).filter((r: any) => {
    if (symbol && r.symbol !== symbol) return false;
    if (action && r.action !== action) return false;
    if (success === 'true' && r.success !== true) return false;
    if (success === 'false' && r.success !== false) return false;
    const ts = typeof r.timestamp === 'string' ? Date.parse(r.timestamp) : new Date(r.timestamp).getTime();
    if (startMs && ts < startMs) return false;
    if (endMs && ts > endMs) return false;
    return true;
  }).sort((a: any, b: any) => {
    const ta = typeof a.timestamp === 'string' ? Date.parse(a.timestamp) : new Date(a.timestamp).getTime();
    const tb = typeof b.timestamp === 'string' ? Date.parse(b.timestamp) : new Date(b.timestamp).getTime();
    return tb - ta;
  });

  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const clampedPage = Math.min(Math.max(1, page), totalPages);
  const startIdx = (clampedPage - 1) * pageSize;
  const paged = filtered.slice(startIdx, startIdx + pageSize);

  return (
    <div className="binance-card p-6 animate-slide-in">
      <div className="flex items-center justify-between mb-5">
        <div className="flex items-center gap-2">
          <h2 className="text-xl font-bold" style={{ color: '#EAECEF' }}>平仓明细日志</h2>
          {baseLogs && (
            <span className="text-xs px-2 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              {baseLogs.length}{sourceTag === 'decisions' ? '（回退：决策日志）' : ''}
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
            {[200, 500, 1000].map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
          <button
            onClick={() => mutate()}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid #2B3139' }}
          >刷新</button>
          <button
            onClick={() => exportCSV(paged)}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(14, 203, 129, 0.12)', color: '#0ECB81', border: '1px solid #2B3139' }}
          >导出CSV</button>
          <button
            onClick={() => exportJSON(paged)}
            className="px-3 py-1.5 rounded text-xs font-semibold transition-all hover:scale-105"
            style={{ background: 'rgba(132, 142, 156, 0.12)', color: '#EAECEF', border: '1px solid #2B3139' }}
          >导出JSON</button>
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
          value={action}
          onChange={(e) => { setAction(e.target.value); setPage(1); }}
          className="rounded px-2 py-1.5 text-xs cursor-pointer"
          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">全部动作</option>
          <option value="close_long">平多</option>
          <option value="close_short">平空</option>
        </select>
        <select
          value={success}
          onChange={(e) => { setSuccess(e.target.value); setPage(1); }}
          className="rounded px-2 py-1.5 text-xs cursor-pointer"
          style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
        >
          <option value="">全部结果</option>
          <option value="true">成功</option>
          <option value="false">失败</option>
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
          onClick={() => { setSymbol(''); setAction(''); setSuccess(''); setStart(''); setEnd(''); setPage(1); }}
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
          已使用回退：平仓明细接口不可用或为空，按最近决策中的平仓动作生成列表。
        </div>
      )}

      {paged && paged.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left border-b border-gray-800">
              <tr>
                <th className="pb-3 font-semibold text-gray-400">时间</th>
                <th className="pb-3 font-semibold text-gray-400">交易对</th>
                <th className="pb-3 font-semibold text-gray-400">动作</th>
                <th className="pb-3 font-semibold text-gray-400">数量</th>
                <th className="pb-3 font-semibold text-gray-400">价格</th>
                <th className="pb-3 font-semibold text-gray-400">结果</th>
                <th className="pb-3 font-semibold text-gray-400">错误信息</th>
              </tr>
            </thead>
            <tbody>
              {paged.map((r: any, i: number) => (
                <tr key={i} className="border-b border-gray-800 last:border-0">
                  <td className="py-2 text-xs" style={{ color: '#848E9C' }}>{formatTs(r.timestamp)}</td>
                  <td className="py-2 font-mono font-semibold">{r.symbol}</td>
                  <td className="py-2">
                    <span className="px-2 py-1 rounded text-xs font-semibold" style={{ background: 'rgba(43, 49, 57, 0.6)', color: '#EAECEF', border: '1px solid #2B3139' }}>{r.action}</span>
                  </td>
                  <td className="py-2 font-mono">{number(r.quantity)}</td>
                  <td className="py-2 font-mono">{number(r.price)}</td>
                  <td className="py-2">
                    <span className="px-2 py-1 rounded text-xs font-semibold" style={{ background: r.success ? 'rgba(14, 203, 129, 0.12)' : 'rgba(246, 70, 93, 0.12)', color: r.success ? '#0ECB81' : '#F6465D', border: '1px solid #2B3139' }}>{r.success ? '成功' : '失败'}</span>
                  </td>
                  <td className="py-2 text-xs" style={{ color: '#94A3B8' }}>{r.error || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
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
          <div className="text-6xl mb-4 opacity-30">📘</div>
          <div className="text-lg font-semibold" style={{ color: '#EAECEF' }}>暂无平仓明细</div>
          <div className="text-sm" style={{ color: '#848E9C' }}>当有平仓动作时，此处显示详细记录。</div>
        </div>
      )}
    </div>
  );
}

function useStateSafe<T>(initial: T): [T, (val: T) => void] {
  const [val, setVal] = useState(initial);
  return [val, (v: T) => { try { setVal(v); } catch {} }];
}

function number(n: any) {
  if (typeof n === 'number') return n.toFixed(4);
  const v = Number(n);
  if (isNaN(v)) return '-';
  if (Math.abs(v) >= 1) return v.toFixed(4);
  return v.toFixed(6);
}

function formatTs(ts: any) {
  const d = typeof ts === 'string' ? new Date(ts) : new Date(ts);
  if (!d || isNaN(d.getTime())) return '-';
  return d.toLocaleString();
}

function exportJSON(rows: any[]) {
  const blob = new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `close_logs_${Date.now()}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function exportCSV(rows: any[]) {
  const headers = ['time', 'symbol', 'action', 'quantity', 'price', 'success', 'error'];
  const lines = [headers.join(',')];
  for (const r of rows) {
    const line = [
      (typeof r.timestamp === 'string' ? new Date(r.timestamp) : new Date(r.timestamp)).toISOString(),
      String(r.symbol ?? ''),
      String(r.action ?? ''),
      String(r.quantity ?? ''),
      String(r.price ?? ''),
      String(r.success ?? ''),
      (r.error ?? '').replace(/\n/g, ' '),
    ].map(v => String(v).replace(/,/g, ''))
     .join(',');
    lines.push(line);
  }
  const blob = new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `close_logs_${Date.now()}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

// 从决策日志中提取 close_* 动作以生成平仓明细回退列表
function mapCloseActionsFromDecisions(records: any[]): any[] {
  if (!records || records.length === 0) return [];
  const rows: any[] = [];
  for (const rec of records) {
    const actions = Array.isArray(rec?.decisions) ? rec.decisions : [];
    for (const a of actions) {
      const act = String(a?.action || '').toLowerCase();
      if (!act.includes('close')) continue;
      const tsStr = a?.timestamp || rec?.timestamp;
      const ts = tsStr ? new Date(tsStr).toISOString() : new Date().toISOString();
      rows.push({
        timestamp: ts,
        symbol: a?.symbol || '',
        action: act,
        quantity: a?.quantity ?? undefined,
        price: a?.price ?? undefined,
        success: Boolean(a?.success),
        error: a?.error || '',
      });
    }
  }
  return rows;
}