package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	twitterscraper "github.com/imperatrona/twitter-scraper"
	"golang.org/x/net/proxy"
)

// Scraper 模拟 tee-worker 的 Scraper 结构
type Scraper struct {
	*twitterscraper.Scraper
	skipLoginVerification bool
}

// NewScraper 创建新的 scraper
func NewScraper() *Scraper {
	return &Scraper{
		Scraper:               twitterscraper.New(),
		skipLoginVerification: true,
	}
}

// SetSkipLoginVerification 设置跳过验证
func (s *Scraper) SetSkipLoginVerification(skip bool) *Scraper {
	s.skipLoginVerification = skip
	return s
}

// IsLoggedIn 检查登录状态，跳过验证时始终返回 true
func (s *Scraper) IsLoggedIn() bool {
	// 调用底层方法来设置 bearer token
	loggedIn := s.Scraper.IsLoggedIn()
	if s.skipLoginVerification {
		return true //[?12;2$y 跳过验证，避免速率限制
	}
	return loggedIn
}

// TweetResult 简化的推文结果结构
type TweetResult struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	Likes     int       `json:"likes"`
	Retweets  int       `json:"retweets"`
	Replies   int       `json:"replies"`
	IsRetweet bool      `json:"is_retweet"`
	URLs      []string  `json:"urls"`
	Hashtags  []string  `json:"hashtags"`
}

const (
	verifyEndpoint    = "https://twitter.com/i/api/1.1/account/verify_credentials.json?skip_status=1&include_email=false"
	verifyBearerToken = "AAAAAAAAAAAAAAAAAAAAAFQODgEAAAAAVHTp76lzh3rFzcHbmHVvQxYYpTw%3DckAlMINMjmCwxUcaXbAN4XqJVdgMJaHqNOFgPMK0zN1qLqLQCF"
)

// loadCookiesFromFile 加载 cookies，模拟 tee-worker 的完整流程
func loadCookiesFromFile(scraper *Scraper, cookieFile string) ([]*http.Cookie, error) {
	log.Printf("Loading cookies from file: %s", cookieFile)

	// 先登出，模拟 tee-worker 的行为
	if err := scraper.Logout(); err != nil {
		log.Printf("Error logging out (continuing): %v", err)
	}

	data, err := os.ReadFile(cookieFile)
	if err != nil {
		return nil, fmt.Errorf("error reading cookie file: %v", err)
	}

	var cookies []*http.Cookie
	if err = json.Unmarshal(data, &cookies); err != nil {
		return nil, fmt.Errorf("error unmarshaling cookies: %v", err)
	}

	log.Printf("Loaded %d cookies from file", len(cookies))

	// 验证关键 cookies
	var hasAuthToken, hasCSRFToken bool
	for _, cookie := range cookies {
		// 确保域名和路径满足 Twitter API 需求
		if cookie.Domain == "" || cookie.Domain == "twitter.com" {
			cookie.Domain = ".twitter.com"
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}

		if cookie.Name == "auth_token" {
			hasAuthToken = true
			log.Printf("Found auth_token cookie")
		}
		if cookie.Name == "ct0" {
			hasCSRFToken = true
			log.Printf("Found CSRF token cookie")
		}
	}

	if !hasAuthToken || !hasCSRFToken {
		return nil, fmt.Errorf("missing critical authentication cookies")
	}

	// 设置 cookies
	scraper.SetCookies(cookies)
	log.Println("Successfully loaded and set cookies")
	return cookies, nil
}

func buildDirectHTTPClient(proxyAddr string) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		Jar:       jar,
	}

	if proxyAddr == "" {
		return client, nil
	}

	if strings.HasPrefix(proxyAddr, "http") {
		proxyURL, err := url.Parse(proxyAddr)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		return client, nil
	}

	if strings.HasPrefix(proxyAddr, "socks5") {
		baseDialer := &net.Dialer{
			Timeout:   client.Timeout,
			KeepAlive: client.Timeout,
		}

		proxyURL, err := url.Parse(proxyAddr)
		if err != nil {
			return nil, err
		}

		username := proxyURL.User.Username()
		password, _ := proxyURL.User.Password()

		var auth *proxy.Auth
		if username != "" || password != "" {
			auth = &proxy.Auth{User: username, Password: password}
		}

		dialSocksProxy, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, baseDialer)
		if err != nil {
			return nil, fmt.Errorf("error creating socks5 proxy: %w", err)
		}
		contextDialer, ok := dialSocksProxy.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("failed to assert socks5 dialer type")
		}
		transport.DialContext = contextDialer.DialContext
		return client, nil
	}

	return nil, fmt.Errorf("unsupported proxy protocol: %s", proxyAddr)
}

