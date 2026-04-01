package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/container"
	"github.com/beriberikix/zephyr-simulator-server/internal/handlers"
	pb_db "github.com/beriberikix/zephyr-simulator-server/internal/pocketbase"
	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/beriberikix/zephyr-simulator-server/internal/uart"
	"github.com/joho/godotenv"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const (
	anonSessionTTL      = 2 * time.Hour
	prunerInterval      = 30 * time.Minute
	timeoutCheckInterval = 30 * time.Second
	defaultPCAPRetention      = 24 * time.Hour
	defaultPCAPPrunerInterval = 1 * time.Hour
)

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		log.Printf("Invalid %s=%q, using default %s", key, raw, fallback)
		return fallback
	}

	return parsed
}

// resolveIdentity is a PocketBase route middleware that reads the caller's
// identity from e.Auth (set by PocketBase from the Authorization header) and
// from the X-Anonymous-ID header, then injects it into the request context so
// all existing http.HandlerFunc handlers can access it via handlers.GetIdentity.
func resolveIdentity(e *core.RequestEvent) error {
	var id handlers.Identity

	if e.Auth != nil {
		id = handlers.Identity{
			Type:    handlers.OwnerTypeUser,
			ID:      e.Auth.Id,
			IsAdmin: e.Auth.GetString("role") == "admin",
		}
	} else {
		raw := e.Request.Header.Get("X-Anonymous-ID")
		if handlers.IsValidAnonID(raw) {
			id = handlers.Identity{Type: handlers.OwnerTypeAnonymous, ID: raw}
		}
	}

	e.Request = handlers.SetIdentity(e.Request, id)
	return e.Next()
}

