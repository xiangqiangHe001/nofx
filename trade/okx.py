import os
import time
import schedule
from openai import OpenAI
import ccxt
import pandas as pd
from datetime import datetime
import json
import re
from dotenv import load_dotenv
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

load_dotenv()

# 代理服务器配置
PROXY_CONFIG = {
    'http': os.getenv('HTTP_PROXY', 'http://127.0.0.1:7897'),
    'https': os.getenv('HTTPS_PROXY', 'http://127.0.0.1:7897'),
}


# 初始化DeepSeek客户端
def setup_deepseek_client():
    """设置带代理的DeepSeek客户端"""
    try:
        client = OpenAI(
            api_key=os.getenv('DEEPSEEK_API_KEY'),
            base_url="https://api.deepseek.com",
            http_client=None
        )

        if PROXY_CONFIG['http'] and PROXY_CONFIG['http'] != 'http://your_proxy_server:port':
            os.environ['HTTP_PROXY'] = PROXY_CONFIG['http']
        if PROXY_CONFIG['https'] and PROXY_CONFIG['https'] != 'http://your_proxy_server:port':
            os.environ['HTTPS_PROXY'] = PROXY_CONFIG['https']

        print("✅ DeepSeek客户端代理设置完成")
        return client
    except Exception as e:
        print(f"❌ DeepSeek客户端初始化失败: {e}")
        return None


# 初始化OKX交易所
def setup_okx_exchange():
    """设置带代理的OKX交易所"""
    try:
        exchange_config = {
            'options': {
                'defaultType': 'swap',
            },
            'apiKey': os.getenv('OKX_API_KEY'),
            'secret': os.getenv('OKX_SECRET'),
            'password': os.getenv('OKX_PASSWORD'),
            'enableRateLimit': True,
            'timeout': 30000
        }

        if (PROXY_CONFIG['http'] and PROXY_CONFIG['http'] != 'http://your_proxy_server:port' and
                PROXY_CONFIG['https'] and PROXY_CONFIG['https'] != 'http://your_proxy_server:port'):
            exchange_config['proxies'] = {
                'http': PROXY_CONFIG['http'],
                'https': PROXY_CONFIG['https'],
            }
            print("✅ CCXT代理设置完成")
        else:
            print("⚠️ 使用直接连接，代理配置无效")

        exchange = ccxt.okx(exchange_config)
        exchange.publicGetPublicTime()
        print("✅ OKX交易所连接测试通过")
        return exchange
    except Exception as e:
        print(f"❌ OKX交易所初始化失败: {e}")
        return None


# 初始化客户端和交易所
deepseek_client = setup_deepseek_client()
exchange = setup_okx_exchange()

# 交易参数配置 - 针对10美元本金优化
TRADE_CONFIG = {
    'symbol': 'OKB/USDT:USDT',
    'target_notional': 5.0,
    'leverage': 10,
    'timeframe': '5m',
    'test_mode': False,
    'data_points': 96,
    'max_position_value': 100.0,
}

# 全局变量
price_history = []
signal_history = []
position = None


def get_contract_specs(inst_id='OKB-USDT-SWAP'):
    """获取合约规格信息"""
    try:
        print(f"📋 获取合约规格信息: {inst_id}")
        instruments = exchange.publicGetPublicInstruments({
            'instType': 'SWAP',
            'instId': inst_id
        })

        if instruments and instruments.get('code') == '0':
            data = instruments.get('data', [])
            if data:
                spec = data[0]
                return {
                    'min_size': float(spec.get('minSz', '0.01')),
                    'size_increment': float(spec.get('lotSz', '0.0001')),
                    'contract_value': float(spec.get('ctVal', '0.1')),
                }
    except Exception as e:
        print(f"⚠️ 获取合约规格失败: {e}")

    return {
        'min_size': 0.01,
        'size_increment': 0.0001,
        'contract_value': 0.1,
    }


