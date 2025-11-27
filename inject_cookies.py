#!/usr/bin/env python3
"""将 main.py 导出的 Cookies 注入到指定的浏览器 CDP 会话中。"""

import argparse
import json
import os
from datetime import datetime, timezone
from pathlib import Path
from typing import List

from loguru import logger
from playwright.sync_api import sync_playwright

PROJECT_ROOT = Path(__file__).resolve().parent
COOKIES_DIR = PROJECT_ROOT.parent / "cookies"
DEFAULT_CDP_ENDPOINT = "http://localhost:9222"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--cookie-file",
        type=Path,
        help="自定义 cookie JSON 文件路径，默认使用 X_ACCOUNT 对应文件",
    )
    parser.add_argument(
        "--endpoint",
        default=os.environ.get("CDP_ENDPOINT", DEFAULT_CDP_ENDPOINT),
        help="CDP 连接地址，默认读取环境变量 CDP_ENDPOINT",
    )
    parser.add_argument(
        "--username",
        default=os.environ.get("X_ACCOUNT", "").strip(),
        help="用于推断 cookie 文件名，若同时提供 --cookie-file 则忽略",
    )
    return parser.parse_args()


def resolve_cookie_file(args: argparse.Namespace) -> Path:
    if args.cookie_file:
        return args.cookie_file
    if not args.username:
        raise SystemExit("未提供 --cookie-file 或 X_ACCOUNT，无法定位 Cookies 文件")
    candidate = COOKIES_DIR / f"{args.username}_twitter_cookies.json"
    if not candidate.exists():
        raise SystemExit(f"未找到 Cookies 文件：{candidate}")
    return candidate


def load_cookies(path: Path) -> List[dict]:
    try:
        with path.open("r", encoding="utf-8") as fp:
            data = json.load(fp)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"Cookies 文件格式错误：{exc}") from exc

    if not isinstance(data, list):
        raise SystemExit("Cookies 文件内容必须是列表")
    return data


def iso_to_epoch(ts: str) -> int:
    if not ts:
        return 0
    try:
        return int(
            datetime.strptime(ts, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc).timestamp()
        )
    except ValueError:
        return 0


def adapt_cookie(cookie: dict) -> dict:
    same_site_map = {0: "Lax", 1: "Strict", 2: "None"}
    adapted = {
        "name": cookie.get("Name", ""),
        "value": cookie.get("Value", ""),
        "domain": cookie.get("Domain") or ".x.com",
        "path": cookie.get("Path") or "/",
        "secure": bool(cookie.get("Secure", True)),
        "httpOnly": bool(cookie.get("HttpOnly", False)),
        "sameSite": cookie.get("SameSite"),
    }
    if isinstance(adapted["sameSite"], int):
        adapted["sameSite"] = same_site_map.get(adapted["sameSite"], "Lax")
    if adapted["sameSite"] not in {"Lax", "Strict", "None"}:
        adapted.pop("sameSite")

    expires = iso_to_epoch(cookie.get("Expires"))
    if expires > 0:
        adapted["expires"] = expires
    else:
        adapted.pop("expires", None)
    return adapted


def ensure_context(browser):
    if not browser.contexts:
        raise RuntimeError("未检测到任何浏览器上下文，请确认目标浏览器已打开页面")
    context = browser.contexts[0]
    return context.pages[0] if context.pages else context.new_page()


def inject_and_open(endpoint: str, cookies: List[dict]):
    with sync_playwright() as p:
        logger.info(f"连接浏览器：{endpoint}")
        browser = p.chromium.connect_over_cdp(endpoint)
        page = ensure_context(browser)

        adapted = [adapt_cookie(item) for item in cookies if item.get("Name")]
        if not adapted:
            raise SystemExit("提供的 Cookies 为空，无法注入")

        logger.info(f"开始注入 {len(adapted)} 个 Cookies")
        page.context.add_cookies(adapted)
        logger.success("Cookies 注入完成，准备打开 x.com")

        page.goto("https://x.com", wait_until="domcontentloaded")
        logger.success("x.com 已打开，若已登录应可直接访问首页")
        browser.close()


def main():
    args = parse_args()
    cookie_file = resolve_cookie_file(args)
    cookies = load_cookies(cookie_file)

    logger.info(f"读取 Cookies：{cookie_file}")
    inject_and_open(args.endpoint, cookies)


if __name__ == "__main__":
    main()
