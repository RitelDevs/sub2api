package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthReauthManagedWorkerNeedsNoEnvironmentToken(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
	t.Setenv("OPENAI_REAUTH_WORKER_VERSION", "2.21.5-ritel-reauth.1")
	t.Setenv("DATA_DIR", t.TempDir())
	s := &OpenAIOAuthReauthService{}
	cfg := &config.Config{}
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 4040
	s.configureWorker(cfg)
	defer s.stopWorker()
	require.NotNil(t, s.worker)
	require.Len(t, s.workerToken, 64)
	configured, valid := s.WorkerAuthentication(s.workerToken)
	require.True(t, configured)
	require.True(t, valid)
	_, valid = s.WorkerAuthentication(strings.Repeat("x", 64))
	require.False(t, valid)
	status := s.WorkerStatus()
	require.Equal(t, "managed", status.Mode)
	require.Equal(t, "idle", status.State)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), s.workerToken)
	require.Error(t, s.checkWorkerMode(OpenAIOAuthReauthModeEmailOTPURL))
}

func TestOpenAIOAuthReauthPreservesExternalWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	t.Setenv("OPENAI_REAUTH_WORKER_VERSION", "2.21.5-ritel-reauth.1")
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil)
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication(strings.Repeat("a", 32))
	require.True(t, configured)
	require.True(t, valid)
	require.Equal(t, "external_offline", s.WorkerStatus().Reason)
}

func TestOpenAIOAuthReauthWorkerHeartbeatKeepsExternalWorkerAvailable(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", strings.Repeat("a", 32))
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil)
	s.MarkWorkerSeen()
	require.Equal(t, "running", s.WorkerStatus().State)
}

func TestOpenAIOAuthReauthShortExplicitTokenDoesNotStartCompetingWorker(t *testing.T) {
	t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "short")
	s := &OpenAIOAuthReauthService{}
	s.configureWorker(nil)
	require.Nil(t, s.worker)
	configured, valid := s.WorkerAuthentication("short")
	require.False(t, configured)
	require.False(t, valid)
	require.Equal(t, "external_not_configured", s.WorkerStatus().Reason)
}

type offlineReauthRuntimeTransport func(*http.Request) (*http.Response, error)

func (f offlineReauthRuntimeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOpenAIOAuthReauthManagedWorkerUsesConfiguredRelease(t *testing.T) {
	for _, tt := range []struct{ version, release, repository string }{
		{" v2.21.5-ritel-reauth.1 ", "v2.21.5-ritel-reauth.1", "RitelDevs/sub2api"},
		{"2.18.0", "v2.18.0", "ranxi2001/sub2api"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
			t.Setenv("OPENAI_REAUTH_WORKER_VERSION", tt.version)
			t.Setenv("DATA_DIR", t.TempDir())
			originalTransport := http.DefaultTransport
			requests := make(chan string, 1)
			http.DefaultTransport = offlineReauthRuntimeTransport(func(r *http.Request) (*http.Response, error) {
				requests <- r.URL.String()
				return nil, errors.New("offline runtime test")
			})
			s := &OpenAIOAuthReauthService{}
			defer func() { s.stopWorker(); http.DefaultTransport = originalTransport }()
			s.configureWorker(&config.Config{})
			s.EnsureWorker()
			select {
			case got := <-requests:
				require.Equal(t, "https://api.github.com/repos/"+tt.repository+"/releases/tags/"+tt.release, got)
			case <-time.After(3 * time.Second):
				t.Fatal("managed worker did not request the configured release")
			}
			s.stopWorker()
			require.Equal(t, "runtime_install_failed", s.WorkerStatus().Reason)
		})
	}
}

func TestOpenAIOAuthReauthManagedWorkerRequiresConfiguredRelease(t *testing.T) {
	for _, version := range []string{"", "latest", "../../worker"} {
		t.Run(version, func(t *testing.T) {
			t.Setenv("OPENAI_REAUTH_WORKER_TOKEN", "")
			t.Setenv("OPENAI_REAUTH_WORKER_VERSION", version)
			t.Setenv("DATA_DIR", t.TempDir())
			s := &OpenAIOAuthReauthService{}
			defer s.stopWorker()
			s.configureWorker(&config.Config{})
			require.Equal(t, "release_required", s.WorkerStatus().Reason)
			require.Error(t, s.checkWorkerMode(OpenAIOAuthReauthModePasswordTOTP))
		})
	}
}
