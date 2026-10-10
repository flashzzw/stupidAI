#!/usr/bin/env python3
"""查询一个城市的当前天气：python3 weather.py 北京（仅使用 Python 标准库）。"""

import argparse
import json
import math
import sys
from datetime import datetime, timedelta, timezone
from http.client import HTTPException
from urllib.error import URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

# 插入顺序也是 ping_codex.py 的轮询顺序，避免维护两份城市列表。
CITY_COORDINATES = {
    "纽约": (40.7128, -74.0060),
    "洛杉矶": (34.0522, -118.2437),
    "多伦多": (43.6532, -79.3832),
    "墨西哥城": (19.4326, -99.1332),
    "圣保罗": (-23.5505, -46.6333),
    "布宜诺斯艾利斯": (-34.6037, -58.3816),
    "伦敦": (51.5074, -0.1278),
    "巴黎": (48.8566, 2.3522),
    "柏林": (52.5200, 13.4050),
    "马德里": (40.4168, -3.7038),
    "罗马": (41.9028, 12.4964),
    "莫斯科": (55.7558, 37.6173),
    "开罗": (30.0444, 31.2357),
    "拉各斯": (6.5244, 3.3792),
    "内罗毕": (-1.2921, 36.8219),
    "约翰内斯堡": (-26.2041, 28.0473),
    "迪拜": (25.2048, 55.2708),
    "孟买": (19.0760, 72.8777),
    "新德里": (28.6139, 77.2090),
    "曼谷": (13.7563, 100.5018),
    "新加坡": (1.3521, 103.8198),
    "雅加达": (-6.2088, 106.8456),
    "吉隆坡": (3.1390, 101.6869),
    "香港": (22.3193, 114.1694),
    "上海": (31.2304, 121.4737),
    "北京": (39.9042, 116.4074),
    "东京": (35.6762, 139.6503),
    "首尔": (37.5665, 126.9780),
    "悉尼": (-33.8688, 151.2093),
    "墨尔本": (-37.8136, 144.9631),
    "奥克兰": (-36.8485, 174.7633),
}
CITIES = tuple(CITY_COORDINATES)
# https://open-meteo.com/en/docs#weathervariables
WEATHER_CODES = {
    0: "晴", 1: "大部晴朗", 2: "多云", 3: "阴",
    45: "雾", 48: "雾凇", 51: "轻微毛毛雨", 53: "中等毛毛雨", 55: "强毛毛雨",
    56: "轻微冻毛毛雨", 57: "强冻毛毛雨", 61: "小雨", 63: "中雨", 65: "大雨",
    66: "轻微冻雨", 67: "强冻雨", 71: "小雪", 73: "中雪", 75: "大雪", 77: "雪粒",
    80: "小阵雨", 81: "中阵雨", 82: "强阵雨", 85: "小阵雪", 86: "大阵雪",
    95: "雷暴", 96: "雷暴伴小冰雹", 97: "强雷暴", 99: "雷暴伴大冰雹",
}


def query_weather(city: str, timeout_s: float = 20) -> str:
    latitude, longitude = CITY_COORDINATES[city]
    params = urlencode({
        "latitude": latitude, "longitude": longitude,
        "current": "temperature_2m,weather_code", "temperature_unit": "celsius",
        "timezone": "auto", "timeformat": "unixtime",
    })
    request = Request(
        "https://api.open-meteo.com/v1/forecast?" + params,
        headers={"User-Agent": "codex-netcap-weather/1.0", "Accept": "application/json"},
    )
    with urlopen(request, timeout=timeout_s) as response:
        payload = json.load(response)
    if not isinstance(payload, dict):
        raise ValueError("天气接口返回的不是 JSON 对象")
    if payload.get("error"):
        raise ValueError(str(payload.get("reason", "天气接口报错")))
    current = payload.get("current")
    if not isinstance(current, dict):
        raise ValueError("天气接口缺少 current 数据")
    temperature = current.get("temperature_2m")
    code = current.get("weather_code")
    timestamp = current.get("time")
    offset = payload.get("utc_offset_seconds")
    for name, value in (("温度", temperature), ("数据时刻", timestamp), ("时区偏移", offset)):
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
            raise ValueError(f"天气接口缺少有效{name}")
    if type(code) is not int or code not in WEATHER_CODES:
        raise ValueError(f"天气接口返回未知天气代码：{code!r}")
    local_tz = timezone(timedelta(seconds=offset))
    local_now = datetime.now(local_tz).isoformat(timespec="seconds")
    data_time = datetime.fromtimestamp(timestamp, local_tz).isoformat(timespec="minutes")
    return (
        f"{city}：{WEATHER_CODES[code]}，{temperature:.1f}°C；当地时间 {local_now}；"
        f"数据时间 {data_time}；Open-Meteo（模型估算）"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("city", nargs="?", choices=CITIES, help="要查询的城市中文名")
    parser.add_argument("--list-cities", action="store_true", help="输出城市顺序 JSON，不查询天气")
    parser.add_argument("--timeout", type=float, default=20, help="天气接口网络超时（秒）")
    args = parser.parse_args()
    if args.list_cities:
        print(json.dumps(CITIES, ensure_ascii=False))
        return 0
    if args.city is None:
        parser.error("请提供城市名称，或使用 --list-cities")
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("timeout 必须是有限的正数")
    try:
        print(query_weather(args.city, args.timeout))
    except (HTTPException, URLError, OSError, ValueError, OverflowError) as exc:
        print(f"{args.city}：无法获取天气（{exc}）", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