func main() {
	_ = godotenv.Load()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	baseImageURL := os.Getenv("BASE_IMAGE_URL")
	if baseImageURL == "" {
		baseImageURL = "zephyr-emulator:latest"
	}

	runtimeName := os.Getenv("RUNTIME_NAME")
	if runtimeName == "" {
		runtimeName = "runsc"
	}

	stateFilePath := os.Getenv("STATE_FILE_PATH")
	// PocketBase is the default persistence store.
	// STATE_FILE_PATH is optional and only used for one-time legacy JSON import.
	if stateFilePath == "" {
		log.Println("STATE_FILE_PATH not set; using PocketBase-only persistence")
	} else {
		log.Printf("STATE_FILE_PATH=%q configured; legacy JSON import enabled", stateFilePath)
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "/data/pb_data"
	}

	pcapRetention := durationFromEnv("PCAP_RETENTION", defaultPCAPRetention)
	pcapPrunerInterval := durationFromEnv("PCAP_PRUNER_INTERVAL", defaultPCAPPrunerInterval)

	containerMgr, err := container.NewManager(baseImageURL, runtimeName)
	if err != nil {
		log.Fatalf("Failed to initialize container manager: %v", err)
	}
	defer containerMgr.Close()

	uartMux := uart.NewMultiplexer("", 10000)
	if err := uartMux.Start(); err != nil {
		log.Fatalf("Failed to start UART multiplexer: %v", err)
	}
	defer uartMux.Stop()
	handlers.SetUARTLifecycleHooks(
		func(session *types.Session) error {
			if session == nil || session.ID == "" || len(session.UARTBins) == 0 {
				return nil
			}
			return uartMux.RegisterSessionBackends(session.ID, session.UARTBins)
		},
		func(sessionID string) {
			uartMux.UnregisterSessionBackends(sessionID)
		},
	)

	// Always inject --dir so every subcommand (serve, superuser, migrate, …)
	// operates on the same database path regardless of how the binary is invoked.
	// Only add it when not already present to avoid duplicates.
	hasDir := false
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--dir") {
			hasDir = true
			break
		}
	}
	if !hasDir {
		os.Args = append(os.Args, "--dir="+dataDir)
	}

	// Default to the "serve" subcommand when no args are provided so the
	// binary can be run without explicit CLI flags.
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "--dir") {
		os.Args = append([]string{os.Args[0], "serve", "--http=0.0.0.0:" + port}, os.Args[1:]...)
	}

	app := pocketbase.New()

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		if err := pb_db.InitCollections(se.App); err != nil {
			return err
		}
		handlers.SetStateSnapshotStore(pb_db.NewSnapshotStore(se.App))
		if err := handlers.ConfigureStatePersistence(stateFilePath); err != nil {
			return err
		}

		r := se.Router

		// Security response headers on our API routes only.
		// Skip /_/ (PocketBase admin dashboard) which sets its own CSP.
		r.BindFunc(func(e *core.RequestEvent) error {
			if !strings.HasPrefix(e.Request.URL.Path, "/_/") {
				applySecurityHeaders(e.Response)
			}
			return e.Next()
		})

		// Resolve caller identity (PocketBase auth token or anonymous UUID).
		r.BindFunc(resolveIdentity)

		// Start anonymous session pruner.
		go runAnonSessionPruner(containerMgr)
		go runSessionTimeoutEnforcer(containerMgr)
		go runPCAPArtifactPruner(pcapRetention, pcapPrunerInterval)

		// Binary management
		r.POST("/api/binaries", wrap(handlers.HandleUploadBinary(containerMgr)))
		r.GET("/api/binaries", wrap(handlers.HandleListBinaries()))
		r.GET("/api/binaries/{id}", wrap(handlers.HandleGetBinary()))
		r.DELETE("/api/binaries/{id}", wrap(handlers.HandleDeleteBinary()))

		// Session management
		r.POST("/api/sessions", wrap(handlers.HandleCreateSession(containerMgr)))
		r.GET("/api/sessions", wrap(handlers.HandleListSessions()))
		r.GET("/api/sessions/{id}", wrap(handlers.HandleGetSession()))
		r.PATCH("/api/sessions/{id}", wrap(handlers.HandleUpdateSession(containerMgr)))
		r.DELETE("/api/sessions/{id}", wrap(handlers.HandleDeleteSession(containerMgr)))
		r.POST("/api/sessions/{id}/start", wrap(handlers.HandleStartSession(containerMgr)))
		r.POST("/api/sessions/{id}/stop", wrap(handlers.HandleStopSession(containerMgr)))
		r.POST("/api/sessions/{id}/pause", wrap(handlers.HandlePauseSession(containerMgr)))
		r.POST("/api/sessions/{id}/resume", wrap(handlers.HandleResumeSession(containerMgr)))
		r.POST("/api/sessions/{id}/restore", wrap(handlers.HandleRestoreSession(containerMgr)))

		// SSE
		r.GET("/api/sse", wrap(handlers.HandleSSE(containerMgr, uartMux)))

		// PCAP / coverage / sanitizers / network
		r.GET("/api/sessions/{id}/pcap", wrap(handlers.HandleDownloadPCAP()))
		r.GET("/api/sessions/{id}/coverage", wrap(handlers.HandleDownloadCoverage()))
		r.GET("/api/sessions/{id}/sanitizers", wrap(handlers.HandleDownloadSanitizers()))
		r.GET("/api/sessions/{id}/sanitizers/report", wrap(handlers.HandleGetSanitizerReport()))
		r.POST("/api/network/setup", wrap(handlers.HandleSetupHostNetwork()))
		r.POST("/api/network/benchmark", wrap(handlers.HandleNetworkBenchmark()))

		// GDB debugging
		r.GET("/api/sessions/{id}/debug-target", wrap(handlers.HandleGetDebugTarget()))
		r.GET("/api/sessions/{id}/debug/ws", wrap(handlers.HandleDebugWebSocketProxy()))
		r.GET("/api/sessions/{id}/debug/breakpoints", wrap(handlers.HandleListDebugBreakpoints()))
		r.POST("/api/sessions/{id}/debug/breakpoints", wrap(handlers.HandleAddDebugBreakpoint()))
		r.DELETE("/api/sessions/{id}/debug/breakpoints/{number}", wrap(handlers.HandleDeleteDebugBreakpoint()))
		r.GET("/api/sessions/{id}/debug/stack", wrap(handlers.HandleGetDebugStackTrace()))

		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// wrap adapts a standard http.HandlerFunc for use in PocketBase's router.
func wrap(h http.HandlerFunc) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		h(e.Response, e.Request)
		return nil
	}
}

// applySecurityHeaders sets hardening response headers. Kept as a standalone
// function so it can be independently tested.
func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
}

// securityHeaders returns an http.Handler that applies security headers before
// calling next. Exists only to satisfy the existing test without rewriting it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}

// runAnonSessionPruner periodically deletes expired anonymous sessions.
// It runs forever in a goroutine and stops their containers before deletion.
func runAnonSessionPruner(mgr handlers.ContainerManager) {
	ticker := time.NewTicker(prunerInterval)
	defer ticker.Stop()
	for range ticker.C {
		handlers.PruneAnonymousSessions(mgr, anonSessionTTL)
	}
}

// runSessionTimeoutEnforcer periodically stops sessions whose configured
// timeout has elapsed.
func runSessionTimeoutEnforcer(mgr handlers.ContainerManager) {
	ticker := time.NewTicker(timeoutCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		handlers.EnforceSessionTimeouts(mgr)
	}
}

// runPCAPArtifactPruner periodically removes stale capture files that are no
// longer referenced by any active session.
func runPCAPArtifactPruner(retention, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		handlers.PruneOrphanedPCAPArtifacts(retention)
	}
}
