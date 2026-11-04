// Command verifyimage checks, with the same code the controller runs before an Ichor upgrade, that
// an image or chart in a registry carries a keyless cosign signature of the Ichor release workflow
// for a tag (ADR 0122):
//
//	verifyimage REPOSITORY sha256:DIGEST TAG
//
// The release workflow runs it right after cosign signs, so a signature the controller would refuse
// fails the release instead of the first upgrade. It reads the registry with the docker config's
// credentials (the workflow's `crane auth login`).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/maeshinshin/ichor/internal/imagesig"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verifyimage REPOSITORY sha256:DIGEST TAG")
		os.Exit(2)
	}

	repository, digest, tag := os.Args[1], os.Args[2], os.Args[3]

	v, err := imagesig.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "verifyimage:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := v.WithKeychain(authn.DefaultKeychain).Verify(ctx, imagesig.Image{Repository: repository, Digest: digest}, imagesig.IchorIdentity(tag)); err != nil {
		fmt.Fprintln(os.Stderr, "verifyimage:", err)
		os.Exit(1)
	}

	fmt.Printf("%s@%s is signed by the Ichor release workflow for %s\n", repository, digest, tag)
}
