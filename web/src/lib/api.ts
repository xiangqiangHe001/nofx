import type {
  SystemStatus,
  AccountInfo,
  Position,
  DecisionRecord,
  Statistics,
  TraderInfo,
  CompetitionData,
} from '../types';

const API_BASE = '/api';

export const api = {
  // 竞赛相关接口
  async getCompetition(): Promise<CompetitionData> {
    const res = await fetch(`${API_BASE}/competition`);
    if (!res.ok) throw new Error('获取竞赛数据失败');
    return res.json();
  },

  async getTraders(): Promise<TraderInfo[]> {
    const res = await fetch(`${API_BASE}/traders`);
    if (!res.ok) throw new Error('获取trader列表失败');
    return res.json();
  },

  // 获取系统状态（支持trader_id）
  async getStatus(traderId?: string): Promise<SystemStatus> {
    const url = traderId
      ? `${API_BASE}/status?trader_id=${traderId}`
      : `${API_BASE}/status`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取系统状态失败');
    return res.json();
  },

  // 获取账户信息（支持trader_id）
  async getAccount(traderId?: string): Promise<AccountInfo> {
    const url = traderId
      ? `${API_BASE}/account?trader_id=${traderId}`
      : `${API_BASE}/account`;
    const res = await fetch(url, {
      cache: 'no-store',
      headers: {
        'Cache-Control': 'no-cache',
      },
    });
    if (!res.ok) throw new Error('获取账户信息失败');
    const data = await res.json();
    console.log('Account data fetched:', data);
    return data;
  },

  // 获取持仓列表（支持trader_id）
  async getPositions(traderId?: string): Promise<Position[]> {
    const url = traderId
      ? `${API_BASE}/positions?trader_id=${traderId}`
      : `${API_BASE}/positions`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取持仓列表失败');
    return res.json();
  },

  // 获取决策日志（支持trader_id）
  async getDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions?trader_id=${traderId}`
      : `${API_BASE}/decisions`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取决策日志失败');
    return res.json();
  },

  // 获取最新决策（支持trader_id）
  async getLatestDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions/latest?trader_id=${traderId}`
      : `${API_BASE}/decisions/latest`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取最新决策失败');
    return res.json();
  },

  // 获取统计信息（支持trader_id）
  async getStatistics(traderId?: string): Promise<Statistics> {
    const url = traderId
      ? `${API_BASE}/statistics?trader_id=${traderId}`
      : `${API_BASE}/statistics`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取统计信息失败');
    return res.json();
  },

  // 获取收益率历史数据（支持trader_id）
  async getEquityHistory(traderId?: string): Promise<any[]> {
    const url = traderId
      ? `${API_BASE}/equity-history?trader_id=${traderId}`
      : `${API_BASE}/equity-history`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取历史数据失败');
    return res.json();
  },

  // 获取AI学习表现分析（支持trader_id）
  async getPerformance(traderId?: string): Promise<any> {
    const url = traderId
      ? `${API_BASE}/performance?trader_id=${traderId}`
      : `${API_BASE}/performance`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取AI学习数据失败');
    return res.json();
  },

  // 获取自动执行开关状态
  async getExecutionStatus(traderId?: string): Promise<{ execution_enabled: boolean }> {
    const url = traderId
      ? `${API_BASE}/execution?trader_id=${traderId}`
      : `${API_BASE}/execution`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('获取执行开关状态失败');
    return res.json();
  },

  // 设置自动执行开关
  async setExecution(enabled: boolean, traderId?: string): Promise<{ execution_enabled: boolean }> {
    const url = traderId
      ? `${API_BASE}/execution?trader_id=${traderId}`
      : `${API_BASE}/execution`;
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    });
    if (!res.ok) throw new Error('设置执行开关失败');
    return res.json();
  },

  // OKX 原始数据接口（按需使用）
  async getOkxAccountRaw(traderId?: string): Promise<any> {
    const url = traderId
      ? `${API_BASE}/okx/account/raw?trader_id=${traderId}`
      : `${API_BASE}/okx/account/raw`;
    const res = await fetch(url, {
      cache: 'no-store',
      headers: { 'Cache-Control': 'no-cache' },
    });
    if (!res.ok) throw new Error('获取 OKX 原始账户数据失败');
    return res.json();
  },

  async getOkxPositionsRaw(traderId?: string): Promise<any> {
    const url = traderId
      ? `${API_BASE}/okx/positions/raw?trader_id=${traderId}`
      : `${API_BASE}/okx/positions/raw`;
    const res = await fetch(url, { cache: 'no-store' });
    if (!res.ok) throw new Error('获取 OKX 原始持仓数据失败');
    return res.json();
  },

  async getOkxOrders(instId?: string, traderId?: string): Promise<any> {
    const params = new URLSearchParams();
    if (instId) params.set('instId', instId);
    if (traderId) params.set('trader_id', traderId);
    const query = params.toString();
    const url = query
      ? `${API_BASE}/okx/orders?${query}`
      : `${API_BASE}/okx/orders`;
    const res = await fetch(url, { cache: 'no-store' });
    if (!res.ok) throw new Error('获取 OKX 原始委托数据失败');
    return res.json();
  },
};
