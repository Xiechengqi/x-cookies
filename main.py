#!/usr/bin/env python3
import json
import os
import random
import subprocess
import sys
import time
from pathlib import Path
from typing import Dict, List

from loguru import logger
from playwright.sync_api import TimeoutError as PlaywrightTimeoutError, sync_playwright

logger.remove()
logger.add(
    sys.stdout,
    colorize=True,
    format="<g>{time:YYYY-MM-DD HH:mm:ss}</g> | {level} | {message}",
)

COOKIE_NAMES = ["personalization_id", "kdt", "twid", "ct0", "auth_token", "att"]
PROJECT_ROOT = Path(__file__).resolve().parent


def resolve_output_dir() -> Path:
    running_in_docker = os.environ.get("RUNNING_IN_DOCKER", "false").lower() == "true"
    if running_in_docker:
        output_dir = Path("/app/cookies")
        logger.info(f"检测到 Docker 环境，Cookies 输出目录：{output_dir}")
    else:
        output_dir = PROJECT_ROOT.parent / "cookies"
        logger.info(f"检测到本地环境，Cookies 输出目录：{output_dir}")
    output_dir.mkdir(parents=True, exist_ok=True)
    return output_dir


OUTPUT_DIR = resolve_output_dir()


def get_future_date(days: int = 7) -> str:
    return (
        time.strftime(
            "%Y-%m-%dT%H:%M:%SZ",
            time.gmtime(time.time() + days * 24 * 3600 + random.uniform(0, 3600)),
        )
    )


def create_cookie_template(name: str, value: str, expires: str) -> Dict[str, str]:
    return {
        "Name": name,
        "Value": value.replace('"', ""),
        "Path": "",
        "Domain": "twitter.com",
        "Expires": expires,
        "RawExpires": "",
        "MaxAge": 0,
        "Secure": False,
        "HttpOnly": False,
        "SameSite": 0,
        "Raw": "",
        "Unparsed": None,
    }


def build_cookie_payload(cookie_values: Dict[str, str]) -> List[Dict[str, str]]:
    one_week = get_future_date(7)
    one_month = get_future_date(30)
    payload = []
    for name in COOKIE_NAMES:
        expires = one_month if name in {"personalization_id", "kdt"} else one_week
        payload.append(create_cookie_template(name, cookie_values.get(name, ""), expires))
    return payload


def save_cookies(username: str, cookies_payload: List[Dict[str, str]]) -> Path:
    output_path = OUTPUT_DIR / f"{username}_twitter_cookies.json"
    with output_path.open("w", encoding="utf-8") as fp:
        json.dump(cookies_payload, fp, indent=2, ensure_ascii=False)
    logger.success(f"Cookies 已保存：{output_path}")
    return output_path


def extract_cookies(context) -> Dict[str, str]:
    cookies = context.cookies()
    cookie_values = {}
    for name in COOKIE_NAMES:
        match = next((item for item in cookies if item.get("name") == name), None)
        cookie_values[name] = (match or {}).get("value", "")
        if cookie_values[name]:
            logger.info(f"发现 Cookie：{name}")
        else:
            logger.warning(f"缺少 Cookie：{name}")
    return cookie_values


def connect_existing_browser(playwright):
    endpoint = os.environ.get("CDP_ENDPOINT", "http://localhost:9222")
    logger.info(f"尝试通过 CDP 连接浏览器：{endpoint}")
    browser = playwright.chromium.connect_over_cdp(endpoint)

    if not browser.contexts:
        raise RuntimeError("未检测到任何上下文，请确保浏览器已开启远程调试并打开页面。")

    context = browser.contexts[0]
    page = context.pages[0] if context.pages else context.new_page()
    logger.info(f"连接成功，当前页面 URL：{page.url}")
    return browser, context, page


def get_primary_page(context):
    """返回一个可用页面，并确保关闭多余标签。"""

    for extra_page in context.pages[1:]:
        try:
            extra_page.close()
        except Exception:
            pass
    return context.pages[0] if context.pages else context.new_page()


def is_logged_in(context, timeout: int = 10_000) -> bool:
    """判断当前页面是否已登录 X。"""

    page = get_primary_page(context)
    page.goto("https://x.com/home", wait_until="domcontentloaded")
    page.wait_for_timeout(3_000)

    try:
        page.wait_for_selector("span:has-text(\"Post\")", timeout=timeout)
        return True
    except PlaywrightTimeoutError:
        return False