def calculate_position_size(price, target_notional=5.0):
    """根据目标价值和价格计算仓位大小"""
    try:
        contract_spec = get_contract_specs('OKB-USDT-SWAP')
        contract_value = contract_spec['contract_value']

        # 计算需要的合约张数：目标价值 / (合约面值 * 价格)
        contracts_needed = target_notional / (contract_value * price)

        # 调整到符合合约规格
        min_size = contract_spec['min_size']
        size_increment = contract_spec['size_increment']

        # 确保不小于最小交易量
        adjusted_contracts = max(contracts_needed, min_size)

        # 调整到增量倍数
        if size_increment > 0:
            adjusted_contracts = round(adjusted_contracts / size_increment) * size_increment

        # 计算实际开仓价值
        actual_notional = adjusted_contracts * contract_value * price
        required_margin = actual_notional / TRADE_CONFIG['leverage']

        print(f"🎯 仓位计算: 目标{target_notional}USD, 价格${price:.2f}")
        print(f"📊 合约面值: {contract_value}OKB, 需要{adjusted_contracts:.4f}张合约")
        print(f"💰 实际开仓价值: ${actual_notional:.2f}, 所需保证金: ${required_margin:.4f}")

        return adjusted_contracts, actual_notional, required_margin

    except Exception as e:
        print(f"❌ 仓位计算失败: {e}")
        # 返回默认值
        default_contracts = 0.01
        default_notional = default_contracts * 0.1 * price
        default_margin = default_notional / TRADE_CONFIG['leverage']
        return default_contracts, default_notional, default_margin


def get_usdt_balance():
    """获取USDT余额"""
    try:
        # 方法1: 使用资金账户API
        try:
            funding_balance = exchange.privateGetAssetBalances({'ccy': 'USDT'})
            if funding_balance and funding_balance.get('code') == '0':
                data = funding_balance.get('data', [])
                for item in data:
                    if item.get('ccy') == 'USDT':
                        bal = float(item.get('bal', 0))
                        if bal > 0:
                            print(f"✅ 资金账户余额: {bal:.2f} USDT")
                            return bal
        except Exception as e:
            print(f"⚠️ 资金账户查询失败: {e}")

        # 方法2: 使用账户余额API
        try:
            account_balance = exchange.privateGetAccountBalance()
            if account_balance and account_balance.get('code') == '0':
                data = account_balance.get('data', [])
                if data:
                    details = data[0].get('details', [])
                    for detail in details:
                        if detail.get('ccy') == 'USDT':
                            avail_bal = float(detail.get('availBal', 0))
                            if avail_bal > 0:
                                print(f"✅ 可用余额: {avail_bal:.2f} USDT")
                                return avail_bal
        except Exception as e:
            print(f"⚠️ 账户余额查询失败: {e}")

        print("⚠️ 使用默认余额10 USDT")
        return 10.0

    except Exception as e:
        print(f"❌ 余额查询失败: {e}")
        return 10.0


def get_current_position():
    """获取当前持仓情况 - 修复版本"""
    try:
        print("📦 查询当前持仓...")
        positions_response = exchange.privateGetAccountPositions({'instType': 'SWAP'})

        if positions_response.get('code') == '0':
            positions_data = positions_response.get('data', [])
            for pos in positions_data:
                if pos.get('instId') == 'OKB-USDT-SWAP':
                    contracts = float(pos.get('pos', 0))
                    # 更严格的持仓检查：持仓数量必须大于最小交易量
                    min_size = get_contract_specs().get('min_size', 0.001)
                    if abs(contracts) > min_size:
                        position_info = {
                            'side': 'long' if contracts > 0 else 'short',
                            'size': abs(contracts),
                            'entry_price': float(pos.get('avgPx', 0)),
                            'unrealized_pnl': float(pos.get('upl', 0)),
                            'leverage': float(pos.get('lever', TRADE_CONFIG['leverage'])),
                            'state': pos.get('state', 'live')  # 添加持仓状态
                        }

                        # 检查持仓状态是否有效
                        if position_info['state'] in ['live', 'normal']:
                            print(f"✅ 当前持仓: {position_info}")
                            return position_info
                        else:
                            print(f"⚠️ 持仓状态无效: {position_info['state']}")
                            return None
                    else:
                        print(f"⚠️ 持仓数量过小: {contracts}, 最小要求: {min_size}")

        print("📦 当前无有效持仓")
        return None
    except Exception as e:
        print(f"❌ 获取持仓失败: {e}")
        return None


