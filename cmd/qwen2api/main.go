package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qwen2api/internal/account"
	"qwen2api/internal/admin"
	"qwen2api/internal/auth"
	"qwen2api/internal/cleanup"
	"qwen2api/internal/config"
	"qwen2api/internal/lingma/lingmaipc"
	lingmaservice "qwen2api/internal/lingma/service"
	"qwen2api/internal/logging"
	"qwen2api/internal/metrics"
	"qwen2api/internal/openai"
	"qwen2api/internal/qwen"
	"qwen2api/internal/server"
	"qwen2api/internal/storage"
)

func main() {
	if err := config.EnsureDotEnv(config.DefaultEnvPath); err != nil {
		panic(err)
	}
	config.LoadDotEnv(config.DefaultEnvPath)
	cfg := config.Load()
	logger := logging.New(cfg.DebugMode)

	if len(cfg.APIKeys) == 0 {
		logger.ErrorModule("APP", "请务必设置 API_KEY 环境变量")
		os.Exit(1)
	}

	store, err := storage.NewAccountStore(cfg)
	if err != nil {
		logger.ErrorModule("APP", "初始化账号存储失败: %v", err)
		os.Exit(1)
	}
	conversationStore, err := storage.NewConversationStore(cfg)
	if err != nil {
		logger.ErrorModule("APP", "初始化会话存储失败: %v", err)
		os.Exit(1)
	}

	keyring := auth.NewKeyring(cfg.APIKeys, cfg.AdminKey)
	runtime := config.NewRuntime(cfg)
	stats := metrics.NewDashboardStats()
	qwenClient := qwen.NewClient(cfg, logger)
	lingmaService := newLingmaService(cfg)
	accountService := account.NewService(cfg, runtime, store, qwenClient, logger)
	conversationSessions := openai.NewConversationSessionService(conversationStore, logger)
	chatTracker, err := storage.NewChatTracker(cfg.RedisURL)
	if err != nil {
		logger.WarnModule("APP", "初始化对话追踪器失败: %v", err)
		chatTracker = nil
	}

	cleanupService := cleanup.NewService(cfg, runtime, accountService, qwenClient, chatTracker, logger)
	cleanupService.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	defer accountService.Close()
	defer func() {
		if err := lingmaService.Close(); err != nil {
			logger.WarnModule("LINGMA", "Lingma service close failed: %v", err)
		}
	}()
	defer cleanupService.Stop()

	openAIHandler := openai.NewHandler(cfg, runtime, qwenClient, lingmaService, accountService, conversationSessions, chatTracker, stats, logger)
	adminHandler := admin.NewHandler(cfg, runtime, keyring, accountService, openAIHandler, stats, logger)
	httpServer := server.New(cfg, keyring, openAIHandler, adminHandler, stats, logger)
	serverErrCh := make(chan error, 1)

	go func() {
		logger.InfoModule("APP", "服务器启动中，监听 %s:%d", cfg.ListenAddressOrDefault(), cfg.ListenPort)
		serverErrCh <- httpServer.ListenAndServe()
	}()

	go func() {
		logger.InfoModule("ACCOUNT", "账号池后台初始化开始")
		if initErr := accountService.Initialize(context.Background()); initErr != nil {
			logger.ErrorModule("ACCOUNT", "账号池后台初始化失败: %v", initErr)
			return
		}
		logger.InfoModule("ACCOUNT", "账号池后台初始化完成")
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	case err = <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorModule("APP", "服务器启动失败: %v", err)
			os.Exit(1)
		}
	}
}

func newLingmaService(cfg config.Config) *lingmaservice.Service {
	transport, err := lingmaipc.ParseTransport(cfg.LingmaTransport)
	if err != nil {
		transport = lingmaipc.TransportAuto
	}

	backend := lingmaservice.BackendMode(cfg.LingmaBackend)
	if backend != lingmaservice.BackendRemote && backend != lingmaservice.BackendIPC {
		backend = lingmaservice.BackendRemote
	}

	sessionMode := lingmaservice.SessionMode(cfg.LingmaSessionMode)
	if sessionMode != lingmaservice.SessionModeFresh &&
		sessionMode != lingmaservice.SessionModeReuse &&
		sessionMode != lingmaservice.SessionModeAuto {
		sessionMode = lingmaservice.SessionModeAuto
	}

	return lingmaservice.New(lingmaservice.Config{
		Backend:               backend,
		Transport:             transport,
		Pipe:                  cfg.LingmaPipe,
		WebSocketURL:          cfg.LingmaWebSocketURL,
		RemoteBaseURL:         cfg.LingmaRemoteBaseURL,
		RemoteAuthFile:        cfg.LingmaRemoteAuthFile,
		RemoteVersion:         cfg.LingmaRemoteVersion,
		Model:                 cfg.LingmaModel,
		SessionMode:           sessionMode,
		Timeout:               time.Duration(cfg.LingmaTimeoutSeconds) * time.Second,
		RemoteFallbackEnabled: cfg.LingmaFallback,
		RemoteFallbackModels:  cfg.LingmaFallbackModels,
	})
}
