package main

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "监听地址；非回环地址必须配置登录")
	data := flag.String("data", "data", "SQLite、仓库及 Pi 会话目录")
	worker := flag.String("pi-worker", "runtime/worker.mjs", "Pi SDK 适配入口")
	sandboxSocket := flag.String("sandbox-socket", "", "受限沙盒 runner 的 Unix socket；项目需单独启用")
	interval := flag.Duration("sync-interval", 5*time.Minute, "Git 同步间隔；0 关闭定时同步")
	authPath := flag.String("auth-config", "", "登录配置文件；默认读取数据目录中的 auth.json")
	timeouts := agent.DefaultRunTimeouts()
	flag.DurationVar(&timeouts.FirstResponse, "agent-first-response-timeout", timeouts.FirstResponse, "每次模型请求等待首次内容的时限")
	flag.DurationVar(&timeouts.StreamIdle, "agent-stream-idle-timeout", timeouts.StreamIdle, "模型流式响应连续无新增内容的时限")
	flag.DurationVar(&timeouts.Total, "agent-total-timeout", timeouts.Total, "整次 Agent 运行总时限（包括工具执行）")
	flag.Parse()
	if e := timeouts.Validate(); e != nil {
		log.Fatal(e)
	}
	root, e := filepath.Abs(*data)
	if e != nil {
		log.Fatal(e)
	}
	workerPath, e := filepath.Abs(*worker)
	if e != nil {
		log.Fatal(e)
	}
	configPath := *authPath
	if configPath == "" {
		configPath = filepath.Join(root, "auth.json")
	}
	authFile, e := auth.Load(configPath, *authPath != "")
	if e != nil {
		log.Fatal(e)
	}
	if e = validateListenAddress(*addr, authFile != nil); e != nil {
		log.Fatal(e)
	}
	db, e := sqlite.Open(filepath.Join(root, "ai-pm.db"))
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	service := &app.Service{Store: db}
	vault, e := credential.Open(filepath.Join(root, "repository.key"), db)
	if e != nil {
		log.Fatal(e)
	}
	repos := &gitrepo.Repositories{Root: filepath.Join(root, "repos"), App: service, Vault: vault}
	manager := agent.New(ctx, service, repos, workerPath, root)
	manager.SandboxSocket = *sandboxSocket
	manager.Timeouts = timeouts
	if e = manager.Recover(ctx); e != nil {
		log.Fatal(e)
	}
	service.OnChange = manager.Changed
	manager.StartReplyWorker()
	api := &server.Server{App: service, Git: repos, Agent: manager}
	if authFile != nil {
		api.Auth, e = auth.Open(ctx, authFile, db)
		if e != nil {
			log.Fatal(e)
		}
		log.Printf("登录已启用 · 访问地址: %s", api.Auth.PublicURL())
		if api.Auth.Accounts != nil && api.Auth.Accounts.SetupToken() != "" {
			log.Printf("尚无平台账号：打开访问地址，用初始化口令 %s 创建首位平台管理员（口令每次启动更新，用后失效）", api.Auth.Accounts.SetupToken())
		}
	}
	httpServer := &http.Server{Addr: *addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	if *interval > 0 {
		go func() {
			timer := time.NewTicker(*interval)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					projects, err := db.ListProjects(ctx)
					if err != nil {
						continue
					}
					_ = repos.ExpireSnapshots(time.Now().Add(-7 * 24 * time.Hour))
					for _, p := range projects {
						if p.Repository != "" {
							if err = repos.Sync(ctx, p.ID); err != nil {
								log.Printf("项目 %s 同步未完成", p.ID)
							}
						}
					}
				}
			}
		}()
	}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Printf("智驱 AI PM: http://%s · SQLite: %s", *addr, filepath.Join(root, "ai-pm.db"))
	if e = httpServer.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		cancel()
		manager.Close()
		log.Fatal(e)
	}
	cancel()
	manager.Close()
}

func validateListenAddress(addr string, authenticated bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("监听地址必须包含主机和端口")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("监听端口无效")
	}
	if host != "localhost" && !net.ParseIP(host).IsLoopback() && !authenticated {
		return errors.New("非回环监听地址必须配置登录（auth.json）；本机免登录模式请使用 127.0.0.1")
	}
	return nil
}
