package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/krisiasty/ado-token/internal/auth"
	"github.com/krisiasty/ado-token/internal/config"
	kubeclient "github.com/krisiasty/ado-token/internal/k8s"
	"k8s.io/client-go/kubernetes"
)

const retryInterval = 30 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	kube, err := kubeclient.NewClient()
	if err != nil {
		logger.Error("failed to build kubernetes client", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	state := &healthState{}
	go runHealthServer(ctx, logger, cfg.HealthPort, state)

	logger.Info("starting ado-token sidecar",
		"output_secret", fmt.Sprintf("%s/%s", cfg.OutputSecretNamespace, cfg.OutputSecretName),
		"credentials_secret", fmt.Sprintf("%s/%s", cfg.CredentialsSecretNamespace, cfg.CredentialsSecretName),
	)

	for {
		state.recordAttempt()

		next, refreshErr := doRefresh(ctx, logger, cfg, kube)
		if refreshErr != nil {
			logger.Error("token refresh failed", "error", refreshErr, "retry_in", retryInterval)
			next = retryInterval
		} else {
			state.recordSuccess()
			logger.Info("token refreshed", "next_refresh_in", next)
		}

		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return
		case <-time.After(next):
		}
	}
}

func doRefresh(ctx context.Context, logger *slog.Logger, cfg *config.Config, kube kubernetes.Interface) (time.Duration, error) {
	creds, err := kubeclient.ReadCredentials(ctx, kube, cfg.CredentialsSecretNamespace, cfg.CredentialsSecretName)
	if err != nil {
		return 0, err
	}

	token, err := auth.FetchToken(creds.TenantID, creds.ClientID, creds.ClientSecret)
	if err != nil {
		return 0, err
	}

	if err := kubeclient.UpdateSecret(ctx, kube, cfg.OutputSecretNamespace, cfg.OutputSecretName, cfg.OutputSecretKey, token.AccessToken); err != nil {
		return 0, err
	}

	return nextRefreshInterval(token.ExpiresAt, cfg.RefreshInterval), nil
}

// nextRefreshInterval returns the duration to wait before the next refresh.
// It uses 80% of the remaining token TTL as the base, with the configured
// override acting as a cap (whichever is shorter wins).
func nextRefreshInterval(expiresAt time.Time, override time.Duration) time.Duration {
	derived := time.Duration(float64(time.Until(expiresAt)) * 0.8)
	if derived <= 0 {
		return retryInterval
	}
	if override > 0 && override < derived {
		return override
	}
	return derived
}

// healthState tracks the refresh loop state for probe endpoints.
type healthState struct {
	mu          sync.RWMutex
	lastAttempt time.Time
	ready       bool
}

func (s *healthState) recordAttempt() {
	s.mu.Lock()
	s.lastAttempt = time.Now()
	s.mu.Unlock()
}

func (s *healthState) recordSuccess() {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
}

// isLive returns true if the refresh loop has made an attempt recently.
// The threshold is 2× the retry interval to tolerate slow AAD responses.
func (s *healthState) isLive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastAttempt.IsZero() {
		return true // still in startup
	}
	return time.Since(s.lastAttempt) < 2*retryInterval
}

func (s *healthState) isReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func runHealthServer(ctx context.Context, logger *slog.Logger, port string, state *healthState) {
	mux := http.NewServeMux()

	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		if state.isLive() {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "refresh loop stalled", http.StatusServiceUnavailable)
		}
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if state.isReady() {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "waiting for first successful token write", http.StatusServiceUnavailable)
		}
	})

	srv := &http.Server{Addr: ":" + port, Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("health server error", "error", err)
	}
}
