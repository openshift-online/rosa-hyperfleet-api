package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	apphandlers "github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/handlers"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/ratelimit"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// Server represents the API server
type Server struct {
	cfg           *config.Config
	logger        *slog.Logger
	apiServer     *http.Server
	healthServer  *http.Server
	metricsServer *http.Server
	healthHandler *apphandlers.HealthHandler
	redisCloser   io.Closer
}

// New creates a new Server instance. The dbClient is used by cluster,
// nodepool, and management cluster handlers.
func New(cfg *config.Config, dbClient *hyperfleetdb.Client, logger *slog.Logger) (*Server, error) {
	// Create handlers
	healthHandler := apphandlers.NewHealthHandler(logger)
	infoHandler := apphandlers.NewInfoHandler(logger)
	mgmtClusterHandler := apphandlers.NewManagementClusterHandler(dbClient, logger)
	clusterHandler := apphandlers.NewClusterHandler(dbClient, cfg.Regional.OIDCIssuerBaseURL, cfg.Regional.DefaultClusterExpiration, logger)
	nodePoolHandler := apphandlers.NewNodePoolHandler(dbClient, logger)
	oidcConfigHandler := apphandlers.NewOidcConfigHandler(dbClient, cfg.Regional.OIDCIssuerBaseURL, cfg.Regional.AWSRegion, logger)

	// Create API router
	apiRouter := mux.NewRouter()
	// Override gorilla/mux plain-text fallbacks with metav1.Status responses so
	// unmatched routes and unsupported methods match the api.WriteError format.
	apiRouter.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := api.WriteError(w, apphandlers.ErrRouteNotFound); err != nil {
			logger.Error("failed to write 404 response", "error", err)
		}
	})
	apiRouter.MethodNotAllowedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := api.WriteError(w, apphandlers.ErrRouteMethodNotAllowed); err != nil {
			logger.Error("failed to write 405 response", "error", err)
		}
	})
	apiRouter.Use(middleware.Identity)

	// Rate limiting middleware
	var redisCloser io.Closer
	if cfg.RateLimit.Enabled {
		var rlCfg *ratelimit.Config
		if cfg.RateLimit.ConfigFile != "" {
			var err error
			rlCfg, err = ratelimit.LoadConfig(cfg.RateLimit.ConfigFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load rate limit config: %w", err)
			}
		} else {
			rlCfg = ratelimit.NewDefaultConfig(
				cfg.RateLimit.DefaultRate,
				cfg.RateLimit.DefaultBurst,
				cfg.RateLimit.DefaultWindow,
			)
		}

		var limiter ratelimit.RateLimiter
		if cfg.RateLimit.InMemory {
			limiter = ratelimit.NewLocalRateLimiter()
			logger.Info("rate limiting enabled", "backend", "in-memory")
		} else {
			if cfg.RateLimit.RedisAddr == "" {
				return nil, fmt.Errorf("REDIS_ENDPOINT is required when rate limiting is enabled and not in-memory mode")
			}
			rdb := redis.NewClient(&redis.Options{
				Addr: cfg.RateLimit.RedisAddr,
				TLSConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
			})
			limiter = ratelimit.NewRedisLimiter(rdb)
			redisCloser = rdb
			logger.Info("rate limiting enabled", "backend", "redis")
		}

		rl := ratelimit.New(limiter, rlCfg, logger)
		apiRouter.Use(rl.Middleware)
	}

	// Management cluster routes
	mgmtRouter := apiRouter.PathPrefix("/api/v0/management_clusters").Subrouter()
	mgmtRouter.HandleFunc("", mgmtClusterHandler.Create).Methods(http.MethodPost)
	mgmtRouter.HandleFunc("", mgmtClusterHandler.List).Methods(http.MethodGet)
	mgmtRouter.HandleFunc("/{id}", mgmtClusterHandler.Get).Methods(http.MethodGet)

	// Cluster routes
	clusterRouter := apiRouter.PathPrefix("/api/v0/clusters").Subrouter()
	clusterRouter.HandleFunc("", clusterHandler.List).Methods(http.MethodGet)
	clusterRouter.HandleFunc("", clusterHandler.Create).Methods(http.MethodPost)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Get).Methods(http.MethodGet)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Update).Methods(http.MethodPatch, http.MethodPut)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Delete).Methods(http.MethodDelete)

	// NodePool routes
	nodePoolRouter := apiRouter.PathPrefix("/api/v0/nodepools").Subrouter()
	nodePoolRouter.HandleFunc("", nodePoolHandler.List).Methods(http.MethodGet)
	nodePoolRouter.HandleFunc("", nodePoolHandler.Create).Methods(http.MethodPost)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Get).Methods(http.MethodGet)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Update).Methods(http.MethodPut)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Delete).Methods(http.MethodDelete)

	// OidcConfig routes
	oidcConfigRouter := apiRouter.PathPrefix("/api/v0/oidc_configs").Subrouter()
	oidcConfigRouter.HandleFunc("", oidcConfigHandler.List).Methods(http.MethodGet)
	oidcConfigRouter.HandleFunc("", oidcConfigHandler.Create).Methods(http.MethodPost)
	oidcConfigRouter.HandleFunc("/{id}", oidcConfigHandler.Get).Methods(http.MethodGet)
	oidcConfigRouter.HandleFunc("/{id}", oidcConfigHandler.Delete).Methods(http.MethodDelete)

	// Health and info routes on API server (no auth required)
	apiRouter.HandleFunc("/api/v0/live", healthHandler.Liveness).Methods(http.MethodGet)
	apiRouter.HandleFunc("/api/v0/ready", healthHandler.Readiness).Methods(http.MethodGet)
	apiRouter.HandleFunc("/api/v0/info", infoHandler.Info).Methods(http.MethodGet)

	// ROSAENG-1236: CORS disabled for machine-to-machine API
	apiHandler := apiRouter

	// Create health router
	healthRouter := mux.NewRouter()
	healthRouter.HandleFunc("/healthz", healthHandler.Liveness).Methods(http.MethodGet)
	healthRouter.HandleFunc("/readyz", healthHandler.Readiness).Methods(http.MethodGet)

	// Create metrics router
	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	return &Server{
		cfg:         cfg,
		logger:      logger,
		redisCloser: redisCloser,
		apiServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Server.APIBindAddress, cfg.Server.APIPort),
			Handler:      apiHandler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		healthServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Server.HealthBindAddress, cfg.Server.HealthPort),
			Handler:      healthRouter,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		metricsServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Server.MetricsBindAddress, cfg.Server.MetricsPort),
			Handler:      metricsRouter,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		healthHandler: healthHandler,
	}, nil
}

