package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/all"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/adapter/fake"
	"github.com/aslafy-z/hauth/internal/source"
	"github.com/aslafy-z/hauth/internal/store"
	filestore "github.com/aslafy-z/hauth/internal/store/file"
	k8sstore "github.com/aslafy-z/hauth/internal/store/k8s"
	"github.com/aslafy-z/hauth/internal/syncer"
)

type rootFlags struct {
	user         string
	instance     string
	storeKind    string
	storePath    string
	namespace    string
	secretPrefix string
	stateDir     string
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newRootCmd() *cobra.Command {
	f := &rootFlags{}
	cmd := &cobra.Command{
		Use:           "hauth",
		Short:         "Auth broker for AI agent harness CLIs in remote workspaces",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&f.user, "user", envOr("HAUTH_USER", os.Getenv("USER")), "store identity of the human user")
	pf.StringVar(&f.instance, "instance", os.Getenv("HAUTH_INSTANCE"), "identity of this workspace (default user@hostname)")
	pf.StringVar(&f.storeKind, "store", envOr("HAUTH_STORE", "file"), "store backend: file, k8s, or none")
	pf.StringVar(&f.storePath, "store-path", os.Getenv("HAUTH_STORE_PATH"), "file store root (default ~/.local/share/hauth/store)")
	pf.StringVar(&f.namespace, "namespace", os.Getenv("HAUTH_NAMESPACE"), "k8s store namespace (default: pod namespace)")
	pf.StringVar(&f.secretPrefix, "secret-prefix", envOr("HAUTH_SECRET_PREFIX", k8sstore.DefaultPrefix), "k8s store Secret name prefix")
	pf.StringVar(&f.stateDir, "state-dir", os.Getenv("HAUTH_STATE_DIR"), "restored-state directory (default ~/.local/state/hauth)")

	cmd.AddCommand(newAgentCmd(f), newLoginCmd(f), newStatusCmd(f), newStoreCmd(f), newVersionCmd())
	return cmd
}

type env struct {
	flags    *rootFlags
	user     string
	instance string
	home     string
	stateDir string
	store    store.Store    // nil when --store none
	syncer   *syncer.Syncer // nil when store is nil
}

// setup resolves identity and paths, registers adapters, and builds the
// store and syncer. Every subcommand starts here.
func setup(f *rootFlags, logf func(string, ...any)) (*env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	e := &env{flags: f, user: f.user, instance: f.instance, home: home, stateDir: f.stateDir}
	if e.user == "" {
		return nil, fmt.Errorf("no user identity: set --user or HAUTH_USER")
	}
	if e.instance == "" {
		host, _ := os.Hostname()
		e.instance = e.user + "@" + host
	}
	if e.stateDir == "" {
		e.stateDir = source.StateDir()
	}

	cfg := common.Config{Home: home, StateDir: e.stateDir}
	registerAdapters(cfg)

	switch f.storeKind {
	case "none":
	case "file":
		root := f.storePath
		if root == "" {
			root = filepath.Join(home, ".local", "share", "hauth", "store")
		}
		e.store = filestore.New(root)
	case "k8s", "kubernetes":
		ns := f.namespace
		if ns == "" {
			ns = podNamespace()
		}
		if ns == "" {
			return nil, fmt.Errorf("k8s store needs a namespace: set --namespace or HAUTH_NAMESPACE")
		}
		st, err := k8sstore.NewInCluster(ns, f.secretPrefix)
		if err != nil {
			return nil, fmt.Errorf("k8s store: %w", err)
		}
		e.store = st
	default:
		return nil, fmt.Errorf("unknown store backend %q", f.storeKind)
	}
	if e.store != nil {
		e.syncer = syncer.New(syncer.Config{
			Store:    e.store,
			User:     e.user,
			Instance: e.instance,
			Home:     home,
			StateDir: e.stateDir,
			Logf:     logf,
		})
	}
	return e, nil
}

var adaptersRegistered bool

func registerAdapters(cfg common.Config) {
	if adaptersRegistered {
		return
	}
	adaptersRegistered = true
	all.Register(cfg)
	if os.Getenv("HAUTH_ENABLE_FAKE") == "1" {
		cli := os.Getenv("HAUTH_FAKE_CLI")
		if cli == "" {
			cli, _ = exec.LookPath("fakecli")
		}
		if cli != "" {
			adapter.Register(fake.New(cfg, cli))
		}
	}
}

func podNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func splitOrigins(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
