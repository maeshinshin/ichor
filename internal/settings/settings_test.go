package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func bp(b bool) *bool { return &b }

func inst() *ichorv1alpha1.IchorInstallation {
	return &ichorv1alpha1.IchorInstallation{
		Name: SingletonName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Reclaim:  &ichorv1alpha1.IchorReclaimSettings{Enabled: bp(true)},
			Users:    &ichorv1alpha1.IchorUserSettings{TokenTTL: &metav1.Duration{Duration: 10 * time.Minute}},
			Defaults: &ichorv1alpha1.IchorDefaultSettings{DriftPolicy: "autoCorrect"},
		},
	}
}

var fb = Fallback{TokenTTL: 5 * time.Minute, RefreshLifetime: 12 * time.Hour}

func TestResolvePrecedence(t *testing.T) {
	e := Resolve(inst(), fb, false)
	if !e.Reclaim.V || e.Reclaim.Source != ichorv1alpha1.IchorSourceSpec ||
		e.TokenTTL.V != 10*time.Minute || e.TokenTTL.Source != ichorv1alpha1.IchorSourceSpec ||
		e.DriftPolicy.V != "autoCorrect" || e.DriftPolicy.Source != ichorv1alpha1.IchorSourceSpec {
		t.Fatalf("set fields must win: %+v", e)
	}

	if e.RefreshLifetime.V != 12*time.Hour || e.RefreshLifetime.Source != ichorv1alpha1.IchorSourceDeployment {
		t.Fatalf("unset field must fall back: %+v", e.RefreshLifetime)
	}

	e = Resolve(nil, Fallback{ReclaimEnabled: true}, false)
	if !e.Reclaim.V || e.Reclaim.Source != ichorv1alpha1.IchorSourceDeployment || e.DriftPolicy.V != "warnOnly" {
		t.Fatalf("no object means the fallback: %+v", e)
	}
}

func TestResolveReadOnlyIgnoresSpec(t *testing.T) {
	e := Resolve(inst(), fb, true)
	if e.Reclaim.V || e.TokenTTL.V != 5*time.Minute || e.DriftPolicy.V != "warnOnly" {
		t.Fatalf("read-only must ignore spec: %+v", e)
	}

	for _, s := range []string{e.Reclaim.Source, e.TokenTTL.Source, e.DriftPolicy.Source} {
		if s != ichorv1alpha1.IchorSourceDeployment {
			t.Fatalf("source = %s", s)
		}
	}
}

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}

	return s
}

func TestReaderLiveAndFailClosed(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme(t)).WithObjects(inst()).Build()
	r := &Reader{Client: c, Fallback: fb}

	if !r.ReclaimEnabled(ctx) {
		t.Fatal("spec reclaim true must be read")
	}

	// A change is seen on the next call (TTL 0).
	cur := inst()
	if err := c.Get(ctx, client.ObjectKeyFromObject(cur), cur); err != nil {
		t.Fatal(err)
	}

	cur.Spec.Reclaim.Enabled = bp(false)
	if err := c.Update(ctx, cur); err != nil {
		t.Fatal(err)
	}

	if r.ReclaimEnabled(ctx) {
		t.Fatal("the change must take effect on the next call")
	}

	// A read error with nothing cached: reclaim off even if the flag is on.
	bad := fake.NewClientBuilder().WithScheme(scheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("boom")
		},
	}).Build()
	if (&Reader{Client: bad, Fallback: Fallback{ReclaimEnabled: true}}).ReclaimEnabled(ctx) {
		t.Fatal("must fail closed")
	}

	// A missing object is the fallback.
	none := fake.NewClientBuilder().WithScheme(scheme(t)).Build()
	if !(&Reader{Client: none, Fallback: Fallback{ReclaimEnabled: true}}).ReclaimEnabled(ctx) {
		t.Fatal("absent object must use the flag")
	}
}
