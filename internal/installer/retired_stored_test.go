package installer

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// A stored v0.2.4 patch that holds tls.clientIdentity or extraManifestHeaders makes the check refuse, naming the patch
// and the field and the way out, and the check writes nothing.
func TestCheckStoredRetiredFieldsRefusesAndWritesNothing(t *testing.T) {
	registry := []any{map[string]any{kKind: kConfigKind, kConfigKey: map[string]any{kVersion: kV1, "machine": map[string]any{"registries": map[string]any{
		"config": map[string]any{"reg.example": map[string]any{"tls": map[string]any{"clientIdentity": map[string]any{"crt": "x", "key": "y"}}}},
	}}}}}
	headers := []any{map[string]any{kKind: kConfigKind, kConfigKey: map[string]any{kVersion: kV1, kCluster: map[string]any{"extraManifestHeaders": map[string]any{"Authorization": "t"}}}}}

	writes := 0
	count := func() { writes++ }
	c := fake.NewClientBuilder().WithObjects(storedPatch("reg", registry), storedPatch("hdr", headers), storedPatch("ok", certSANDocs)).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
				count()
				return c.Create(ctx, o, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
				count()
				return c.Update(ctx, o, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
				count()
				return c.Patch(ctx, o, p, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
				count()
				return c.Delete(ctx, o, opts...)
			},
			SubResource: func(c client.WithWatch, s string) client.SubResourceClient { count(); return c.SubResource(s) },
		}).Build()

	err := CheckStoredRetiredFields(context.Background(), c, "")
	if err == nil {
		t.Fatal("the check did not refuse")
	}

	for _, want := range []string{
		"default/reg spec.documents[0].config.machine.registries.config.reg.example.tls.clientIdentity",
		"default/hdr spec.documents[0].config.cluster.extraManifestHeaders",
		"move it out of the patch and set it on the node outside Ichor (ADR 0152) before upgrading",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}

	if strings.Contains(err.Error(), "default/ok") {
		t.Errorf("a patch without the fields is named:\n%v", err)
	}

	if writes != 0 {
		t.Errorf("%d writes", writes)
	}
}