def cancel_all_open_orders():
    """取消所有未成交订单"""
    try:
        print("🗑️ 取消所有未成交订单...")

        # 获取当前所有未成交订单
        open_orders = exchange.fetch_open_orders(symbol='OKB/USDT:USDT')
        if open_orders:
            print(f"📋 发现 {len(open_orders)} 个未成交订单")

            for order in open_orders:
                try:
                    cancel_response = exchange.cancel_order(order['id'], symbol='OKB/USDT:USDT')
                    if cancel_response:
                        print(f"✅ 已取消订单: {order['id']}")
                    time.sleep(0.5)  # 避免频率限制
                except Exception as e:
                    print(f"⚠️ 取消订单 {order['id']} 失败: {e}")
        else:
            print("✅ 没有未成交订单需要取消")

    except Exception as e:
        print(f"❌ 取消订单操作失败: {e}")


def close_all_positions():
    """平掉所有持仓 - 修复版本"""
    try:
        print("📦 检查并平掉所有持仓...")

        # 获取当前持仓
        positions_response = exchange.privateGetAccountPositions({'instType': 'SWAP'})

        if positions_response.get('code') == '0':
            positions_data = positions_response.get('data', [])
            positions_to_close = []

            for pos in positions_data:
                if pos.get('instId') == 'OKB-USDT-SWAP':
                    contracts = float(pos.get('pos', 0))
                    # 更严格的持仓检查
                    min_size = get_contract_specs().get('min_size', 0.001)
                    if abs(contracts) > min_size:
                        positions_to_close.append({
                            'instId': pos.get('instId'),
                            'side': 'long' if contracts > 0 else 'short',
                            'size': abs(contracts),
                            'posSide': 'long' if contracts > 0 else 'short',
                            'state': pos.get('state', 'live')
                        })

            if positions_to_close:
                print(f"📦 发现 {len(positions_to_close)} 个持仓需要平仓")

                for position in positions_to_close:
                    try:
                        # 检查持仓状态是否有效
                        if position['state'] not in ['live', 'normal']:
                            print(f"⚠️ 持仓状态无效，跳过平仓: {position}")
                            continue

                        # 平仓操作 - 修复参数
                        close_params = {
                            'instId': position['instId'],
                            'tdMode': 'isolated',
                            'side': 'buy' if position['side'] == 'short' else 'sell',
                            'posSide': position['posSide'],
                            'ordType': 'market',
                            'sz': str(position['size']),
                            'reduceOnly': True  # 添加只减仓标志
                        }

                        print(f"🔄 平仓参数: {close_params}")

                        close_response = exchange.privatePostTradeOrder(close_params)
                        if close_response.get('code') == '0':
                            print(f"✅ 已平仓: {position['instId']} {position['side']} {position['size']}")
                        else:
                            print(f"⚠️ 平仓失败: {close_response}")
                            # 如果平仓失败，尝试使用CCXT标准方法
                            try:
                                print("🔄 尝试使用CCXT标准方法平仓...")
                                if position['side'] == 'long':
                                    # 平多仓
                                    exchange.create_order(
                                        symbol='OKB/USDT:USDT',
                                        type='market',
                                        side='sell',
                                        amount=position['size'],
                                        params={'reduceOnly': True}
                                    )
                                else:
                                    # 平空仓
                                    exchange.create_order(
                                        symbol='OKB/USDT:USDT',
                                        type='market',
                                        side='buy',
                                        amount=position['size'],
                                        params={'reduceOnly': True}
                                    )
                                print("✅ CCXT标准平仓成功")
                            except Exception as e2:
                                print(f"❌ CCXT标准平仓也失败: {e2}")

                        time.sleep(1)  # 避免频率限制
                    except Exception as e:
                        print(f"❌ 平仓操作失败: {e}")
            else:
                print("✅ 没有持仓需要平仓")

    except Exception as e:
        print(f"❌ 平仓操作失败: {e}")


def cleanup_before_setup():
    """在设置持仓模式前清理所有订单和持仓"""
    try:
        print("🔄 开始清理现有订单和持仓...")

        # 取消所有未成交订单
        cancel_all_open_orders()
        time.sleep(2)  # 等待订单取消完成

        # 平掉所有持仓
        close_all_positions()
        time.sleep(3)  # 等待平仓完成

        # 再次检查持仓状态
        time.sleep(2)
        current_position = get_current_position()
        if current_position is None:
            print("✅ 确认所有持仓已平仓")
        else:
            print(f"⚠️ 仍有持仓存在: {current_position}")

        print("✅ 清理操作完成")
        return True
    except Exception as e:
        print(f"❌ 清理操作失败: {e}")
        return False


