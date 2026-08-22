package testinfra

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/client"
	"github.com/jackc/pgx/v5"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/schema"
)

// proxyReadyTimeout is the maximum time to wait for the Toxiproxy API port to be
// reachable after the container starts.
const proxyReadyTimeout = 2 * time.Minute

type ProxiedDB struct {
	DirectConnStr  string
	ProxiedConnStr string
	Proxy          *toxiproxy.Proxy
	ToxiClient     *toxiproxy.Client
	network        string
	pgContainer    string
	toxiContainer  string
	stopSignals    func()
	cleanup        func() // removes only created resources; idempotent
}

// StartPostgresWithProxy starts a Postgres container and a Toxiproxy container
// on a shared podman network. Returns direct and proxied connection strings.
func StartPostgresWithProxy() *ProxiedDB {
	apiPort := freePortNoT()
	proxyPort := freePortNoT()
	networkName := fmt.Sprintf("pgctl-net-%d", apiPort)
	pgContainer := fmt.Sprintf("pgctl-pg-%d", apiPort)
	toxiContainer := fmt.Sprintf("pgctl-toxi-%d", apiPort)

	// Track each resource so partial-failure cleanup removes only what was created.
	var networkCreated, pgCreated, toxiCreated bool

	// cleanup removes only the resources that were successfully created.
	// Idempotent: each bool is cleared after the corresponding teardown so a
	// second call (e.g. Stop() after the signal handler fired) is a no-op.
	cleanup := func() {
		if toxiCreated {
			if err := podmanCmd("stop", toxiContainer).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "testinfra: stop container %s: %v\n", toxiContainer, err)
			}
			toxiCreated = false
		}
		if pgCreated {
			if err := podmanCmd("stop", pgContainer).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "testinfra: stop container %s: %v\n", pgContainer, err)
			}
			pgCreated = false
		}
		if networkCreated {
			if err := podmanCmd("network", "rm", networkName).Run(); err != nil {
				fmt.Fprintf(os.Stderr, "testinfra: remove network %s: %v\n", networkName, err)
			}
			networkCreated = false
		}
	}

	// Register the signal handler before creating any resources so SIGTERM/SIGINT
	// during setup still tears down whatever was already created.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return // closed by stopSignals on normal teardown
		}
		cleanup()
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

	// Create podman network
	run("podman", "network", "create", networkName)
	networkCreated = true

	// Start Postgres on the network
	run("podman", "run", "-d", "--rm",
		"--name", pgContainer,
		"--network", networkName,
		"-e", "POSTGRES_DB=pgctl_test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_PASSWORD=test",
		"docker.io/library/postgres:16-alpine")
	pgCreated = true

	// Start Toxiproxy on the same network, expose API and proxy ports
	run("podman", "run", "-d", "--rm",
		"--name", toxiContainer,
		"--network", networkName,
		"-p", fmt.Sprintf("%d:8474", apiPort),
		"-p", fmt.Sprintf("%d:15432", proxyPort),
		"ghcr.io/shopify/toxiproxy:latest")
	toxiCreated = true

	// Wait for postgres to be ready (connect via the network using pg container name)
	// First, get the direct port mapping for postgres
	pgDirectPort := freePortNoT()
	// Actually, we need postgres accessible from host too for direct connections.
	// Let's restart postgres with a host port mapping.
	run("podman", "stop", pgContainer)

	run("podman", "run", "-d", "--rm",
		"--name", pgContainer,
		"--network", networkName,
		"-p", fmt.Sprintf("%d:5432", pgDirectPort),
		"-e", "POSTGRES_DB=pgctl_test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_PASSWORD=test",
		"docker.io/library/postgres:16-alpine")

	directConnStr := fmt.Sprintf("postgres://test:test@localhost:%d/pgctl_test?sslmode=disable", pgDirectPort)
	waitForPostgresNoT(directConnStr)

	// Wait for toxiproxy API
	waitForTCP(fmt.Sprintf("localhost:%d", apiPort), proxyReadyTimeout)

	// Create the proxy: toxiproxy listens on 0.0.0.0:15432 -> pgContainer:5432
	toxiClient := toxiproxy.NewClient(fmt.Sprintf("http://localhost:%d", apiPort))
	proxy, err := toxiClient.CreateProxy("postgres",
		"0.0.0.0:15432",
		fmt.Sprintf("%s:5432", pgContainer))
	if err != nil {
		panic(fmt.Sprintf("create toxiproxy proxy: %v", err))
	}

	proxiedConnStr := fmt.Sprintf("postgres://test:test@localhost:%d/pgctl_test?sslmode=disable", proxyPort)

	// Wait for proxied connection to work
	waitForPostgresNoT(proxiedConnStr)

	// Run migrations via direct connection
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, directConnStr)
	if err != nil {
		panic(fmt.Sprintf("connect for migration: %v", err))
	}
	if err := schema.Migrate(ctx, conn); err != nil {
		_ = conn.Close(ctx)
		panic(fmt.Sprintf("migrate: %v", err))
	}
	_ = conn.Close(ctx)

	return &ProxiedDB{
		DirectConnStr:  directConnStr,
		ProxiedConnStr: proxiedConnStr,
		Proxy:          proxy,
		ToxiClient:     toxiClient,
		network:        networkName,
		pgContainer:    pgContainer,
		toxiContainer:  toxiContainer,
		stopSignals:    stopSignals,
		cleanup:        cleanup,
	}
}

func (p *ProxiedDB) Stop() {
	if p.stopSignals != nil {
		p.stopSignals()
	}
	if p.cleanup != nil {
		p.cleanup()
	}
}

func (p *ProxiedDB) DirectConn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, p.DirectConnStr)
}

func (p *ProxiedDB) ProxiedConn(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, p.ProxiedConnStr)
}

func run(name string, args ...string) { //nolint:unparam
	out, err := podmanCmd(args...).CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("%s %v: %v\n%s", name, args, err, out))
	}
}

func waitForTCP(addr string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	panic(fmt.Sprintf("tcp %s not ready after %s", addr, timeout))
}
