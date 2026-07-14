package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/hyperfleet"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	apphandlers "github.com/openshift/rosa-hyperfleet-api/pkg/handlers"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/zoa"
)

// Server represents the API server
type Server struct {
	cfg            *config.Config
	logger         *slog.Logger
	apiServer      *http.Server
	healthServer   *http.Server
	metricsServer  *http.Server
	healthHandler  *apphandlers.HealthHandler
	zoaReconciler  *zoa.Reconciler
}

// New creates a new Server instance
func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	ctx := context.Background()

	// Create Maestro client
	maestroClient := maestro.NewClient(cfg.Maestro, logger)

	// Create Hyperfleet client
	hyperfleetClient := hyperfleet.NewClient(cfg.Hyperfleet, logger)

	// Create handlers
	healthHandler := apphandlers.NewHealthHandler()
	infoHandler := apphandlers.NewInfoHandler()
	mgmtClusterHandler := apphandlers.NewManagementClusterHandler(maestroClient, logger)
	resourceBundleHandler := apphandlers.NewResourceBundleHandler(maestroClient, logger)
	workHandler := apphandlers.NewWorkHandler(maestroClient, logger)
	clusterHandler := apphandlers.NewClusterHandler(hyperfleetClient, maestroClient, logger)
	nodePoolHandler := apphandlers.NewNodePoolHandler(maestroClient, logger)

	// Create legacy authorization middleware (for non-authz routes)
	authMiddleware := middleware.NewAuthorization(cfg.AllowedAccounts, logger)

	// Create API router
	apiRouter := mux.NewRouter()
	apiRouter.Use(middleware.Identity)

	// Initialize authz components if enabled
	var privilegedMiddleware *middleware.Privileged
	var accountCheckMiddleware *middleware.AccountCheck
	var authzMiddleware *middleware.Authz

	if cfg.Authz != nil && cfg.Authz.Enabled {
		// Create DynamoDB client
		dynamoClient, err := client.NewDynamoDBClient(ctx, cfg.Authz.AWSRegion, cfg.Authz.DynamoDBEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create DynamoDB client: %w", err)
		}

		// Create AVP client (or mock for local testing)
		var avpClient client.AVPClient
		if cfg.Authz.CedarAgentEndpoint != "" {
			// Use mock AVP client with cedar-agent for local testing
			avpClient = client.NewMockAVPClient(cfg.Authz.CedarAgentEndpoint, logger)
			logger.Info("using MockAVPClient with cedar-agent", "endpoint", cfg.Authz.CedarAgentEndpoint)
		} else {
			avpClient, err = client.NewAVPClient(ctx, cfg.Authz.AWSRegion)
			if err != nil {
				return nil, fmt.Errorf("failed to create AVP client: %w", err)
			}
		}

		// Create authorizer (implements both Checker and Service)
		authorizer := authz.New(cfg.Authz, dynamoClient, avpClient, logger)

		// Create authz middleware
		privilegedMiddleware = middleware.NewPrivileged(authorizer, logger)
		accountCheckMiddleware = middleware.NewAccountCheck(authorizer, logger)
		adminCheckMiddleware := middleware.NewAdminCheck(authorizer, logger)
		authzMiddleware = middleware.NewAuthz(authorizer, cfg.Authz.Enabled, cfg.Authz.AWSRegion, logger)

		// Create authz handlers
		accountsHandler := apphandlers.NewAccountsHandler(authorizer, logger)
		authzHandler := apphandlers.NewAuthzHandler(authorizer, authorizer, logger)

		// Account management routes (privileged only)
		accountsRouter := apiRouter.PathPrefix("/api/v0/accounts").Subrouter()
		accountsRouter.Use(privilegedMiddleware.CheckPrivileged)
		accountsRouter.Use(privilegedMiddleware.RequirePrivileged)
		accountsRouter.HandleFunc("", accountsHandler.Create).Methods(http.MethodPost)
		accountsRouter.HandleFunc("", accountsHandler.List).Methods(http.MethodGet)
		accountsRouter.HandleFunc("/{id}", accountsHandler.Get).Methods(http.MethodGet)
		accountsRouter.HandleFunc("/{id}", accountsHandler.Delete).Methods(http.MethodDelete)

		// Authorization check route (requires provisioned account, open to all users)
		checkRouter := apiRouter.PathPrefix("/api/v0/authz/check").Subrouter()
		checkRouter.Use(privilegedMiddleware.CheckPrivileged)
		checkRouter.Use(accountCheckMiddleware.RequireProvisioned)
		checkRouter.HandleFunc("", authzHandler.CheckAuthorization).Methods(http.MethodPost)

		// Authorization management routes (require provisioned account + admin)
		authzRouter := apiRouter.PathPrefix("/api/v0/authz").Subrouter()
		authzRouter.Use(privilegedMiddleware.CheckPrivileged)
		authzRouter.Use(accountCheckMiddleware.RequireProvisioned)
		authzRouter.Use(adminCheckMiddleware.RequireAdmin)

		// Policy routes
		authzRouter.HandleFunc("/policies", authzHandler.CreatePolicy).Methods(http.MethodPost)
		authzRouter.HandleFunc("/policies", authzHandler.ListPolicies).Methods(http.MethodGet)
		authzRouter.HandleFunc("/policies/{id}", authzHandler.GetPolicy).Methods(http.MethodGet)
		authzRouter.HandleFunc("/policies/{id}", authzHandler.UpdatePolicy).Methods(http.MethodPut)
		authzRouter.HandleFunc("/policies/{id}", authzHandler.DeletePolicy).Methods(http.MethodDelete)

		// Group routes
		authzRouter.HandleFunc("/groups", authzHandler.CreateGroup).Methods(http.MethodPost)
		authzRouter.HandleFunc("/groups", authzHandler.ListGroups).Methods(http.MethodGet)
		authzRouter.HandleFunc("/groups/{id}", authzHandler.GetGroup).Methods(http.MethodGet)
		authzRouter.HandleFunc("/groups/{id}", authzHandler.DeleteGroup).Methods(http.MethodDelete)
		authzRouter.HandleFunc("/groups/{id}/members", authzHandler.UpdateGroupMembers).Methods(http.MethodPut)
		authzRouter.HandleFunc("/groups/{id}/members", authzHandler.ListGroupMembers).Methods(http.MethodGet)

		// Attachment routes
		authzRouter.HandleFunc("/attachments", authzHandler.CreateAttachment).Methods(http.MethodPost)
		authzRouter.HandleFunc("/attachments", authzHandler.ListAttachments).Methods(http.MethodGet)
		authzRouter.HandleFunc("/attachments/{id}", authzHandler.DeleteAttachment).Methods(http.MethodDelete)

		// Admin routes
		authzRouter.HandleFunc("/admins", authzHandler.AddAdmin).Methods(http.MethodPost)
		authzRouter.HandleFunc("/admins", authzHandler.ListAdmins).Methods(http.MethodGet)
		authzRouter.HandleFunc("/admins/{arn:.*}", authzHandler.RemoveAdmin).Methods(http.MethodDelete)

		logger.Info("Cedar/AVP authorization enabled")
	}

	// Management cluster routes (require allowed account)
	mgmtRouter := apiRouter.PathPrefix("/api/v0/management_clusters").Subrouter()
	if authzMiddleware != nil {
		mgmtRouter.Use(privilegedMiddleware.CheckPrivileged)
		mgmtRouter.Use(authzMiddleware.Authorize)
	} else {
		mgmtRouter.Use(authMiddleware.RequireAllowedAccount)
	}
	mgmtRouter.HandleFunc("", mgmtClusterHandler.Create).Methods(http.MethodPost)
	mgmtRouter.HandleFunc("", mgmtClusterHandler.List).Methods(http.MethodGet)
	mgmtRouter.HandleFunc("/{id}", mgmtClusterHandler.Get).Methods(http.MethodGet)

	// Resource bundle routes (require allowed account)
	rbRouter := apiRouter.PathPrefix("/api/v0/resource_bundles").Subrouter()
	if authzMiddleware != nil {
		rbRouter.Use(privilegedMiddleware.CheckPrivileged)
		rbRouter.Use(authzMiddleware.Authorize)
	} else {
		rbRouter.Use(authMiddleware.RequireAllowedAccount)
	}
	rbRouter.HandleFunc("", resourceBundleHandler.List).Methods(http.MethodGet)
	rbRouter.HandleFunc("/{id}", resourceBundleHandler.Delete).Methods(http.MethodDelete)

	// Work routes (require allowed account)
	workRouter := apiRouter.PathPrefix("/api/v0/work").Subrouter()
	if authzMiddleware != nil {
		workRouter.Use(privilegedMiddleware.CheckPrivileged)
		workRouter.Use(authzMiddleware.Authorize)
	} else {
		workRouter.Use(authMiddleware.RequireAllowedAccount)
	}
	workRouter.HandleFunc("", workHandler.Create).Methods(http.MethodPost)

	// Cluster routes (user-facing, require authz)
	clusterRouter := apiRouter.PathPrefix("/api/v0/clusters").Subrouter()
	if authzMiddleware != nil {
		clusterRouter.Use(privilegedMiddleware.CheckPrivileged)
		clusterRouter.Use(authzMiddleware.Authorize)
	} else {
		clusterRouter.Use(authMiddleware.RequireAllowedAccount)
	}
	clusterRouter.HandleFunc("", clusterHandler.List).Methods(http.MethodGet)
	clusterRouter.HandleFunc("", clusterHandler.Create).Methods(http.MethodPost)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Get).Methods(http.MethodGet)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Update).Methods(http.MethodPatch, http.MethodPut)
	clusterRouter.HandleFunc("/{id}", clusterHandler.Delete).Methods(http.MethodDelete)
	clusterRouter.HandleFunc("/{id}/statuses", clusterHandler.GetStatus).Methods(http.MethodGet)

	// NodePool routes (user-facing, require authz)
	nodePoolRouter := apiRouter.PathPrefix("/api/v0/nodepools").Subrouter()
	if authzMiddleware != nil {
		nodePoolRouter.Use(privilegedMiddleware.CheckPrivileged)
		nodePoolRouter.Use(authzMiddleware.Authorize)
	} else {
		nodePoolRouter.Use(authMiddleware.RequireAllowedAccount)
	}
	nodePoolRouter.HandleFunc("", nodePoolHandler.List).Methods(http.MethodGet)
	nodePoolRouter.HandleFunc("", nodePoolHandler.Create).Methods(http.MethodPost)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Get).Methods(http.MethodGet)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Update).Methods(http.MethodPut)
	nodePoolRouter.HandleFunc("/{id}", nodePoolHandler.Delete).Methods(http.MethodDelete)
	nodePoolRouter.HandleFunc("/{id}/status", nodePoolHandler.GetStatus).Methods(http.MethodGet)

	// ZOA Trusted Actions routes (privileged)
	var zoaReconciler *zoa.Reconciler
	if cfg.Zoa.Enabled {
		jobConfig, err := zoa.LoadJobConfig(cfg.Zoa.JobConfigDir)
		if err != nil {
			return nil, fmt.Errorf("failed to load ZOA job config from %s: %w", cfg.Zoa.JobConfigDir, err)
		}

		zoaDynamoClient, err := client.NewDynamoDBClient(ctx, cfg.Zoa.AWSRegion, "")
		if err != nil {
			return nil, fmt.Errorf("failed to create ZOA DynamoDB client: %w", err)
		}

		zoaStore := zoa.NewDynamoExecutionStore(cfg.Zoa.TableName, zoaDynamoClient, logger, jobConfig.DynamoDBTTLDays)

		var auditStore zoa.AuditStore
		if cfg.Zoa.AuditTableName != "" {
			auditStore = zoa.NewDynamoAuditStore(cfg.Zoa.AuditTableName, zoaDynamoClient, logger, jobConfig.DynamoDBTTLDays)
			logger.Info("ZOA audit logging enabled", "table", cfg.Zoa.AuditTableName)
		}

		zoaRegistry := zoa.NewTemplateRegistry(logger)
		if err := zoaRegistry.LoadFromDir(cfg.Zoa.TemplatesDir); err != nil {
			return nil, fmt.Errorf("failed to load ZOA templates from %s: %w", cfg.Zoa.TemplatesDir, err)
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Zoa.AWSRegion))
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config for ZOA S3: %w", err)
		}
		s3Client := s3.NewFromConfig(awsCfg)

		zoaHandler := apphandlers.NewZoaHandler(zoaStore, zoaRegistry, maestroClient, s3Client, apphandlers.ZoaConfig{
			BucketName: cfg.Zoa.BucketName,
			JobConfig:  jobConfig,
			AuditStore: auditStore,
		}, logger)

		zoaRouter := apiRouter.PathPrefix("/api/v0/trusted-actions").Subrouter()
		if privilegedMiddleware != nil {
			zoaRouter.Use(privilegedMiddleware.CheckPrivileged)
		} else {
			zoaRouter.Use(authMiddleware.RequireAllowedAccount)
		}
		zoaRouter.HandleFunc("/audit", zoaHandler.AuditList).Methods(http.MethodGet)
		zoaRouter.HandleFunc("/runs", zoaHandler.List).Methods(http.MethodGet)
		zoaRouter.HandleFunc("/runs/{id}", zoaHandler.Get).Methods(http.MethodGet)
		zoaRouter.HandleFunc("/{action}/run", zoaHandler.Create).Methods(http.MethodPost)
		zoaRouter.HandleFunc("/{action}", zoaHandler.Describe).Methods(http.MethodGet)
		zoaRouter.HandleFunc("", zoaHandler.Catalog).Methods(http.MethodGet)

		zoaReconciler = zoa.NewReconciler(zoaStore, zoaRegistry, maestroClient, jobConfig, cfg.Zoa.PollInterval, logger)
		logger.Info("ZOA trusted actions enabled", "table", cfg.Zoa.TableName, "bucket", cfg.Zoa.BucketName)
	}

	// Health and info routes on API server (no auth required)
	apiRouter.HandleFunc("/api/v0/live", healthHandler.Liveness).Methods(http.MethodGet)
	apiRouter.HandleFunc("/api/v0/ready", healthHandler.Readiness).Methods(http.MethodGet)
	apiRouter.HandleFunc("/api/v0/info", infoHandler.Info).Methods(http.MethodGet)

	// ROSAENG-1236: CORS disabled for machine-to-machine API
	// Previous wildcard CORS was a security vulnerability
	// apiHandler := handlers.CORS(
	// 	handlers.AllowedOrigins([]string{"*"}),
	// 	handlers.AllowedMethods([]string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut}),
	// 	handlers.AllowedHeaders([]string{"Content-Type", "Authorization"}),
	// )(apiRouter)
	apiHandler := apiRouter

	// Create health router
	healthRouter := mux.NewRouter()
	healthRouter.HandleFunc("/healthz", healthHandler.Liveness).Methods(http.MethodGet)
	healthRouter.HandleFunc("/readyz", healthHandler.Readiness).Methods(http.MethodGet)

	// Create metrics router
	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	return &Server{
		cfg:           cfg,
		logger:        logger,
		zoaReconciler: zoaReconciler,
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

// Run starts all servers and blocks until context is cancelled
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)

	// Start ZOA reconciler if enabled
	if s.zoaReconciler != nil {
		go s.zoaReconciler.Run(ctx)
	}

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

	s.logger.Info("all servers stopped")
	return nil
}
