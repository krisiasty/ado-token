package main

import (
	"context"
	"errors"
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

const (
	retryInterval         = 30 * time.Second
	refreshAttemptTimeout = 60 * time.Second // upper bound on a single AAD round-trip
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				return slog.String(attr.Key, attr.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return attr
		},
	}))

	cfg, err := config.Load(logger)
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

	logger.Info("starting ado-token helper",
		"output_secret", fmt.Sprintf("%s/%s", cfg.OutputSecretNamespace, cfg.OutputSecretName),
		"credentials_secret", fmt.Sprintf("%s/%s", cfg.CredentialsSecretNamespace, cfg.CredentialsSecretName),
	)

	for {
		state.recordAttempt()

		attemptCtx, cancel := context.WithTimeout(ctx, refreshAttemptTimeout)
		next, refreshErr := doRefresh(attemptCtx, cfg, kube)
		cancel()
		if ctx.Err() != nil {
			logger.Info("shutting down")
			return
		}

		if refreshErr != nil {
			next = retryInterval
			state.recordFailure(next)
			logger.Error("token refresh failed", "error", refreshErr, "retry_in", next.Round(time.Second).String())
		} else {
			state.recordSuccess(next)
			logger.Info("token refreshed", "next_refresh_in", next.Round(time.Second).String())
		}

		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			logger.Info("shutting down")
			return
		case <-timer.C:
		}
	}
}

func doRefresh(ctx context.Context, cfg *config.Config, kube kubernetes.Interface) (time.Duration, error) {
	creds, err := kubeclient.ReadCredentials(ctx, kube, cfg.CredentialsSecretNamespace, cfg.CredentialsSecretName)
	if err != nil {
		return 0, err
	}

	token, err := auth.FetchToken(ctx, creds.TenantID, creds.ClientID, creds.ClientSecret)
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
	mu            sync.RWMutex
	nextAttemptAt time.Time
	ready         bool
}

func (s *healthState) recordAttempt() {
	s.mu.Lock()
	s.nextAttemptAt = time.Time{} // clear while attempt is in progress
	s.mu.Unlock()
}

func (s *healthState) recordSuccess(next time.Duration) {
	s.mu.Lock()
	s.ready = true
	s.nextAttemptAt = time.Now().Add(next)
	s.mu.Unlock()
}

func (s *healthState) recordFailure(next time.Duration) {
	s.mu.Lock()
	s.nextAttemptAt = time.Now().Add(next)
	s.mu.Unlock()
}

// isLive returns true if the refresh loop is on schedule.
// A 2× retryInterval grace window absorbs slow AAD responses.
func (s *healthState) isLive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nextAttemptAt.IsZero() {
		return true // startup or attempt in progress
	}
	return time.Now().Before(s.nextAttemptAt.Add(2 * retryInterval))
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

	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() { //nolint:gosec // ctx is already cancelled here; a fresh context is required for the shutdown timeout
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("health server error", "error", err)
	}
}
