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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/open-policy-agent/cert-controller/pkg/rotator"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/certreload"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/settings"
	webhookv1alpha1 "github.com/maeshinshin/ichor/internal/webhook/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// webhookSecretName, webhookServiceName and validatingWebhookConfigName name the
// resources the in-process certificate rotator maintains; all three names come from
// the kustomize namePrefix in config/default.
const (
	webhookSecretName           = "ichor-webhook-server-cert"
	webhookServiceName          = "ichor-webhook-service"
	validatingWebhookConfigName = "ichor-validating-webhook-configuration"
	mutatingWebhookConfigName   = "ichor-mutating-webhook-configuration"

	// metricsSecretName and metricsServiceName name the second cert-controller rotator's
	// Secret and the metrics Service its certificate's DNSName matches (config/default/
	// metrics_server_cert_secret.yaml, config/default/metrics_service.yaml) -- opt-in,
	// gated on --metrics-cert-path actually being set, unlike the webhook rotator above.
	metricsSecretName  = "ichor-metrics-server-cert"
	metricsServiceName = "ichor-controller-manager-metrics-service"

	// backupSpoolDir is the emptyDir both manifests mount for TalosEtcdBackup's staged
	// (encrypted) snapshots, since the root filesystem is read-only.
	backupSpoolDir = "/var/spool/ichor"

	// restoreTailGrace is how long the process outlives its manager while an etcd restore is mid-way: the restore's own
	// bound for the wipe and the recovery, plus a margin.
	restoreTailGrace = 70 * time.Minute
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// The cert-controller rotator only ever touches the single Secret named by SecretKey,
// in this controller's own namespace. Kubernetes RBAC cannot restrict list,
// watch or create by resourceNames (the request has no single object name to check), so
// those stay namespace-scoped; get, update and patch are restricted to that one Secret.
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-webhook-server-cert,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-metrics-server-cert,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=list;watch;create
// cert-controller reads the webhook configuration through an informer, which lists and watches
// the whole kind (no object name in the request), so list and watch cannot be scoped to one
// name; get, update and patch can.
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=list;watch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,resourceNames=ichor-validating-webhook-configuration,verbs=get;update;patch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=list;watch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,resourceNames=ichor-mutating-webhook-configuration,verbs=get;update;patch

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ichorv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// certPollInterval is how often a replica checks for the mounted webhook certificate.
const certPollInterval = time.Second

// logCertStatusBeforeStart logs, once, whether certDir/certName already exists right before
// mgr.Start. On a brand-new install it does not, and that is normal: the webhook server (and
// its readiness) waits for the file instead of crashing, so no restart follows (cold-start amendment).
func logCertStatusBeforeStart(log logr.Logger, server, certDir, certName string) {
	path := filepath.Join(certDir, certName)
	if _, err := os.Stat(path); err != nil {
		log.Info("certificate not written yet for this server; expected on a brand-new install. "+
			"The server starts once the rotator (leader) or the mounted volume provides it; "+
			"not ready until then, and no restart is needed",
			"server", server, "path", path)
	}
}

// must exits the process, logging msg with kv, when err is set.
func must(err error, msg string, kv ...any) {
	if err != nil {
		setupLog.Error(err, msg, kv...)
		os.Exit(1)
	}
}

// nolint:gocyclo
func main() {
	ns := os.Getenv("POD_NAMESPACE")
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var certExpiryWarningDays int
	var userIssuer bool
	var externalURL string
	var deployedBy string
	var settingsReadOnly bool
	var noCiliumPolicies bool
	var uiServiceName string
	var refreshLifetime time.Duration
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "/tmp/k8s-webhook-server/serving-certs",
		"The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&deployedBy, "deployed-by", ichorv1alpha1.IchorManagerInstaller,
		"Who deploys Ichor, recorded as spec.deployment.manager when the controller creates the missing IchorInstallation (installer or helm; the chart passes helm).")
	flag.BoolVar(&settingsReadOnly, "settings-read-only", false,
		"Ignore the runtime settings in the IchorInstallation spec and deny writes to them: a Git or Helm deployment owns them (chart settings.readOnly).")
	flag.BoolVar(&noCiliumPolicies, "no-cilium-policies", false,
		"Tell the installer Job not to apply Ichor's CiliumNetworkPolicies even on Cilium (the chart sets it from networkPolicy.cilium.enabled=false).")
	flag.BoolVar(&userIssuer, "user-issuer", false,
		"Keep the signing keys and user credentials for ichor-ui's own OIDC issuer. The serving CA and certificate are kept either way.")
	flag.StringVar(&externalURL, "ui-external-url", "",
		"ichor-ui's public URL; its host is added to the serving certificate.")
	flag.StringVar(&uiServiceName, "ui-service-name", "",
		"Override: the name of the ClusterIP Service in front of ichor-ui. Empty finds it by the app.kubernetes.io/component=ui label.")
	flag.DurationVar(&refreshLifetime, "oidc-refresh-lifetime", controller.DefaultRefreshLifetime,
		"The longest-lived token ichor-ui signs; a rotated-out signing key is kept this long.")
	flag.IntVar(&certExpiryWarningDays, "certificate-expiry-warning-days", 90,
		"Days before a cluster CA expires at which the CertificatesExpiring condition turns True.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// To serve the metrics endpoint with a real certificate instead, point metricsCertPath (and
	// metricsCertName/metricsCertKey, if not the defaults) at a directory holding one. Ichor ships no
	// certificate-issuing dependency by default; how that directory gets populated is up to
	// the deployment.
	sigCtx := ctrl.SetupSignalHandler()

	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		// Not CertDir/CertName/KeyName: the metrics server would then read the files when it
		// starts and exit if the rotator has not written them yet (cold start). A
		// certwatcher-backed GetCertificate lets it start first and follow rotations, keeping
		// the last good certificate if a new pair fails to load.
		metricsServerOptions.TLSOpts = append(slices.Clone(tlsOpts), func(c *tls.Config) {
			c.GetCertificate = certreload.GetCertificate(sigCtx, filepath.Join(metricsCertPath, metricsCertName), filepath.Join(metricsCertPath, metricsCertKey), certreload.Interval)
		})
	}

	restConfig, err := waitForAPIServerDefault(logr.NewContext(sigCtx, setupLog), probeAddr)
	must(err, "API server unavailable")

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		Cache:                  controller.CacheOptions(ns),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "61779df1.maesh.dev",
		// Long enough to ride out an API server restart on a single control plane.
		LeaseDuration: &leaseDuration,
		RenewDeadline: &renewDeadline,
		RetryPeriod:   &retryPeriod,
		// LeaderElectionReleaseOnCancel stays off: the process outlives its manager in
		// WaitForRestoreTails (up to restoreTailGrace) and must keep the lease
		// then, or a second replica could act while an etcd restore is mid-way.
	})
	must(err, "Failed to start manager")

	evac := &controller.LonghornEvacuator{Client: mgr.GetClient()}
	// One live reader of the IchorInstallation settings for the controllers and webhooks.
	// It reads uncached with a short TTL, so a change applies within seconds and nothing needs an informer.
	settingsFallback := settings.Fallback{RefreshLifetime: refreshLifetime}
	settingsReader := &settings.Reader{
		Client: mgr.GetAPIReader(), Fallback: settingsFallback, ReadOnly: settingsReadOnly, TTL: 2 * time.Second,
	}

	webhooksUp := make(chan struct{})
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		// The webhook serving certificate has no caBundle and no Secret data shipped with
		// the chart: it is generated at runtime by cert-controller and read back
		// by controller-runtime's certwatcher via webhookCertPath/CertName/KeyName above.
		// Nothing may call mgr.GetWebhookServer() before the certificate exists:
		// controller-runtime adds the webhook server to the manager lazily on that first
		// call, and the server reads its certificate the moment it starts and exits the
		// process if the file is missing. So the server, its handlers and its readiness
		// check all wait behind webhooksUp, which closes once the rotator reports the
		// certificate ready (the leader) or the files appear on disk (any replica: the
		// rotator is leader-only, so a follower learns of the leader's write through the
		// kubelet's Secret volume sync)., cold-start amendment.
		certsReady := make(chan struct{})
		must(rotator.AddRotator(mgr, &rotator.CertRotator{
			SecretKey:      types.NamespacedName{Namespace: ns, Name: webhookSecretName},
			CertDir:        webhookCertPath,
			CertName:       webhookCertName,
			KeyName:        webhookCertKey,
			CAName:         "ichor-ca",
			CAOrganization: "ichor",
			DNSName:        fmt.Sprintf("%s.%s.svc", webhookServiceName, ns),
			IsReady:        certsReady,
			FieldOwner:     "ichor-controller",
			// Two replicas race to generate a CA on a cold start otherwise; one writer
			// for this disruptive operation follows principle 6.
			RequireLeaderElection: true,
			Webhooks: []rotator.WebhookInfo{
				{Name: validatingWebhookConfigName, Type: rotator.Validating},
				{Name: mutatingWebhookConfigName, Type: rotator.Mutating},
			},
		}), "Failed to set up webhook certificate rotation")

		go func() {
			fileCh := make(chan struct{})
			go func() {
				if waitForCertFiles(sigCtx, webhookCertPath, webhookCertName, webhookCertKey, certPollInterval) == nil {
					close(fileCh)
				}
			}()
			select {
			case <-certsReady:
				setupLog.Info("Webhook certificate ready (rotator); starting the webhook server")
			case <-fileCh:
				setupLog.Info("Webhook certificate found on disk; starting the webhook server")
			case <-sigCtx.Done():
				return
			}
			must(webhookv1alpha1.SetupTalosClusterWebhookWithManager(mgr, settingsReader), "Failed to create webhook", "webhook", "TalosCluster")
			must(webhookv1alpha1.SetupTalosConfigPatchWebhookWithManager(mgr, ns), "Failed to create webhook", "webhook", "TalosConfigPatch")
			must(webhookv1alpha1.SetupTalosMachineWebhookWithManager(mgr), "Failed to create webhook", "webhook", "TalosMachine")
			// ICHOR_BREAK_GLASS_EXEMPT_USERS adds exact usernames (comma separated) to the
			// built-in list.
			exempt := webhookv1alpha1.DefaultExemptUsers(ns, os.Getenv("POD_SERVICE_ACCOUNT"))
			for u := range strings.SplitSeq(os.Getenv("ICHOR_BREAK_GLASS_EXEMPT_USERS"), ",") {
				if u = strings.TrimSpace(u); u != "" {
					exempt = append(exempt, u)
				}
			}
			must(webhookv1alpha1.SetupIchorInstallationWebhookWithManager(mgr, webhookv1alpha1.IchorInstallationOptions{
				SettingsReadOnly: settingsReadOnly, ExemptUsers: exempt,
				Recorder: mgr.GetEventRecorderFor("ichor-settings"), //nolint:staticcheck // typed against record.EventRecorder, as above
			}), "Failed to create webhook", "webhook", "IchorInstallation")
			must(webhookv1alpha1.SetupBreakGlassWebhookWithManager(mgr, webhookv1alpha1.BreakGlassOptions{
				ExemptUsers: exempt,
			}), "Failed to create webhook", "webhook", "BreakGlass")
			must(webhookv1alpha1.SetupMachineReclaimWebhookWithManager(mgr), "Failed to create webhook", "webhook", "MachineReclaim")
			mgr.GetWebhookServer() // idempotent; makes sure the server is a runnable even if no handler did
			close(webhooksUp)
		}()
	}

	// The metrics endpoint's serving certificate, provisioned the same way as the webhook's
	// above: a second cert-controller rotator in the same process. CertRotator's
	// own ControllerName doc comment says this is supported ("allows registering multiple
	// cert-rotator controllers... unless rotating multiple certificate secrets") -- each
	// instance keeps its own reader/writer/channels, so nothing but the controller name (which
	// addController registers on the manager, and would collide on the shared default) needs
	// to differ. Gated on metricsCertPath, the same signal metricsServerOptions.CertDir above
	// already uses for "a real certificate was requested" -- unset (the default) leaves the
	// metrics server on controller-runtime's own ephemeral self-signed cert, untouched.
	// cert-controller v0.16.0 has no Create path (confirmed to
	// apply here too by reading pkg/rotator.writeSecret, which only ever calls Update): the
	// Secret this targets must already exist, empty, shipped by config/default/
	// metrics_server_cert_secret.yaml.
	if len(metricsCertPath) > 0 {
		must(rotator.AddRotator(mgr, &rotator.CertRotator{
			SecretKey:             types.NamespacedName{Namespace: ns, Name: metricsSecretName},
			CertDir:               metricsCertPath,
			CertName:              metricsCertName,
			KeyName:               metricsCertKey,
			CAName:                "ichor-ca",
			CAOrganization:        "ichor",
			DNSName:               fmt.Sprintf("%s.%s.svc", metricsServiceName, ns),
			FieldOwner:            "ichor-controller",
			ControllerName:        "ichor-metrics-cert-rotator",
			RequireLeaderElection: true,
		}), "Failed to set up metrics certificate rotation")
	}

	// Only the drains (drain.go: removal, and the config rollout before a reboot) need a raw Clientset (k8s.io/kubectl's
	// drain helper takes kubernetes.Interface, not controller-runtime's
	// cached client.Client); a failure here is fatal rather than leaving
	// removal silently unable to drain.
	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	must(err, "Failed to create Kubernetes client")

	must((&controller.TalosMachineReconciler{
		ControllerNamespace: ns,
		Client:              mgr.GetClient(),
		APIReader:           mgr.GetAPIReader(),
		TalosClient:         controller.NewMachineryTalosClient,
		MaintenanceClient:   controller.NewMachineryMaintenanceClient,
		KubeClient:          kubeClient,
		Evacuator:           evac,
		Recorder:            mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "talosmachine")
	must((&controller.TalosClusterReconciler{
		Client: mgr.GetClient(),
		Discovery: &controller.DiscoveryReconciler{
			Client: mgr.GetClient(),
			// GetEventRecorderFor is deprecated in favor of the newer events/v1
			// API, but DiscoveryReconciler.Recorder (and its fakes in
			// discovery_test.go) are typed against record.EventRecorder, the
			// older interface this returns; migrating both is a separate,
			// unrelated change.
			Recorder:          mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck
			MaintenanceClient: controller.NewMachineryMaintenanceClient,
		},
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "taloscluster")
	must((&controller.CertExpiryReconciler{
		ControllerNamespace: ns,
		Client:              mgr.GetClient(),
		ExpiryWarning:       time.Duration(certExpiryWarningDays) * 24 * time.Hour,
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "certexpiry")
	must((&controller.IssuerTrustReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
		Namespace: ns, UIServiceName: uiServiceName,
		RefreshLifetime: refreshLifetime, Settings: settingsReader, UserIssuer: userIssuer, ExternalURL: externalURL,
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "issuertrust")
	must((&controller.CNIHandoverReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Kube: kubeClient,
		Namespace: ns,
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "cnihandover")
	must((&controller.CNIDetectReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "cnidetect")
	must((&controller.TalosStorageReconciler{
		ControllerNamespace: ns,
		Client:              mgr.GetClient(), APIReader: mgr.GetAPIReader(),
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "talosstorage")
	// The backbone (TalosOperationReconciler) never branches on spec.kind itself, so the kind-to-executor selection
	// happens here, in the wiring: Talos, Kubernetes, Ichor and the CNI kube-proxy switch. An unknown kind pauses
	// immediately with a plain error -- never silently treated as another kind.
	talosUpgradeAdvance := controller.NewTalosUpgradeAdvancer(
		mgr.GetClient(), ns, controller.NewMachineryTalosClient,
		evac,
	)
	kubernetesUpgradeAdvance := controller.NewKubernetesUpgradeAdvancer(
		mgr.GetClient(), mgr.GetAPIReader(), ns, controller.NewMachineryTalosClient,
		controller.NewKubernetesAdmin(),
		evac, kubeClient, os.Getenv("NODE_NAME"),
	)

	configRolloutAdvance := controller.NewConfigRolloutAdvancer(
		mgr.GetClient(), ns, controller.NewMachineryTalosClient,
		evac, kubeClient,
	)

	ichorUpgradeAdvance := controller.NewIchorUpgradeAdvancer(mgr.GetClient(), ns, noCiliumPolicies)
	cniKubeProxyAdvance := controller.NewCNIKubeProxyAdvancer(
		mgr.GetClient(), mgr.GetAPIReader(), ns, controller.NewMachineryTalosClient,
		controller.NewKubeProxyAdmin(),
	)

	// The shutdown kind is not a rollout and has no Advance entry: the backbone
	// dispatches it to its own state machine, which needs a Talos dialer, the bundle's
	// namespace, and the node this pod runs on (NODE_NAME, from the downward API) so that
	// node can be stopped last.
	opReconciler := &controller.TalosOperationReconciler{
		ControllerNamespace: ns,
		Client:              mgr.GetClient(),
		APIReader:           mgr.GetAPIReader(),
		TalosClient:         controller.NewMachineryTalosClient,
		NodeName:            os.Getenv("NODE_NAME"),
		// The ca-rotation kind is, like shutdown, dispatched by the backbone to its own
		// state machine: it needs the Kubernetes Node readiness check and an Event recorder.
		NodesReady:      controller.NewNodesReady(),
		RootCAPublished: controller.NewRootCAPublished(),
		Recorder:        mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck // typed against record.EventRecorder, as above
		// The node kind drains with the same clientset and storage hook as the
		// config rollout.
		KubeClient: kubeClient,
		Evacuator:  evac,
		Advance: func(
			ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			switch op.Spec.Kind {
			case ichorv1alpha1.TalosOperationKindTalos:
				return talosUpgradeAdvance(ctx, cluster, m, op)
			case ichorv1alpha1.TalosOperationKindKubernetes:
				return kubernetesUpgradeAdvance(ctx, cluster, m, op)
			case ichorv1alpha1.TalosOperationKindConfig:
				return configRolloutAdvance(ctx, cluster, m, op)
			case ichorv1alpha1.TalosOperationKindIchor:
				return ichorUpgradeAdvance(ctx, cluster, m, op)
			case ichorv1alpha1.TalosOperationKindCNI:
				return cniKubeProxyAdvance(ctx, cluster, m, op)
			default:
				return false, fmt.Errorf("no executor wired up for operation kind %q yet", op.Spec.Kind)
			}
		},
	}
	must(opReconciler.SetupWithManager(mgr), "Failed to create controller", "controller", "talosoperation")
	// An etcd restore rolls the Kubernetes API back past its own operation. When the
	// controller starts again, with the API up, the operation is recreated from the marker kept
	// next to the backups.
	must(mgr.Add(manager.RunnableFunc(opReconciler.ResumeEtcdRestoresUntilDone)), "Failed to add the etcd restore resumer")
	// Every replica, not only the leader, keeps its own trust pool right during a CA rotation.
	must(mgr.Add(&controller.CATrustSync{
		Reader: mgr.GetAPIReader(), ControllerNamespace: ns,
		Pool: kubetrust.Shared, File: kubetrust.TrustFile, Informers: mgr.GetCache(),
	}), "Failed to add the CA trust sync")
	must((&controller.TalosEtcdBackupReconciler{
		ControllerNamespace: ns,
		Client:              mgr.GetClient(),
		SnapshotClient:      controller.NewMachineryEtcdSnapshotClient,
		SpoolDir:            backupSpoolDir,
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "talosetcdbackup")
	ichorInstReconciler := &controller.IchorInstallationReconciler{
		Client: mgr.GetClient(), DeployedBy: deployedBy, SettingsFallback: settingsFallback, SettingsReadOnly: settingsReadOnly,
	}
	must(ichorInstReconciler.SetupWithManager(mgr), "Failed to create controller", "controller", "ichorinstallation")
	// Create the singleton once if it is absent. The admission webhook may
	// not be serving yet, so a failed create is retried; losing the race is not an error.
	must(mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			if err := ichorInstReconciler.Seed(ctx); err != nil {
				setupLog.Info("Could not create the IchorInstallation yet", "error", err.Error())

				return false, nil
			}

			return true, nil
		})
	})), "Failed to add the IchorInstallation seed")
	must((&controller.MachineReclaimReconciler{
		Client:              mgr.GetClient(),
		ControllerNamespace: ns,
		Recorder:            mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck // typed against record.EventRecorder, as above
	}).SetupWithManager(mgr), "Failed to create controller", "controller", "machinereclaim")
	// +kubebuilder:scaffold:builder

	must(mgr.AddHealthzCheck("healthz", healthz.Ping), "Failed to set up health check")
	must(mgr.AddReadyzCheck("readyz", healthz.Ping), "Failed to set up ready check")

	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		// Ready means the webhook server answers: otherwise a replaced pod is 1/1 Ready while
		// admission calls still fail (seen as "failed calling webhook" right after a rotation).
		// Not mgr.GetWebhookServer() here: that would start the server before its certificate exists.
		must(mgr.AddReadyzCheck("webhook", gatedChecker(webhooksUp, func() healthz.Checker {
			return mgr.GetWebhookServer().StartedChecker()
		})), "Failed to set up webhook ready check")

		logCertStatusBeforeStart(setupLog, "webhook", webhookCertPath, webhookCertName)
	}

	setupLog.Info("Starting manager")
	err = mgr.Start(sigCtx)

	// An etcd restore takes the Kubernetes API away, which costs the manager its leader
	// lease and stops it. The restore is then mid-way (etcd wiped, snapshot not yet recovered) and
	// only this process can finish it, so it is waited for before the process exits.
	controller.WaitForRestoreTails(restoreTailGrace, probeAddr)

	must(err, "Failed to run manager")
}