// verifyCookiesDirectly 通过直接访问 verify_credentials 接口验证 cookie 是否有效
func verifyCookiesDirectly(cookies []*http.Cookie, proxyAddr string) error {
	client, err := buildDirectHTTPClient(proxyAddr)
	if err != nil {
		return fmt.Errorf("failed to build verification http client: %w", err)
	}

	verifyURL, err := url.Parse(verifyEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse verify endpoint: %w", err)
	}
	client.Jar.SetCookies(verifyURL, cookies)

	var csrfToken string
	for _, cookie := range cookies {
		if cookie.Name == "ct0" {
			csrfToken = cookie.Value
			break
		}
	}
	if csrfToken == "" {
		return fmt.Errorf("ct0 cookie missing for verification")
	}

	req, err := http.NewRequest(http.MethodGet, verifyEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create verify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+verifyBearerToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("X-Twitter-Auth-Type", "OAuth2Session")
	req.Header.Set("X-Twitter-Active-User", "yes")
	req.Header.Set("Referer", "https://twitter.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("verification request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("verification failed: status %s, body: %s", resp.Status, string(body))
	}

	log.Printf("Direct cookie verification succeeded: %s", string(body))
	return nil
}

// verifyLoginStatus 调用底层登录验证，确保 scraper 已处于登录状态
func verifyLoginStatus(scraper *Scraper) error {
	log.Println("Verifying login status with Twitter API")
	if !scraper.Scraper.IsLoggedIn() {
		return fmt.Errorf("login verification failed, 请确认 cookies 或 auth_token 是否有效")
	}
	log.Println("Login verification succeeded")
	return nil
}

// convertTweet 转换推文格式
func convertTweet(tweet twitterscraper.Tweet) *TweetResult {
	return &TweetResult{
		ID:        tweet.ID,
		Text:      tweet.Text,
		Username:  tweet.Username,
		CreatedAt: time.Unix(tweet.Timestamp, 0).UTC(),
		Likes:     tweet.Likes,
		Retweets:  tweet.Retweets,
		Replies:   tweet.Replies,
		IsRetweet: tweet.IsRetweet,
		URLs:      tweet.URLs,
		Hashtags:  tweet.Hashtags,
	}
}

// searchTweets 执行搜索
func searchTweets(scraper *Scraper, query string, count int) ([]*TweetResult, error) {
	tweets := make([]*TweetResult, 0, count)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scraper.SetSearchMode(twitterscraper.SearchLatest)

	log.Printf("Searching for tweets with query: %s (max: %d)", query, count)
	log.Printf("Login status: %v", scraper.IsLoggedIn())

	for tweetScraped := range scraper.SearchTweets(ctx, query, count) {
		if tweetScraped.Error != nil {
			return nil, fmt.Errorf("error scraping tweet: %v", tweetScraped.Error)
		}

		tweetResult := convertTweet(tweetScraped.Tweet)
		tweets = append(tweets, tweetResult)

		log.Printf("Found tweet: @%s", tweetResult.Username)
	}

	return tweets, nil
}

// printResults 输出结果
func printResults(tweets []*TweetResult) {
	fmt.Printf("\n=== 搜索结果 (%d 条推文) ===\n\n", len(tweets))

	for i, tweet := range tweets {
		fmt.Printf("--- 推文 %d ---\n", i+1)
		fmt.Printf("用户: @%s\n", tweet.Username)
		fmt.Printf("时间: %s\n", tweet.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("内容: %s\n", tweet.Text)
		fmt.Printf("互动: ❤️ %d | 🔄 %d | 💬 %d\n", tweet.Likes, tweet.Retweets, tweet.Replies)
		fmt.Println()
	}
}

func resolveOutputDir() string {
	if customDir := os.Getenv("COOKIES_DIR"); customDir != "" {
		return customDir
	}
	if strings.ToLower(os.Getenv("RUNNING_IN_DOCKER")) == "true" {
		return "/app/cookies"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "cookies"
	}

	// 优先使用当前目录下的 cookies
	first := filepath.Join(cwd, "cookies")
	if _, err := os.Stat(first); err == nil {
		return first
	}

	// 退回到上级目录
	second := filepath.Join(filepath.Dir(cwd), "cookies")
	if _, err := os.Stat(second); err == nil {
		return second
	}

	return first
}

func resolveAccountEnv() string {
	return os.Getenv("X_ACCOUNT")
}

func defaultCookiePath(account string) string {
	if account == "" {
		return ""
	}
	outputDir := resolveOutputDir()
	return filepath.Join(outputDir, fmt.Sprintf("%s_twitter_cookies.json", account))
}

func resolveCookieFile(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("TWITTER_COOKIE_FILE"); env != "" {
		return env
	}
	return defaultCookiePath(resolveAccountEnv())
}

func resolveQuery(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("SCRAPER_TEST_QUERY"); env != "" {
		return env
	}
	if account := resolveAccountEnv(); account != "" {
		return fmt.Sprintf("from:%s", account)
	}
	return ""
}

func resolveCount(flagValue int) int {
	if env := os.Getenv("SCRAPER_TEST_COUNT"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			return v
		}
	}
	return flagValue
}

func resolveProxy(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("TWITTER_SCRAPER_PROXY")
}

func main() {
	var (
		cookieFile = flag.String("cookies", "", "Cookie 文件路径 (必需)")
		query      = flag.String("query", "", "搜索查询 (必需)")
		count      = flag.Int("count", 10, "最大结果数量")
		jsonOutput = flag.Bool("json", false, "输出 JSON 格式")
		proxy      = flag.String("proxy", "", "代理地址，支持 socks5，例如 socks5://127.0.0.1:1080")
	)
	flag.Parse()

	resolvedCookieFile := resolveCookieFile(*cookieFile)
	resolvedQuery := resolveQuery(*query)
	resolvedCount := resolveCount(*count)
	resolvedProxy := resolveProxy(*proxy)

	if resolvedCookieFile == "" || resolvedQuery == "" {
		fmt.Println("用法: go run main.go -cookies <cookie文件路径> -query <搜索查询> [-count <数量>] [-json] [-proxy <代理URL>]")
		os.Exit(1)
	}

	log.Printf("使用 cookie 文件: %s", resolvedCookieFile)
	log.Printf("搜索查询: %s (max %d)", resolvedQuery, resolvedCount)
	if resolvedProxy != "" {
		log.Printf("代理: %s", resolvedProxy)
	}

	// 创建 scraper 并设置跳过验证
	scraper := NewScraper()
	scraper.SetSkipLoginVerification(true)

	// 设置代理（支持 socks5 和 http）
	if resolvedProxy != "" {
		if err := scraper.SetProxy(resolvedProxy); err != nil {
			log.Fatalf("Failed to set proxy: %v", err)
		}
	}

	// 加载 cookies
	cookies, err := loadCookiesFromFile(scraper, resolvedCookieFile)
	if err != nil {
		log.Fatalf("Failed to load cookies: %v", err)
	}

	// 通过独立的 HTTP 调用再次校验 cookies 是否仍然有效
	if err := verifyCookiesDirectly(cookies, resolvedProxy); err != nil {
		log.Fatalf("Failed to verify cookies via direct request: %v", err)
	}

	// 验证登录状态，确保搜索流程可以访问需要认证的接口
	if err := verifyLoginStatus(scraper); err != nil {
		log.Fatalf("Failed to verify login: %v", err)
	}

	// 执行搜索
	tweets, err := searchTweets(scraper, resolvedQuery, resolvedCount)
	if err != nil {
		log.Fatalf("Failed to search tweets: %v", err)
	}

	// 输出结果
	if *jsonOutput {
		jsonData, err := json.MarshalIndent(tweets, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal JSON: %v", err)
		}
		fmt.Println(string(jsonData))
	} else {
		printResults(tweets)
	}

	log.Printf("Successfully scraped %d tweets", len(tweets))
}
