package testinfra

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/schema"
)

// postgresReadyTimeout is the maximum time to wait for Postgres to accept connections
// after the container starts. 2 minutes covers cold image pulls and slow container init.
const postgresReadyTimeout = 2 * time.Minute

var podmanEnv = sync.OnceValue(func() []string {
	home := os.Getenv("HOME")
	if home == "" || home == "/" {
		tmp, err := os.MkdirTemp("", "podman-home-*")
		if err != nil {
			tmp = os.TempDir()
		}
		return append(os.Environ(),
			"HOME="+tmp,
			"XDG_RUNTIME_DIR="+tmp,
			"XDG_CONFIG_HOME="+filepath.Join(tmp, ".config"),
		)
	}
	return nil
})

func podmanCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("podman", args...)
	if env := podmanEnv(); env != nil {
		cmd.Env = env
	}
	return cmd
}

type TestDB struct {
	ConnStr     string
	container   string
	stopSignals func() // unregisters the signal handler goroutine on normal Stop()
}

func StartPostgres(t testing.TB) *TestDB {
	t.Helper()

	if dsn := os.Getenv("PGCTL_DSN"); dsn != "" {
		waitForPostgres(t, dsn)
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatalf("connect to external DB: %v", err)
		}
		if err := schema.Migrate(ctx, conn); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("migrate external DB: %v", err)
		}
		_ = conn.Close(ctx)
		return &TestDB{ConnStr: dsn}
	}

	port := freePort(t)
	container := fmt.Sprintf("pgctl-test-%d", port)

	args := []string{
		"run", "-d", "--rm",
		"--name", container,
		"-p", fmt.Sprintf("%d:5432", port),
		"-e", "POSTGRES_DB=pgctl_test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_PASSWORD=test",
		"docker.io/library/postgres:16-alpine",
	}

	out, err := podmanCmd(args...).CombinedOutput()
	if err != nil {
		t.Fatalf("podman run: %v\n%s", err, out)
	}

	connStr := fmt.Sprintf("postgres://test:test@localhost:%d/pgctl_test?sslmode=disable", port)

	t.Cleanup(func() {
		_ = podmanCmd("stop", container).Run()
	})

	waitForPostgres(t, connStr)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect for migration: %v", err)
	}
	if err := schema.Migrate(ctx, conn); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("migrate: %v", err)
	}
	_ = conn.Close(ctx)

	return &TestDB{ConnStr: connStr, container: container}
}

func (db *TestDB) Connect(t testing.TB) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, db.ConnStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func (db *TestDB) TruncateAll(t testing.TB, conn *pgx.Conn) {
	t.Helper()
	tables := []string{
		"kubernetes_resources",
		"compaction_horizon",
	}
	ctx := context.Background()
	for _, tbl := range tables {
		if _, err := conn.Exec(ctx, "TRUNCATE "+tbl+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func waitForPostgres(t testing.TB, connStr string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(postgresReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(ctx, connStr)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		err = conn.Ping(ctx)
		_ = conn.Close(ctx)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return
	}
	// show container logs on failure
	out, _ := podmanCmd("logs", extractContainer(connStr)).CombinedOutput()
	t.Fatalf("postgres not ready after %s: %v\nlogs:\n%s", postgresReadyTimeout, lastErr, out)
}

// StartPostgresForTestMain is for use in TestMain where testing.TB is not available.
// The caller must call Stop() when done.
//
// If PGCTL_DSN is set, connects to that external database instead of starting
// a Podman container. This allows running tests against Aurora or any remote
// Postgres instance.
func StartPostgresForTestMain() *TestDB {
	if dsn := os.Getenv("PGCTL_DSN"); dsn != "" {
		waitForPostgresNoT(dsn)
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			panic(fmt.Sprintf("connect to external DB: %v", err))
		}
		if err := schema.Migrate(ctx, conn); err != nil {
			_ = conn.Close(ctx)
			panic(fmt.Sprintf("migrate external DB: %v", err))
		}
		_ = conn.Close(ctx)
		return &TestDB{ConnStr: dsn}
	}

	port := freePortNoT()
	container := fmt.Sprintf("pgctl-test-%d", port)

	args := []string{
		"run", "-d", "--rm",
		"--name", container,
		"-p", fmt.Sprintf("%d:5432", port),
		"-e", "POSTGRES_DB=pgctl_test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_PASSWORD=test",
		"docker.io/library/postgres:16-alpine",
	}

	out, err := podmanCmd(args...).CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("podman run: %v\n%s", err, out))
	}

	connStr := fmt.Sprintf("postgres://test:test@localhost:%d/pgctl_test?sslmode=disable", port)

	// Stop the container on SIGTERM or SIGINT so --rm removes it even when
	// the test binary is interrupted. SIGKILL cannot be caught, but this
	// covers `go test -timeout` (which sends SIGTERM) and Ctrl-C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return // closed by stopSignals on normal teardown
		}
		if err := podmanCmd("stop", container).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "testinfra: stop container %s: %v\n", container, err)
		}
		// Re-raise so the process terminates with the correct signal after cleanup.
		signal.Reset(sig)
		p, err := os.FindProcess(os.Getpid())
		if err != nil || p.Signal(sig) != nil {
			os.Exit(1)
		}
	}()
	stopSignals := func() {
		signal.Stop(sigCh)
		close(sigCh)
	}

	waitForPostgresNoT(connStr)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		panic(fmt.Sprintf("connect for migration: %v", err))
	}
	if err := schema.Migrate(ctx, conn); err != nil {
		_ = conn.Close(ctx)
		panic(fmt.Sprintf("migrate: %v", err))
	}
	_ = conn.Close(ctx)

	return &TestDB{ConnStr: connStr, container: container, stopSignals: stopSignals}
}

func (db *TestDB) Stop() {
	if db.stopSignals != nil {
		db.stopSignals()
	}
	_ = podmanCmd("stop", db.container).Run()
}

func freePortNoT() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Sprintf("free port: %v", err))
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func waitForPostgresNoT(connStr string) {
	ctx := context.Background()
	deadline := time.Now().Add(postgresReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(ctx, connStr)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		err = conn.Ping(ctx)
		_ = conn.Close(ctx)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		return
	}
	panic(fmt.Sprintf("postgres not ready after %s: %v", postgresReadyTimeout, lastErr))
}

func extractContainer(connStr string) string {
	parts := strings.Split(connStr, ":")
	if len(parts) >= 4 {
		portPart := strings.Split(parts[3], "/")[0]
		return "pgctl-test-" + portPart
	}
	return ""
}