def setup_exchange():
    """设置交易所参数 - 修复版本"""
    try:
        # 首先清理现有订单和持仓
        cleanup_before_setup()

        # 设置持仓模式为双向持仓
        max_retries = 3
        for attempt in range(max_retries):
            try:
                print(f"⚙️ 设置持仓模式 (第{attempt + 1}次尝试)...")

                # 使用最简单的参数设置持仓模式
                position_mode_response = exchange.privatePostAccountSetPositionMode({
                    'posMode': 'long_short_mode'
                })

                if position_mode_response.get('code') == '0':
                    print("✅ 双向持仓模式设置成功")
                    break
                elif position_mode_response.get('code') == '59000':
                    print(f"❌ 设置持仓模式失败: 需要先取消订单和平仓")
                    if attempt < max_retries - 1:
                        print("🔄 重新尝试清理并设置...")
                        cleanup_before_setup()
                        time.sleep(5)
                        continue
                    else:
                        print("❌ 多次尝试设置持仓模式均失败")
                        # 尝试继续运行，可能使用默认模式
                        print("⚠️ 使用默认持仓模式继续运行")
                        break
                else:
                    print(f"⚠️ 持仓模式设置返回: {position_mode_response}")
                    break

            except Exception as e:
                print(f"⚠️ 持仓模式设置警告 (第{attempt + 1}次): {e}")
                if attempt < max_retries - 1:
                    time.sleep(2)
                else:
                    print("❌ 持仓模式设置最终失败")
                    break

        # 设置杠杆
        try:
            print("⚙️ 设置杠杆...")

            # 方法1: 使用CCXT内置方法设置杠杆
            try:
                exchange.set_leverage(
                    leverage=TRADE_CONFIG['leverage'],
                    symbol='OKB/USDT:USDT'
                )
                print(f"✅ CCXT杠杆设置成功: {TRADE_CONFIG['leverage']}x")
            except Exception as e:
                print(f"⚠️ CCXT杠杆设置失败: {e}")
                # 方法2: 使用简化参数手动设置
                leverage_response = exchange.privatePostAccountSetLeverage({
                    'instId': 'OKB-USDT-SWAP',
                    'lever': str(TRADE_CONFIG['leverage'])
                })
                if leverage_response.get('code') == '0':
                    print(f"✅ 简化参数杠杆设置成功: {TRADE_CONFIG['leverage']}x")
                else:
                    print(f"⚠️ 简化参数杠杆设置返回: {leverage_response}")

        except Exception as e:
            print(f"⚠️ 杠杆设置警告: {e}")

        # 检查余额
        usdt_balance = get_usdt_balance()
        print(f"💰 当前USDT余额: {usdt_balance:.2f}")

        return True
    except Exception as e:
        print(f"交易所设置失败: {e}")
        return True


def calculate_technical_indicators(df):
    """计算技术指标"""
    try:
        # 移动平均线
        df['sma_5'] = df['close'].rolling(window=5, min_periods=1).mean()
        df['sma_20'] = df['close'].rolling(window=20, min_periods=1).mean()
        df['sma_50'] = df['close'].rolling(window=50, min_periods=1).mean()

        # 指数移动平均线
        df['ema_12'] = df['close'].ewm(span=12).mean()
        df['ema_26'] = df['close'].ewm(span=26).mean()
        df['macd'] = df['ema_12'] - df['ema_26']
        df['macd_signal'] = df['macd'].ewm(span=9).mean()
        df['macd_histogram'] = df['macd'] - df['macd_signal']

        # RSI
        delta = df['close'].diff()
        gain = (delta.where(delta > 0, 0)).rolling(14).mean()
        loss = (-delta.where(delta < 0, 0)).rolling(14).mean()
        rs = gain / loss
        df['rsi'] = 100 - (100 / (1 + rs))

        # 布林带
        df['bb_middle'] = df['close'].rolling(20).mean()
        bb_std = df['close'].rolling(20).std()
        df['bb_upper'] = df['bb_middle'] + (bb_std * 2)
        df['bb_lower'] = df['bb_middle'] - (bb_std * 2)
        df['bb_position'] = (df['close'] - df['bb_lower']) / (df['bb_upper'] - df['bb_lower'])

        df = df.bfill().ffill()
        return df
    except Exception as e:
        print(f"技术指标计算失败: {e}")
        return df


