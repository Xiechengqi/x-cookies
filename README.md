# X Cookies

使用 Playwright 与 Loguru 的 Twitter Cookie 导出工具。脚本通过 `connect_over_cdp` 连接到开启远程调试的浏览器，并可选地使用 `X_AUTH_TOKEN` 注入方式自动完成登录，再导出所需 Cookies。

## 快速开始

1. 安装依赖（需额外安装 Go 1.21+ 用于测试）：
   ```bash
   pip install -r requirements.txt
   playwright install chromium
   ```
2. 启动 Chrome/Chromium 并开启远程调试端口，例如：
   ```bash
   chromium --remote-debugging-port=9222
   ```
   打开 `https://twitter.com/`（无需手动登录，脚本会注入 token）。
3. 配置环境变量：
   ```bash
   export X_ACCOUNT="my_account"         # 必须，脚本只接受单账号
   export X_AUTH_TOKEN="xxxxx"           # 必须，用于自动登录
   export CDP_ENDPOINT="http://localhost:9222"  # 可选，默认即为该值
   ```
4. 编译 Go 冒烟测试二进制（首次或源码更新后）：
   ```bash
   cd twitter-scraper
   ./build.sh
   cd ..
   ```
5. 运行脚本（会在导出后自动调用 `twitter-scraper/twitter-scraper` 进行一次搜索测试）：
   ```bash
   python main.py
   ```

## 输出

- Cookies 默认写入 `cookies/<username>_twitter_cookies.json`。
- 若设置 `RUNNING_IN_DOCKER=true` 则输出目录为 `/app/cookies`。

## 其他

- `X_ACCOUNT` 用于决定输出文件名，脚本仅支持单账号。
- `X_AUTH_TOKEN` 为必填项，脚本会注入此 Cookie 以完成登录。
- 只要浏览器开放了远程调试端口，`CDP_ENDPOINT` 可指向任意可访问地址，如 `http://127.0.0.1:9223`，不设置则默认 `http://localhost:9222`。
- 自动测试阶段可通过 `SCRAPER_TEST_QUERY`、`SCRAPER_TEST_COUNT`、`TWITTER_SCRAPER_PROXY` 调整搜索参数；若需要代理则设置 `TERMINAL_RPOXY`，脚本会自动添加 `-proxy` 传给二进制；设置 `SKIP_SCRAPER_TEST=true` 可跳过 Go 测试。若缺少 `twitter-scraper/twitter-scraper` 可执行文件，请先运行 `twitter-scraper/build.sh`。
