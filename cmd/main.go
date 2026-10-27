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
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	webhookv1alpha1 "github.com/maeshinshin/ichor/internal/webhook/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// webhookSecretName, webhookServiceName and validatingWebhookConfigName name the
// resources the in-process certificate rotator maintains; all three names come from
// the kustomize namePrefix in config/default (ADR 0006).
const (
	webhookSecretName           = "ichor-webhook-server-cert"
	webhookServiceName          = "ichor-webhook-service"
	validatingWebhookConfigName = "ichor-validating-webhook-configuration"

	// metricsSecretName and metricsServiceName name the second cert-controller rotator's
	// Secret and the metrics Service its certificate's DNSName matches (config/default/
	// metrics_server_cert_secret.yaml, config/default/metrics_service.yaml) -- opt-in,
	// gated on --metrics-cert-path actually being set, unlike the webhook rotator above.
	metricsSecretName  = "ichor-metrics-server-cert"
	metricsServiceName = "ichor-controller-manager-metrics-service"

	// backupSpoolDir is the emptyDir both manifests mount for TalosEtcdBackup's staged
	// (encrypted) snapshots, since the root filesystem is read-only (ADR 0047).
	backupSpoolDir = "/var/spool/ichor"

	// restoreTailGrace is how long the process outlives its manager while an etcd restore is mid-way
	// (ADR 0089): the restore's own bound for the wipe and the recovery, plus a margin.
	restoreTailGrace = 70 * time.Minute
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// The cert-controller rotator only ever touches the single Secret named by SecretKey,
// in this controller's own namespace (ADR 0006). Kubernetes RBAC cannot restrict list,
// watch or create by resourceNames (the request has no single object name to check), so
// those stay namespace-scoped; get, update and patch are restricted to that one Secret.
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-webhook-server-cert,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,resourceNames=ichor-metrics-server-cert,verbs=get;update;patch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=list;watch;create
// cert-controller reads the webhook configuration through an informer, which lists and watches
// the whole kind (no object name in the request), so list and watch cannot be scoped to one
// name (ADR 0081); get, update and patch can.
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=list;watch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,resourceNames=ichor-validating-webhook-configuration,verbs=get;update;patch

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ichorv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// logCertStatusBeforeStart logs, once, whether certDir/certName already exists right before
// mgr.Start below. It is not a guess: ADR 0031 read controller-runtime's own manager
// internals to confirm that server's own Runnable starts its listener concurrently with the
// cert-controller rotator that (eventually) writes this file, and that the listener does not
// retry if it loses that race -- only Kubernetes' pod restart does, by trying again from
// scratch. Without this line, a brand-new install's first restart or two shows up as
// CrashLoopBackOff plus a "no such file" TLS error with nothing in the logs saying that is
// expected, which is indistinguishable from a real misconfiguration.
func logCertStatusBeforeStart(log logr.Logger, server, certDir, certName string) {
	path := filepath.Join(certDir, certName)
	if _, err := os.Stat(path); err != nil {
		log.Info("certificate not written yet for this server; this is expected on a brand-new "+
			"install and resolves on its own once cert-controller's rotator writes the Secret and "+
			"the mounted volume picks it up -- if this server's listener loses that race, the "+
			"process exits and Kubernetes restarts it, which is one or two expected restarts, not "+
			"a failure (ADR 0006, ADR 0031)",
			"server", server, "path", path)
	}
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var certExpiryWarningDays int
	var userIssuer bool
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
	flag.BoolVar(&userIssuer, "user-issuer", false,
		"Keep the signing keys, internal TLS and trust ConfigMap for ichor-ui's own OIDC issuer (ADR 0094).")
	flag.StringVar(&uiServiceName, "ui-service-name", controller.DefaultUIServiceName,
		"The ClusterIP Service in front of ichor-ui, named by the issuer's internal certificate.")
	flag.DurationVar(&refreshLifetime, "oidc-refresh-lifetime", controller.DefaultRefreshLifetime,
		"The longest-lived token ichor-ui signs; a rotated-out signing key is kept this long.")
	flag.IntVar(&certExpiryWarningDays, "certificate-expiry-warning-days", 90,
		"Days before a cluster CA expires at which the CertificatesExpiring condition turns True (ADR 0063).")
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
	// certificate-issuing dependency by default (ADR 0006); how that directory gets populated is up to
	// the deployment.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		Cache:                  controller.CacheOptions(os.Getenv("POD_NAMESPACE")),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "61779df1.maesh.dev",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		// The webhook serving certificate has no caBundle and no Secret data shipped with
		// the chart (ADR 0006): it is generated at runtime by cert-controller and read back
		// by controller-runtime's certwatcher via webhookCertPath/CertName/KeyName above.
		// certsReady only delays SetupTalosClusterWebhookWithManager -- *admission handler
		// registration*, not the webhook server's own listener, which controller-runtime
		// starts regardless and which needs the cert file to exist the moment it does (no
		// retry if it doesn't). On a cold start (Secret shipped empty, no data yet) that is
		// a real race against this rotator's own first write, not something this gate
		// prevents; ADR 0006's "Consequences" already accepts the resulting crash-loop as
		// the intended failure direction (framed there around the Secret being deleted, the
		// same mechanism as a cold start), and ADR 0031 confirmed it against
		// controller-runtime's actual manager internals rather than taking this comment's
		// older claim on faith.
		certsReady := make(chan struct{})
		if err := rotator.AddRotator(mgr, &rotator.CertRotator{
			SecretKey:      types.NamespacedName{Namespace: os.Getenv("POD_NAMESPACE"), Name: webhookSecretName},
			CertDir:        webhookCertPath,
			CertName:       webhookCertName,
			KeyName:        webhookCertKey,
			CAName:         "ichor-ca",
			CAOrganization: "ichor",
			DNSName:        fmt.Sprintf("%s.%s.svc", webhookServiceName, os.Getenv("POD_NAMESPACE")),
			IsReady:        certsReady,
			FieldOwner:     "ichor-controller",
			// Two replicas race to generate a CA on a cold start otherwise; one writer
			// for this disruptive operation follows principle 6 (ADR 0006 amendment).
			RequireLeaderElection: true,
			Webhooks: []rotator.WebhookInfo{
				{Name: validatingWebhookConfigName, Type: rotator.Validating},
			},
		}); err != nil {
			setupLog.Error(err, "Failed to set up webhook certificate rotation")
			os.Exit(1)
		}

		go func() {
			<-certsReady
			if err := webhookv1alpha1.SetupTalosClusterWebhookWithManager(mgr); err != nil {
				setupLog.Error(err, "Failed to create webhook", "webhook", "TalosCluster")
				os.Exit(1)
			}
			if err := webhookv1alpha1.SetupTalosMachineWebhookWithManager(mgr); err != nil {
				setupLog.Error(err, "Failed to create webhook", "webhook", "TalosMachine")
				os.Exit(1)
			}
			argoNS := os.Getenv("ICHOR_ARGOCD_NAMESPACE")
			if argoNS == "" {
				argoNS = "argocd"
			}
			// ICHOR_BREAK_GLASS_EXEMPT_USERS adds exact usernames (comma separated) to the
			// built-in list, for an Argo CD or Flux that impersonates or lives elsewhere (ADR 0065).
			exempt := webhookv1alpha1.DefaultExemptUsers(os.Getenv("POD_NAMESPACE"), os.Getenv("POD_SERVICE_ACCOUNT"), argoNS)
			for u := range strings.SplitSeq(os.Getenv("ICHOR_BREAK_GLASS_EXEMPT_USERS"), ",") {
				if u = strings.TrimSpace(u); u != "" {
					exempt = append(exempt, u)
				}
			}
			if err := webhookv1alpha1.SetupBreakGlassWebhookWithManager(mgr, webhookv1alpha1.BreakGlassOptions{
				ArgoCDNamespace: argoNS, ExemptUsers: exempt,
			}); err != nil {
				setupLog.Error(err, "Failed to create webhook", "webhook", "BreakGlass")
				os.Exit(1)
			}
		}()
	}

	// The metrics endpoint's serving certificate, provisioned the same way as the webhook's
	// above (ADR 0006): a second cert-controller rotator in the same process. CertRotator's
	// own ControllerName doc comment says this is supported ("allows registering multiple
	// cert-rotator controllers... unless rotating multiple certificate secrets") -- each
	// instance keeps its own reader/writer/channels, so nothing but the controller name (which
	// addController registers on the manager, and would collide on the shared default) needs
	// to differ. Gated on metricsCertPath, the same signal metricsServerOptions.CertDir above
	// already uses for "a real certificate was requested" -- unset (the default) leaves the
	// metrics server on controller-runtime's own ephemeral self-signed cert, untouched.
	// cert-controller v0.16.0 has no Create path (ADR 0006's own recorded gotcha, confirmed to
	// apply here too by reading pkg/rotator.writeSecret, which only ever calls Update): the
	// Secret this targets must already exist, empty, shipped by config/default/
	// metrics_server_cert_secret.yaml.
	if len(metricsCertPath) > 0 {
		if err := rotator.AddRotator(mgr, &rotator.CertRotator{
			SecretKey:             types.NamespacedName{Namespace: os.Getenv("POD_NAMESPACE"), Name: metricsSecretName},
			CertDir:               metricsCertPath,
			CertName:              metricsCertName,
			KeyName:               metricsCertKey,
			CAName:                "ichor-ca",
			CAOrganization:        "ichor",
			DNSName:               fmt.Sprintf("%s.%s.svc", metricsServiceName, os.Getenv("POD_NAMESPACE")),
			FieldOwner:            "ichor-controller",
			ControllerName:        "ichor-metrics-cert-rotator",
			RequireLeaderElection: true,
		}); err != nil {
			setupLog.Error(err, "Failed to set up metrics certificate rotation")
			os.Exit(1)
		}
	}

	// Only the drains (drain.go: removal, and the config rollout before a reboot) need a raw Clientset (k8s.io/kubectl's
	// drain helper takes kubernetes.Interface, not controller-runtime's
	// cached client.Client); a failure here is fatal rather than leaving
	// removal silently unable to drain.
	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "Failed to create Kubernetes client")
		os.Exit(1)
	}

	if err := (&controller.TalosMachineReconciler{
		Client:              mgr.GetClient(),
		APIReader:           mgr.GetAPIReader(),
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		TalosClient:         controller.NewMachineryTalosClient,
		MaintenanceClient:   controller.NewMachineryMaintenanceClient,
		KubeClient:          kubeClient,
		Evacuator:           &controller.LonghornEvacuator{Client: mgr.GetClient()},
		Recorder:            mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "talosmachine")
		os.Exit(1)
	}
	if err := (&controller.TalosClusterReconciler{
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
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "taloscluster")
		os.Exit(1)
	}
	if err := (&controller.CertExpiryReconciler{
		Client:              mgr.GetClient(),
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		ExpiryWarning:       time.Duration(certExpiryWarningDays) * 24 * time.Hour,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "certexpiry")
		os.Exit(1)
	}
	if userIssuer {
		if err := (&controller.IssuerTrustReconciler{
			Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
			Namespace: os.Getenv("POD_NAMESPACE"), UIServiceName: uiServiceName,
			RefreshLifetime: refreshLifetime,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "issuertrust")
			os.Exit(1)
		}
	}
	if err := (&controller.CNIHandoverReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "cnihandover")
		os.Exit(1)
	}
	if err := (&controller.TalosStorageReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "talosstorage")
		os.Exit(1)
	}
	// The backbone (TalosOperationReconciler) never branches on spec.kind itself (ADR
	// 0026), so the kind-to-executor selection happens here, in the wiring: Talos
	// (ADR 0043), Kubernetes (ADR 0057), Ichor (ADR 0059) and the CNI kube-proxy switch (ADR 0066). An unknown kind pauses
	// immediately with a plain error -- never silently treated as another kind.
	talosUpgradeAdvance := controller.NewTalosUpgradeAdvancer(
		mgr.GetClient(), os.Getenv("POD_NAMESPACE"), controller.NewMachineryTalosClient,
		&controller.LonghornEvacuator{Client: mgr.GetClient()},
	)
	kubernetesUpgradeAdvance := controller.NewKubernetesUpgradeAdvancer(
		mgr.GetClient(), mgr.GetAPIReader(), os.Getenv("POD_NAMESPACE"), controller.NewMachineryTalosClient,
		controller.NewKubernetesAdmin(),
	)

	configRolloutAdvance := controller.NewConfigRolloutAdvancer(
		mgr.GetClient(), os.Getenv("POD_NAMESPACE"), controller.NewMachineryTalosClient,
		&controller.LonghornEvacuator{Client: mgr.GetClient()}, kubeClient,
	)

	ichorUpgradeAdvance := controller.NewIchorUpgradeAdvancer(mgr.GetClient(), os.Getenv("POD_NAMESPACE"))
	cniKubeProxyAdvance := controller.NewCNIKubeProxyAdvancer(
		mgr.GetClient(), mgr.GetAPIReader(), os.Getenv("POD_NAMESPACE"), controller.NewMachineryTalosClient,
		controller.NewKubeProxyAdmin(),
	)

	// The shutdown kind (ADR 0067) is not a rollout and has no Advance entry: the backbone
	// dispatches it to its own state machine, which needs a Talos dialer, the bundle's
	// namespace, and the node this pod runs on (NODE_NAME, from the downward API) so that
	// node can be stopped last.
	opReconciler := &controller.TalosOperationReconciler{
		Client:              mgr.GetClient(),
		APIReader:           mgr.GetAPIReader(),
		TalosClient:         controller.NewMachineryTalosClient,
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		NodeName:            os.Getenv("NODE_NAME"),
		// The ca-rotation kind (ADR 0063) is, like shutdown, dispatched by the backbone to its own
		// state machine: it needs the Kubernetes Node readiness check and an Event recorder.
		NodesReady:      controller.NewNodesReady(),
		RootCAPublished: controller.NewRootCAPublished(),
		Recorder:        mgr.GetEventRecorderFor("ichor-controller"), //nolint:staticcheck // typed against record.EventRecorder, as above
		// The node kind (ADR 0085) drains with the same clientset and storage hook as the
		// config rollout.
		KubeClient: kubeClient,
		Evacuator:  &controller.LonghornEvacuator{Client: mgr.GetClient()},
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
	if err := opReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "talosoperation")
		os.Exit(1)
	}
	// An etcd restore (ADR 0089) rolls the Kubernetes API back past its own operation. When the
	// controller starts again, with the API up, the operation is recreated from the marker kept
	// next to the backups.
	if err := mgr.Add(manager.RunnableFunc(opReconciler.ResumeEtcdRestoresUntilDone)); err != nil {
		setupLog.Error(err, "Failed to add the etcd restore resumer")
		os.Exit(1)
	}
	if err := (&controller.TalosEtcdBackupReconciler{
		Client:              mgr.GetClient(),
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		SnapshotClient:      controller.NewMachineryEtcdSnapshotClient,
		SpoolDir:            backupSpoolDir,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "talosetcdbackup")
		os.Exit(1)
	}
	directClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		setupLog.Error(err, "Failed to create uncached client")
		os.Exit(1)
	}
	argoCDNamespace := os.Getenv("ICHOR_ARGOCD_NAMESPACE")
	if argoCDNamespace == "" {
		argoCDNamespace = "argocd"
	}
	if err := (&controller.IchorInstallationReconciler{
		Client:              mgr.GetClient(),
		Direct:              directClient,
		ControllerNamespace: os.Getenv("POD_NAMESPACE"),
		ArgoCDNamespace:     argoCDNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "ichorinstallation")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		logCertStatusBeforeStart(setupLog, "webhook", webhookCertPath, webhookCertName)
	}
	if len(metricsCertPath) > 0 {
		logCertStatusBeforeStart(setupLog, "metrics", metricsCertPath, metricsCertName)
	}

	setupLog.Info("Starting manager")
	err = mgr.Start(ctrl.SetupSignalHandler())

	// An etcd restore (ADR 0089) takes the Kubernetes API away, which costs the manager its leader
	// lease and stops it. The restore is then mid-way (etcd wiped, snapshot not yet recovered) and
	// only this process can finish it, so it is waited for before the process exits.
	controller.WaitForRestoreTails(restoreTailGrace, probeAddr)

	if err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