def get_market_trend(df):
    """判断市场趋势"""
    try:
        current_price = df['close'].iloc[-1]
        trend_short = "上涨" if current_price > df['sma_20'].iloc[-1] else "下跌"
        trend_medium = "上涨" if current_price > df['sma_50'].iloc[-1] else "下跌"
        macd_trend = "bullish" if df['macd'].iloc[-1] > df['macd_signal'].iloc[-1] else "bearish"

        if trend_short == "上涨" and trend_medium == "上涨":
            overall_trend = "强势上涨"
        elif trend_short == "下跌" and trend_medium == "下跌":
            overall_trend = "强势下跌"
        else:
            overall_trend = "震荡整理"

        return {
            'short_term': trend_short,
            'medium_term': trend_medium,
            'macd': macd_trend,
            'overall': overall_trend,
            'rsi_level': df['rsi'].iloc[-1]
        }
    except Exception as e:
        print(f"趋势分析失败: {e}")
        return {}


def get_OKB_ohlcv_enhanced():
    """获取OKB K线数据并计算技术指标"""
    max_retries = 3
    for attempt in range(max_retries):
        try:
            print(f"📊 获取K线数据 (第{attempt + 1}次尝试)...")
            ohlcv = exchange.fetch_ohlcv(TRADE_CONFIG['symbol'], TRADE_CONFIG['timeframe'],
                                         limit=TRADE_CONFIG['data_points'])

            if not ohlcv or len(ohlcv) == 0:
                if attempt < max_retries - 1:
                    time.sleep(2)
                    continue
                return None

            df = pd.DataFrame(ohlcv, columns=['timestamp', 'open', 'high', 'low', 'close', 'volume'])
            df['timestamp'] = pd.to_datetime(df['timestamp'], unit='ms')
            df = calculate_technical_indicators(df)

            current_data = df.iloc[-1]
            previous_data = df.iloc[-2]

            trend_analysis = get_market_trend(df)

            return {
                'price': float(current_data['close']),
                'timestamp': datetime.now().strftime('%Y-%m-%d %H:%M:%S'),
                'high': float(current_data['high']),
                'low': float(current_data['low']),
                'volume': float(current_data['volume']),
                'timeframe': TRADE_CONFIG['timeframe'],
                'price_change': float(
                    ((current_data['close'] - previous_data['close']) / max(previous_data['close'], 0.0001)) * 100),
                'kline_data': df[['timestamp', 'open', 'high', 'low', 'close', 'volume']].tail(10).to_dict('records'),
                'technical_data': {
                    'sma_5': float(current_data.get('sma_5', 0)),
                    'sma_20': float(current_data.get('sma_20', 0)),
                    'sma_50': float(current_data.get('sma_50', 0)),
                    'rsi': float(current_data.get('rsi', 0)),
                    'macd': float(current_data.get('macd', 0)),
                    'macd_signal': float(current_data.get('macd_signal', 0)),
                },
                'trend_analysis': trend_analysis,
                'full_data': df
            }
        except Exception as e:
            print(f"❌ 获取K线数据失败 (第{attempt + 1}次): {e}")
            if attempt < max_retries - 1:
                time.sleep(2)
            else:
                return None
    return None


def create_fallback_signal(price_data):
    """创建备用交易信号"""
    return {
        "signal": "HOLD",
        "reason": "因技术分析暂时不可用，采取保守策略",
        "stop_loss": price_data['price'] * 0.98,
        "take_profit": price_data['price'] * 1.02,
        "confidence": "LOW",
        "is_fallback": True
    }