// Run starts all servers and blocks until context is canceled
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)

	// Start health server
	go func() {
		s.logger.Info("starting health server", "addr", s.healthServer.Addr)
		if err := s.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("health server error: %w", err)
		}
	}()

	// Start metrics server
	go func() {
		s.logger.Info("starting metrics server", "addr", s.metricsServer.Addr)
		if err := s.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics server error: %w", err)
		}
	}()

	// Start API server
	go func() {
		s.logger.Info("starting API server", "addr", s.apiServer.Addr)
		if err := s.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("API server error: %w", err)
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		s.logger.Info("shutting down servers")
		return s.shutdown()
	case err := <-errCh:
		return err
	}
}

func (s *Server) shutdown() error {
	// Mark as not ready to stop receiving traffic
	s.healthHandler.SetReady(false)

	// Give load balancers time to detect we're not ready
	time.Sleep(5 * time.Second)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer cancel()

	// Shutdown servers in order
	if err := s.apiServer.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("failed to shutdown API server", "error", err)
	}

	if err := s.metricsServer.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("failed to shutdown metrics server", "error", err)
	}

	if err := s.healthServer.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("failed to shutdown health server", "error", err)
	}

	if s.redisCloser != nil {
		if err := s.redisCloser.Close(); err != nil {
			s.logger.Error("failed to close Redis client", "error", err)
		}
	}

	s.logger.Info("all servers stopped")
	return nil
}
