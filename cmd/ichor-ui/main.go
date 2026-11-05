/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// ichor-ui is the Go backend that embeds the Preact SPA (docs/design/architecture.md,
// "CLI and UI"). It serves the SPA's static assets and, from internal/uiapi
// (docs/adr/0018, docs/adr/0035), the Connect API's ClusterService, MachineService,
// NodeService and PatchService: phase 5's read and approval surface, plus phase 6's
// config-editing backend. It holds a read-only Talos identity and reads and writes
// TalosMachine and TalosConfigPatch CRs; it never talks to a Talos node's mutating API
// (architecture.md "Trust").
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/stdr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	runtime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ichorv1alpha1.AddToScheme(scheme))
}

// mustSub roots an embedded FS at dir so callers can address files the way the
// browser does ("index.html", "assets/app.js"), regardless of which build tag
// populated webFS (see webfs_dev.go and webfs_embed.go).
func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// spaHandler serves files as a single-page app: real files pass through
// unchanged, anything else (a client-side route) falls back to index.html so
// the SPA's own router can handle it.
func spaHandler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(files, name); errors.Is(err, fs.ErrNotExist) {
			fallback := r.Clone(r.Context())
			fallback.URL.Path = "/"
			fileServer.ServeHTTP(w, fallback)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", ":8443", "HTTPS address ichor-ui listens on; it never serves plaintext here (docs/adr/0105)")
	httpAddr := flag.String("http-addr", "", "DEPRECATED no-op: plain HTTP was removed (docs/adr/0105); accepted for one release so an old Deployment still starts")
	oidcIssuer := flag.String("oidc-issuer", os.Getenv("ICHOR_OIDC_ISSUER"), "OIDC issuer URL; empty disables login (docs/adr/0058)")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("ICHOR_OIDC_CLIENT_ID"), "OIDC client ID (public client, PKCE); also the expected token audience")
	oidcUserClaim := flag.String("oidc-username-claim", os.Getenv("ICHOR_OIDC_USERNAME_CLAIM"), "claim used as the Kubernetes user name (default email)")
	oidcGroupsClaim := flag.String("oidc-groups-claim", os.Getenv("ICHOR_OIDC_GROUPS_CLAIM"), "claim used for Kubernetes groups (default groups)")
	oidcUserPrefix := flag.String("oidc-username-prefix", os.Getenv("ICHOR_OIDC_USERNAME_PREFIX"), "prefix added to the user name")
	oidcGroupsPrefix := flag.String("oidc-groups-prefix", os.Getenv("ICHOR_OIDC_GROUPS_PREFIX"), "prefix added to each group")
	allowAnonymous := flag.Bool("allow-unauthenticated", envTrue("ICHOR_ALLOW_UNAUTHENTICATED"),
		"serve every call without a sign-in, using this pod's ServiceAccount; without it and without OIDC or Ichor users only read-only calls are served (docs/adr/0108)")
	// Ichor's own users and issuer (docs/adr/0094); off by default.
	var users usersFlags

	flag.BoolVar(&users.enabled, "users-enabled", envTrue("ICHOR_USERS_ENABLED"), "serve Ichor's own OIDC issuer and accept its users (docs/adr/0094)")
	flag.StringVar(&users.externalURL, "external-url", os.Getenv("ICHOR_USERS_EXTERNAL_URL"), "https URL of ichor-ui as browsers and the CLI reach it; the issuer of Ichor's tokens")
	flag.StringVar(&users.internalAddr, "internal-addr", ":8444", "HTTPS address of the listener the kube-apiserver reads discovery and JWKS from")
	flag.StringVar(&users.internalTLSDir, "internal-tls-dir", "/etc/ichor/oidc-tls", "directory with tls.crt and tls.key of the internal listener")
	flag.StringVar(&users.internalJWKSURI, "internal-jwks-uri", os.Getenv("ICHOR_USERS_INTERNAL_JWKS_URI"), "jwks_uri of the internal discovery document (default: the host the apiserver connected to, so a new Service ClusterIP is followed)")
	flag.StringVar(&users.signingKeysDir, "signing-keys-dir", "/etc/ichor/oidc-signing", "directory with keys.json, the signing keys the controller keeps")
	flag.DurationVar(&users.tokenTTL, "token-ttl", 5*time.Minute, "lifetime of an ID token (1m to 1h); spec.users.tokenTTL of the IchorInstallation wins when set")
	flag.DurationVar(&users.refreshTTL, "refresh-lifetime", 12*time.Hour, "absolute lifetime of a refresh token (1h to 30d)")

	flag.StringVar(&users.trustedProxies, "trusted-proxy-cidrs", os.Getenv("ICHOR_TRUSTED_PROXY_CIDRS"), "comma-separated CIDRs of proxies whose X-Forwarded-For the login rate limits trust; empty: the TCP peer is the client (docs/adr/0108)")
	flag.StringVar(&users.kubeAPIServerURL, "kube-api-server-url", "", "Kubernetes API URL put in users' kubeconfigs (default: the cluster's spec.endpoint)")
	flag.StringVar(&users.kubeAPITLSServerName, "kube-api-tls-server-name", "", "tls-server-name in users' kubeconfigs, for a --kube-api-server-url host outside the apiserver certificate's SANs")

	tlsDir := flag.String("tls-dir", "/etc/ichor/ui-tls", "directory with tls.crt and tls.key of the Ichor-made certificate, served when the operator's pair does not exist")
	tlsCert := flag.String("tls-cert-file", "", "the operator's certificate; wins over --tls-dir when the files exist (with --tls-key-file)")
	tlsKey := flag.String("tls-key-file", "", "private key for --tls-cert-file")

	_ = flag.Bool("reclaim-enabled", false, "DEPRECATED no-op: Reset and add is decided by RBAC alone (docs/adr/0107); accepted for one release so an old Deployment still starts")
	settingsReadOnly := flag.Bool("settings-read-only", envTrue("ICHOR_SETTINGS_READ_ONLY"), "ignore the runtime settings in the IchorInstallation spec (docs/adr/0105; chart settings.readOnly)")

	flag.Parse()

	// certreload's certwatcher logs through controller-runtime's logger, which dumps a stack
	// trace when none is set; send it to the std logger this binary uses everywhere else.
	ctrl.SetLogger(stdr.New(log.Default()))

	if err := users.validate(); err != nil {
		log.Fatalf("ichor-ui: %v", err)
	}

	restCfg := ctrl.GetConfigOrDie()

	c, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("ichor-ui: building Kubernetes client: %v", err)
	}

	// The runtime settings of the IchorInstallation, read live (ADR 0105): a set spec field wins over the flags.
	liveSettings := &settings.Reader{
		Client: c, ReadOnly: *settingsReadOnly, TTL: 5 * time.Second,
		Fallback: settings.Fallback{TokenTTL: users.tokenTTL, RefreshLifetime: users.refreshTTL},
	}

	// draining is closed once, right when shutdown starts, so every in-flight
	// StreamNodeLogs call notices on its next select and returns
	// CodeUnavailable -- "graceful termination that asks clients to
	// reconnect" (architecture.md "CLI and UI").
	draining := make(chan struct{})

	server := &uiapi.Server{
		Client: c,
		// Same meaning as TalosMachineReconciler.ControllerNamespace on the
		// controller side (ADR 0011): where loadBundle resolves the secrets
		// bundle Secret, never the TalosCluster's own namespace.
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		NodeTalosClient:     uiapi.NewMachineryNodeTalosClient,
		// Support bundle, etcd snapshot and packet capture downloads (docs/adr/0084), each
		// gated by a Kubernetes RBAC verb of the caller's own.
		DownloadTalosClient: uiapi.NewMachineryDownloadTalosClient,
		Draining:            draining,
	}

	// OIDC (docs/adr/0058). Off: writes use this pod's ServiceAccount, so anyone who
	// can reach ichor-ui can write; that is said here and by /auth/config (the UI
	// shows a banner). On: every RPC needs a verified ID token and the Kubernetes
	// API is called as that user. Discovery failing is fatal -- never fall back
	// to open.
	var authn *uiapi.Authenticator

	if *oidcIssuer != "" {
		dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		authn, err = uiapi.NewAuthenticator(dctx, uiapi.OIDCConfig{
			Issuer: *oidcIssuer, ClientID: *oidcClientID,
			UsernameClaim: *oidcUserClaim, GroupsClaim: *oidcGroupsClaim,
			UsernamePrefix: *oidcUserPrefix, GroupsPrefix: *oidcGroupsPrefix,
		})

		cancel()

		if err != nil {
			log.Fatalf("ichor-ui: %v", err)
		}

		log.Printf("ichor-ui: OIDC login enabled (issuer %s); Kubernetes calls are made as the signed-in user", *oidcIssuer)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		issuer      *oidcissuer.Issuer
		internalSrv *http.Server
	)

	if users.enabled {
		cache := &uiapi.UserCache{Reader: c, Namespace: server.ControllerNamespace}
		server.Users = uiapi.NewUserSettings(cache, users.externalURL)
		server.Users.KubeAPIServerURL, server.Users.KubeAPITLSServerName = users.kubeAPIServerURL, users.kubeAPITLSServerName

		var verifier localVerifier

		issuer, verifier, err = newIssuer(context.Background(), users, liveSettings, &uiapi.IssuerBackend{
			Users: cache, Reader: c, Writer: c, Namespace: server.ControllerNamespace, Settings: server.Users,
		})
		if err != nil {
			log.Fatalf("ichor-ui: %v", err)
		}

		authn = authn.WithLocal(users.externalURL, verifier, cache)
		internalSrv = internalServer(ctx, users, issuer)

		log.Printf("ichor-ui: Ichor users enabled (issuer %s)", users.externalURL)
	}

	if authn != nil {
		server.Impersonate = uiapi.NewImpersonator(restCfg, client.Options{Scheme: scheme, Mapper: c.RESTMapper()})
	} else if *allowAnonymous {
		log.Print("ichor-ui: WARNING: --allow-unauthenticated is set and no sign-in is configured; every client that " +
			"can reach ichor-ui writes with this pod's ServiceAccount permissions and is audited as \"ichor-ui\"")
	} else {
		log.Print("ichor-ui: no sign-in is configured (no OIDC, no Ichor users): serving read-only calls only; " +
			"configure sign-in, or pass --allow-unauthenticated to allow writes without it")
	}

	server.Settings = liveSettings

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(authConfig(*oidcIssuer, *oidcClientID, users, *allowAnonymous))
	})

	if issuer != nil {
		h := issuer.Handler() // the issuer's external endpoints
		mux.Handle("/.well-known/openid-configuration", h)
		mux.Handle("/oidc/", h)
	}
	mux.Handle("/", spaHandler(webFS))

	// /metrics, on the same mux and port as every Connect handler below: ichor-ui is
	// only ever reached in-cluster (architecture.md "Trust": a port-forward, no exposed
	// Service today), the same trust boundary the Connect API itself already sits
	// behind, so a second port and its own TLS/auth story (the controller's secure
	// metrics server, cmd/main.go) would buy nothing here. rpcInterceptor below is what
	// the exported RPC-latency series comes from (docs/adr/0040).
	// Since ADR 0108 it answers only a caller with a Kubernetes token that may `get` /metrics (TokenReview
	// and SubjectAccessReview, as the controller's secure metrics endpoint does).
	metricsHandler, err := authenticatedMetrics(restCfg, promhttp.Handler())
	if err != nil {
		log.Fatalf("ichor-ui: protecting /metrics: %v", err)
	}

	mux.Handle("/metrics", metricsHandler)

	mountRPC(mux, server, authn, *allowAnonymous)

	srv := httpsServer(*addr, securityHeaders(http.MaxBytesHandler(mux, bodyBackstop), contentSecurityPolicy(*oidcIssuer != "")), servingCertificate(ctx, *tlsCert, *tlsKey, *tlsDir))

	listen(srv, "HTTPS listening on "+*addr)

	if *httpAddr != "" {
		log.Printf("ichor-ui: --http-addr=%s ignored: plaintext was removed (ADR 0105); serve HTTPS on %s", *httpAddr, *addr)
	}

	if internalSrv != nil {
		listen(internalSrv, "internal HTTPS listener on "+users.internalAddr)
	}

	<-ctx.Done()
	close(draining)

	// srv.Shutdown stops accepting new connections and waits out in-flight
	// requests; closing draining above is what makes an in-flight
	// StreamNodeLogs call actually finish promptly instead of running until
	// this timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("ichor-ui: graceful shutdown failed: %v", err)
	}

	if internalSrv != nil {
		_ = internalSrv.Shutdown(shutdownCtx)
	}
}

// envTrue reports whether the environment variable is "true".
func envTrue(name string) bool { return os.Getenv(name) == "true" }

// listen starts srv in the background. The certificate comes from
// TLSConfig.GetCertificate, so the file arguments stay empty.
func listen(srv *http.Server, what string) {
	go func() {
		log.Printf("ichor-ui: %s", what)

		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("ichor-ui: %v", err)
		}
	}()
}