def login_with_token(context, auth_token: str) -> bool:
    """使用 auth_token Cookie 执行快速登录。"""

    page = get_primary_page(context)

    logger.info("准备注入 auth_token Cookie")
    page.goto("https://x.com", wait_until="domcontentloaded")
    page.wait_for_timeout(5_000)

    expiration_time = int(time.time()) + 365 * 24 * 60 * 60
    cookie = {
        "name": "auth_token",
        "value": auth_token,
        "domain": ".x.com",
        "path": "/",
        "expires": expiration_time,
        "secure": True,
        "httpOnly": False,
        "sameSite": "Lax",
    }
    context.add_cookies([cookie])
    logger.info("auth_token 注入完成，刷新页面进行验证")

    if is_logged_in(context):
        logger.success("检测到 Post 按钮，登录成功")
        return True

    logger.error("未检测到 Post 按钮，疑似被风控（X Locking）")
    return False


def run_scraper_smoke_test(cookie_path: Path, username: str):
    if os.getenv("SKIP_SCRAPER_TEST", "").lower() == "true":
        logger.info("检测到 SKIP_SCRAPER_TEST=true，跳过 go 测试")
        return

    query = os.getenv("SCRAPER_TEST_QUERY") or f"from:{username}"
    count = os.getenv("SCRAPER_TEST_COUNT", "5")
    binary_path = PROJECT_ROOT / "twitter-scraper" / "twitter-scraper"

    if not binary_path.exists():
        logger.error(
            f"未找到 twitter-scraper 二进制：{binary_path}，请先运行 twitter-scraper/build.sh"
        )
        raise FileNotFoundError(f"twitter-scraper binary missing at {binary_path}")

    cmd = [
        str(binary_path),
        "-cookies",
        str(cookie_path),
        "-query",
        query,
        "-count",
        count,
        "-json",
    ]

    proxy = os.getenv("TERMINAL_RPOXY", "").strip()
    if proxy:
        cmd.extend(["-proxy", proxy])

    env = os.environ.copy()
    env.setdefault("X_ACCOUNT", username)
    env.setdefault("TWITTER_COOKIE_FILE", str(cookie_path))
    env.setdefault("COOKIES_DIR", str(OUTPUT_DIR))

    logger.info(f"开始运行 twitter-scraper 测试，命令：{' '.join(cmd)}")
    try:
        subprocess.run(cmd, cwd=PROJECT_ROOT, env=env, check=True)
        logger.success("twitter-scraper 测试通过")
    except FileNotFoundError:
        logger.error("未找到 go 可执行文件，请安装 Go 1.21+")
        raise
    except subprocess.CalledProcessError as exc:
        logger.error(f"twitter-scraper 测试失败，退出码：{exc.returncode}")
        raise


def main():
    username = os.getenv("X_ACCOUNT", "").strip()
    if not username:
        logger.error("必须通过 X_ACCOUNT 指定单个账号")
        return

    auth_token = os.getenv("X_AUTH_TOKEN", "").strip()
    if not auth_token:
        logger.warning("未提供 X_AUTH_TOKEN，若浏览器未登录将无法自动注入凭证")
    logger.info(f"将使用用户名标识导出文件：{username}")

    with sync_playwright() as playwright:
        browser = None
        try:
            browser, context, _ = connect_existing_browser(playwright)
            if is_logged_in(context):
                logger.success("检测到现有登录态，跳过 auth_token 注入")
            else:
                logger.info("未检测到现有登录态，尝试通过 auth_token 自动登录")
                if not auth_token:
                    logger.error("当前未登录且缺少 X_AUTH_TOKEN，无法继续")
                    return
                if not login_with_token(context, auth_token):
                    logger.error("登录失败，无法提取 Cookies")
                    return
            cookies = extract_cookies(context)
            missing = [name for name, value in cookies.items() if not value]
            if missing:
                logger.error(
                    f"检测到缺失 Cookie：{', '.join(missing)}，跳过写入及测试"
                )
                return
            payload = build_cookie_payload(cookies)
            cookie_path = save_cookies(username, payload)
            run_scraper_smoke_test(cookie_path, username)
        except Exception as exc:
            logger.exception(f"提取 Cookies 失败：{exc}")
        finally:
            if browser:
                browser.close()

    logger.success("处理完成")


if __name__ == "__main__":
    main()
