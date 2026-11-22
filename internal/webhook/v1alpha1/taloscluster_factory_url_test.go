package v1alpha1

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestValidateImageFactoryURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		url     string
		wantErr bool
	}{
		{url: ""},
		{url: ichorv1alpha1.DefaultImageFactoryURL},
		{url: "https://factory.example.test:8443"},
		{url: "https://factory.example.test/"},
		{url: "http://factory.example.test", wantErr: true},
		{url: "factory.example.test", wantErr: true},
		{url: "https://", wantErr: true},
		{url: "https://factory.example.test/prefix", wantErr: true},
		{url: "https://u:p@factory.example.test", wantErr: true},
		{url: "https://factory.example.test?x=1", wantErr: true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			c := &ichorv1alpha1.TalosCluster{}
			c.Spec.ImageFactoryURL = tc.url

			err := validateImageFactoryURL(c)
			if (err != nil) != tc.wantErr || (err != nil && !strings.Contains(err.Error(), "spec.imageFactoryURL")) {
				t.Fatalf("validateImageFactoryURL(%q) = %v, wantErr %v", tc.url, err, tc.wantErr)
			}
		})
	}
}
