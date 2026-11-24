package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/version"
)

// upgradeSignIn holds the sign-in flags of `ichor upgrade ichor` (init's, with the same meaning).
var upgradeSignIn initOptions

// An ichor-ui that has no sign-in cannot stay as it is: the release `ichor upgrade ichor` installs does not
// start without one. So the upgrade turns Ichor's sign-in on first, with the functions init and adopt use
// (initUsers and setClusterAuthentication), and then starts the upgrade. Consent comes first and the
// password last, as in adopt.

// signInPlanFor settles what an upgrade has to set up, writing nothing. It returns a nil plan when ichor-ui
// has a sign-in already. clusters are the TalosClusters that get the authenticator: those that can carry
// Ichor's issuer and do not have it.
func signInPlanFor(ctx context.Context, k client.Client, opts initOptions) (*signInPlan, []ichorv1alpha1.TalosCluster, error) {
	ui, err := findDeploymentByLabel(ctx, k, ichorControllerNamespace, uiComponentLabel, uiComponentValue)
	if err != nil {
		return nil, nil, err
	}

	if installer.ReadUISettings(ui, nil).SignedIn {
		if opts.UIURL != "" || opts.AdminUser != "" || opts.hasUIExtras() {
			return nil, nil, errors.New("upgrade: this Ichor's ichor-ui already has sign-in on, so --ui-url, --admin-user, --ui-tls-* and " +
				"--kube-api-* have nothing to set up: leave them out")
		}

		return nil, nil, nil
	}

	// The users step does not depend on a cluster's contract; only the authenticator does, and it is
	// kept to the clusters that can carry it.
	opts.VersionContract = initUsersMinContract

	opts, err = prepareInitUsers("upgrade", opts)
	if err != nil {
		return nil, nil, err
	}

	var list ichorv1alpha1.TalosClusterList
	if err := k.List(ctx, &list, client.InNamespace(ichorControllerNamespace)); err != nil {
		return nil, nil, fmt.Errorf("upgrade: listing the TalosClusters: %w", err)
	}

	var clusters []ichorv1alpha1.TalosCluster

	for _, c := range list.Items {
		a := c.Spec.Authentication
		if a != nil && a.Ichor != nil && a.Ichor.Enabled {
			continue
		}

		if semver.Compare(cmp.Or(c.Spec.VersionContract, c.Spec.TalosVersion), initUsersMinContract) >= 0 {
			clusters = append(clusters, c)
		}
	}

	return &signInPlan{opts: opts, users: true, authenticator: len(clusters) > 0}, clusters, nil
}

// ensureSignIn turns sign-in on for an ichor-ui that has none, after consent (--yes or an interactive yes),
// and returns the generated password of the first administrator for the caller to print last. A failure
// after the administrator exists still returns the password. Nothing is written before the consent.
func ensureSignIn(ctx context.Context, k client.Client, in io.Reader, errw io.Writer, opts initOptions, assumeYes bool) (adminUser, password string, err error) {
	plan, clusters, err := signInPlanFor(ctx, k, opts)
	if err != nil || plan == nil {
		return "", "", err
	}

	_, _ = fmt.Fprintf(errw, "upgrade: this ichor-ui has no sign-in, and the release being installed does not start without one. The upgrade will first %s\n", plan.describe())

	if !assumeYes {
		if err := confirmOrAbort(in, errw, "upgrade", "Turn sign-in on and then upgrade? [y/N]: ", "aborted, nothing was changed"); err != nil {
			return "", "", err
		}
	}

	password, err = initUsers(ctx, plan.opts, k, errw)
	if err != nil {
		return plan.opts.AdminUser, password, err
	}

	for i := range clusters {
		if err := setClusterAuthentication(ctx, k, &clusters[i], plan.opts.UIURL, errw); err != nil {
			return plan.opts.AdminUser, password, err
		}
	}

	if plan.authenticator {
		_, _ = fmt.Fprintln(errw, "upgrade: "+authRolloutNotice)
	}

	if plan.opts.UIURL != defaultUIURL {
		_, _ = fmt.Fprintln(errw, "upgrade: "+uiReachNotice(plan.opts.UIURL))
	}

	return plan.opts.AdminUser, password, nil
}

// upgradeWithSignIn is `ichor upgrade ichor` around the upgrade itself: every refusal first (preflight reads and
// writes nothing), then sign-in, the upgrade, and the generated password last and once, also when either step
// failed (by then the administrator exists and nothing else could show the password).
func upgradeWithSignIn(ctx context.Context, k client.Client, in io.Reader, errw io.Writer, opts initOptions, assumeYes bool, preflight, start func() error) error {
	if err := preflight(); err != nil {
		return err
	}

	user, password, err := ensureSignIn(ctx, k, in, errw, opts, assumeYes)
	if err == nil {
		err = start()
	}

	if password != "" {
		printGeneratedPassword(errw, user, password)
	}

	return err
}

// signInIsPending reports whether this command is going to turn sign-in on (ensureSignIn will act). It reads only.
func signInIsPending(ctx context.Context, k client.Client, opts initOptions) (bool, error) {
	plan, _, err := signInPlanFor(ctx, k, opts)

	return plan != nil, err
}

// preflightIchorUpgrade is every refusal of `ichor upgrade ichor` that can be judged before the first write: a
// downgrade (the rule the executor and the UI's plan use, version.IsDowngrade) and a stored patch that holds a field
// the new release retires. It only reads.
func preflightIchorUpgrade(ctx context.Context, k client.Reader, target string) error {
	var inst ichorv1alpha1.IchorInstallation
	if err := k.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst); err == nil {
		current := cmp.Or(inst.Status.InstalledVersion, inst.Spec.Version)
		if t := "v" + strings.TrimPrefix(target, "v"); version.IsDowngrade(current, t) {
			return fmt.Errorf("upgrade: cannot downgrade ichor from %s to %s: the executor refuses it, and nothing was written", current, t)
		}
	}

	return installer.CheckStoredRetiredFields(ctx, k)
}