def analyze_with_deepseek(price_data):
    """使用DeepSeek分析市场并生成交易信号"""
    if not deepseek_client:
        return create_fallback_signal(price_data)

    try:
        prompt = f"""
        基于以下OKB/USDT {TRADE_CONFIG['timeframe']}周期数据进行分析：
        当前价格: ${price_data['price']:,.2f}
        价格变化: {price_data['price_change']:+.2f}%
        趋势: {price_data['trend_analysis'].get('overall', 'N/A')}
        RSI: {price_data['technical_data'].get('rsi', 0):.1f}

        请给出交易信号，用JSON格式回复：
        {{
            "signal": "BUY|SELL|HOLD",
            "reason": "分析理由",
            "stop_loss": 具体价格,
            "take_profit": 具体价格, 
            "confidence": "HIGH|MEDIUM|LOW"
        }}
        """

        response = deepseek_client.chat.completions.create(
            model="deepseek-chat",
            messages=[{"role": "user", "content": prompt}],
            stream=False,
            temperature=0.1
        )

        result = response.choices[0].message.content
        print(f"🧠 DeepSeek回复: {result}")

        # 解析JSON
        try:
            start_idx = result.find('{')
            end_idx = result.rfind('}') + 1
            if start_idx != -1 and end_idx != 0:
                json_str = result[start_idx:end_idx]
                signal_data = json.loads(json_str)

                required_fields = ['signal', 'reason', 'stop_loss', 'take_profit', 'confidence']
                if all(field in signal_data for field in required_fields):
                    signal_data['timestamp'] = price_data['timestamp']
                    signal_history.append(signal_data)
                    if len(signal_history) > 30:
                        signal_history.pop(0)
                    return signal_data
        except:
            pass

        return create_fallback_signal(price_data)

    except Exception as e:
        print(f"❌ DeepSeek分析失败: {e}")
        return create_fallback_signal(price_data)


def execute_trade(signal_data, price_data):
    """执行交易 - 修复版本"""
    global position

    current_position = get_current_position()
    current_price = price_data['price']

    print(f"📈 交易信号: {signal_data['signal']}")
    print(f"📊 信心程度: {signal_data['confidence']}")

    # 严格的风险管理
    if signal_data['confidence'] == 'LOW':
        print("⚠️ 低信心信号，跳过执行")
        return

    if TRADE_CONFIG['test_mode']:
        print("🧪 测试模式 - 仅模拟交易")
        return

    try:
        # 计算仓位大小
        position_size, actual_notional, required_margin = calculate_position_size(current_price)

        # 严格的余额检查
        usdt_balance = get_usdt_balance()
        if required_margin > usdt_balance * 0.6:
            print(f"❌ 保证金不足，取消交易")
            return

        if actual_notional > TRADE_CONFIG['max_position_value']:
            print(f"❌ 开仓价值超过限制: ${actual_notional:.2f} > ${TRADE_CONFIG['max_position_value']}")
            return

        # 最小开仓价值检查
        if actual_notional < 3.0:
            print(f"⚠️ 开仓价值过小 (${actual_notional:.2f})，可能不划算")
            return

        # 检查最大持仓限制
        if current_position:
            current_notional = current_position['size'] * get_contract_specs()['contract_value'] * current_price
            if current_notional + actual_notional > TRADE_CONFIG['max_position_value']:
                print(f"⚠️ 超过最大持仓限制，跳过交易。当前: ${current_notional:.2f}, 新增: ${actual_notional:.2f}")
                return

        # 执行交易
        if signal_data['signal'] == 'BUY':
            if current_position and current_position['side'] == 'short':
                print("🔄 平空仓并开多仓...")
                # 平空仓
                close_params = {
                    'instId': 'OKB-USDT-SWAP',
                    'tdMode': 'isolated',
                    'side': 'buy',
                    'posSide': 'short',
                    'ordType': 'market',
                    'sz': str(current_position['size']),
                    'reduceOnly': True
                }
                close_response = exchange.privatePostTradeOrder(close_params)
                print(f"✅ 平空仓结果: {close_response.get('code', 'N/A')}")
                if close_response.get('code') != '0':
                    print(f"❌ 平仓失败: {close_response}")
                    return
                time.sleep(2)

            # 开多仓
            print("📈 开多仓...")
            open_params = {
                'instId': 'OKB-USDT-SWAP',
                'tdMode': 'isolated',
                'side': 'buy',
                'posSide': 'long',
                'ordType': 'market',
                'sz': str(position_size)
            }
            response = exchange.privatePostTradeOrder(open_params)
            print(f"✅ 开多仓结果: {response.get('code', 'N/A')}")

        elif signal_data['signal'] == 'SELL':
            if current_position and current_position['side'] == 'long':
                print("🔄 平多仓并开空仓...")
                # 平多仓
                close_params = {
                    'instId': 'OKB-USDT-SWAP',
                    'tdMode': 'isolated',
                    'side': 'sell',
                    'posSide': 'long',
                    'ordType': 'market',
                    'sz': str(current_position['size']),
                    'reduceOnly': True
                }
                close_response = exchange.privatePostTradeOrder(close_params)
                print(f"✅ 平多仓结果: {close_response.get('code', 'N/A')}")
                if close_response.get('code') != '0':
                    print(f"❌ 平仓失败: {close_response}")
                    return
                time.sleep(2)

            # 开空仓
            print("📉 开空仓...")
            open_params = {
                'instId': 'OKB-USDT-SWAP',
                'tdMode': 'isolated',
                'side': 'sell',
                'posSide': 'short',
                'ordType': 'market',
                'sz': str(position_size)
            }
            response = exchange.privatePostTradeOrder(open_params)
            print(f"✅ 开空仓结果: {response.get('code', 'N/A')}")

        elif signal_data['signal'] == 'HOLD':
            print("⏸️ 建议观望，不执行交易")
            return

        print("✅ 订单执行完成")
        time.sleep(3)
        position = get_current_position()

    except Exception as e:
        print(f"❌ 订单执行失败: {e}")


def wait_for_next_period():
    """等待到下一个5分钟整点"""
    now = datetime.now()
    current_minute = now.minute
    current_second = now.second

    next_period_minute = ((current_minute // 5) + 1) * 5
    if next_period_minute == 60:
        next_period_minute = 0

    if next_period_minute > current_minute:
        minutes_to_wait = next_period_minute - current_minute
    else:
        minutes_to_wait = 60 - current_minute + next_period_minute

    seconds_to_wait = minutes_to_wait * 60 - current_second

    if minutes_to_wait > 0:
        print(f"🕒 等待 {minutes_to_wait} 分 {seconds_to_wait % 60} 秒到整点...")
    else:
        print(f"🕒 等待 {seconds_to_wait} 秒到整点...")

    return seconds_to_wait


def trading_bot():
    """主交易机器人函数"""
    wait_seconds = wait_for_next_period()
    if wait_seconds > 0:
        time.sleep(wait_seconds)

    print("\n" + "=" * 60)
    print(f"⏰ 执行时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 60)

    # 获取价格数据
    price_data = get_OKB_ohlcv_enhanced()
    if not price_data:
        print("❌ 无法获取价格数据，跳过本次执行")
        return

    print(f"💰 OKB当前价格: ${price_data['price']:,.2f}")
    print(f"📈 价格变化: {price_data['price_change']:+.2f}%")

    # 生成交易信号
    signal_data = analyze_with_deepseek(price_data)
    if signal_data.get('is_fallback', False):
        print("⚠️ 使用备用交易信号")

    # 执行交易
    execute_trade(signal_data, price_data)


def main():
    """主函数"""
    required_env_vars = ['OKX_API_KEY', 'OKX_SECRET', 'OKX_PASSWORD', 'DEEPSEEK_API_KEY']
    missing_vars = [var for var in required_env_vars if not os.getenv(var)]

    if missing_vars:
        print(f"❌ 缺少环境变量: {missing_vars}")
        return

    print("🤖 OKB/USDT OKX自动交易机器人启动成功！")
    print("🎯 10美元本金安全优化版")

    if TRADE_CONFIG['test_mode']:
        print("🧪 当前为测试模式")
    else:
        print("💰 实盘交易模式，请谨慎操作！")

    print(f"⏰ 交易周期: {TRADE_CONFIG['timeframe']}")
    print(f"🎯 目标开仓价值: ${TRADE_CONFIG['target_notional']}")
    print(f"📊 杠杆倍数: {TRADE_CONFIG['leverage']}x")
    print(f"🔐 最大持仓: ${TRADE_CONFIG['max_position_value']}")

    if deepseek_client is None:
        print("❌ DeepSeek客户端初始化失败")
        return
    if exchange is None:
        print("❌ OKX交易所初始化失败")
        return

    if not setup_exchange():
        print("❌ 交易所设置失败")
        return

    print("🔁 开始主循环...")

    # 先执行一次
    trading_bot()

    # 然后按计划执行
    schedule.every(5).minutes.do(trading_bot)

    while True:
        try:
            schedule.run_pending()
            time.sleep(1)
        except Exception as e:
            print(f"❌ 执行周期出错: {e}")
            time.sleep(60)


if __name__ == "__main__":
    main()